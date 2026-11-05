// [INPUT]: the pinned OMP session directory of one run (read-only JSONL OMP wrote itself)
// [OUTPUT]: WorkerResult or ErrNoResult
// [POS]: bounded, fail-closed reader; never launches OMP, never writes, never trusts an unfinished or ambiguous transcript
// [PROTOCOL]: Update header on changes; read project CLAUDE.md
package personalagent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxWorkerTranscript = int64(32 << 20)
	maxWorkerFiles      = 8
	maxWorkerLine       = 4 << 20
	maxResultSummary    = 64 << 10
	maxGroundCorpus     = 4 << 20
	maxResultSources    = 50
)

var (
	urlRe       = regexp.MustCompile(`https?://[^\s<>"'\x60\]\)]+`)
	skillURLRe  = regexp.MustCompile(`skill://([A-Za-z0-9][A-Za-z0-9_.-]*)`)
	skillPathRe = regexp.MustCompile(`/skills/([A-Za-z0-9][A-Za-z0-9_.-]*)/(?:SKILL\.md|[^"\s]*)`)
)

type ompWorkerRow struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	ParentSession string `json:"parentSession"`
	Message       struct {
		Role       string `json:"role"`
		StopReason string `json:"stopReason"`
		ToolName   string `json:"toolName"`
		IsError    bool   `json:"isError"`
		Content    []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"content"`
	} `json:"message"`
}

func (r ompWorkerRow) text() string {
	var b strings.Builder
	for _, c := range r.Message.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// ReadOMPWorkerResult reads the result of one run from the OMP session
// directory Solco pinned with `--session-dir`. It requires exactly one root
// session whose first user turn carries the run's marker, and a final
// assistant message that ended normally with text. Everything else is
// ErrNoResult: a missing directory, two sessions, a transcript that ends in a
// tool call, an error or an abort, or an empty answer.
func ReadOMPWorkerResult(dir, runID string) (WorkerResult, error) {
	if runID == "" {
		return WorkerResult{}, ErrInvalid
	}
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WorkerResult{}, ErrNoResult
		}
		return WorkerResult{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return WorkerResult{}, ErrNoResult
	}
	var files []string
	count := 0
	walkErr := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 2000 {
			return filepath.SkipAll
		}
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		return WorkerResult{}, walkErr
	}
	sort.Strings(files)
	var chosen string
	var sessionID string
	for _, f := range files {
		if len(files) > maxWorkerFiles {
			return WorkerResult{}, ErrNoResult
		}
		id, root, e := rootSessionOf(f)
		if e != nil {
			return WorkerResult{}, e
		}
		if !root {
			continue // an OMP child session of this run is not its answer
		}
		if chosen != "" {
			return WorkerResult{}, ErrNoResult // two root sessions: ambiguous
		}
		chosen, sessionID = f, id
	}
	if chosen == "" {
		return WorkerResult{}, ErrNoResult
	}
	return readWorkerTranscript(chosen, sessionID, runID)
}

func rootSessionOf(path string) (id string, root bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for i := 0; i < 32; i++ {
		line, e := r.ReadBytes('\n')
		var row ompWorkerRow
		if json.Unmarshal(line, &row) == nil && row.Type == "session" {
			return row.ID, row.ID != "" && row.ParentSession == "", nil
		}
		if e != nil {
			break
		}
	}
	return "", false, nil
}

func readWorkerTranscript(path, sessionID, runID string) (WorkerResult, error) {
	st, err := os.Stat(path)
	if err != nil {
		return WorkerResult{}, err
	}
	if st.Size() <= 0 || st.Size() > maxWorkerTranscript {
		return WorkerResult{}, ErrNoResult
	}
	f, err := os.Open(path)
	if err != nil {
		return WorkerResult{}, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), maxWorkerLine)

	res := WorkerResult{Evidence: EvidenceOMPTranscript, AgentSessionID: sessionID, Tools: map[string]int{}}
	matched := false
	var last ompWorkerRow
	haveLast := false
	skills := map[string]bool{}
	var corpus strings.Builder
	for scan.Scan() {
		var row ompWorkerRow
		if json.Unmarshal(scan.Bytes(), &row) != nil || row.Type != "message" {
			continue
		}
		switch row.Message.Role {
		case "user":
			if hasRunMarker(row.text(), runID) {
				matched = true
			}
		case "assistant":
			if !matched {
				continue
			}
			last, haveLast = row, true
			for _, c := range row.Message.Content {
				if c.Type != "toolCall" {
					continue
				}
				res.Tools[c.Name]++
				args := string(c.Arguments)
				for _, m := range skillURLRe.FindAllStringSubmatch(args, -1) {
					skills[m[1]] = true
				}
				for _, m := range skillPathRe.FindAllStringSubmatch(strings.ReplaceAll(args, `\/`, "/"), -1) {
					skills[m[1]] = true
				}
			}
		case "toolResult":
			if !matched {
				continue
			}
			if row.Message.IsError {
				res.ToolErrors++
			}
			if corpus.Len() < maxGroundCorpus {
				corpus.WriteString(row.text())
				corpus.WriteByte('\n')
			}
		}
	}
	if scan.Err() != nil || !matched || !haveLast {
		return WorkerResult{}, ErrNoResult
	}
	// The run ended well only if its last assistant row is a normal stop
	// carrying text. toolUse = still working or cut off; error/aborted = ended badly.
	text := strings.TrimSpace(last.text())
	if last.Message.StopReason != "stop" || last.ID == "" || text == "" {
		return WorkerResult{}, ErrNoResult
	}
	if len(text) > maxResultSummary {
		cut := maxResultSummary
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text, res.Truncated = text[:cut], true
	}
	res.Summary = text
	sum := sha256.Sum256([]byte(text))
	res.SHA256 = hex.EncodeToString(sum[:])
	res.MessageID = last.ID
	for s := range skills {
		res.Skills = append(res.Skills, s)
	}
	sort.Strings(res.Skills)
	seen := map[string]bool{}
	grounded := corpus.String()
	for _, u := range urlRe.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".,;:!?")
		if seen[u] || len(res.Sources) >= maxResultSources {
			continue
		}
		seen[u] = true
		res.Sources = append(res.Sources, SourceRef{URL: u, Grounded: strings.Contains(grounded, u)})
	}
	return res, nil
}
