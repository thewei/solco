// [INPUT]: OMP-owned read-only JSONL under the user's own session directory
// [OUTPUT]: evidence of the exact batch marker's user turn and finished assistant turn
// [POS]: Bounded, fail-closed observer; never launches OMP, mutates history, or invents Chat answers
// [PROTOCOL]: Update header on changes; read project CLAUDE.md
package personalagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	reportReplyPrefix   = "Solco-Report-Id: "
	maxReplyFiles       = 24
	maxReplyWalkEntries = 3000
	maxReplyFileTail    = int64(8 << 20)
	maxReplyReadBudget  = int64(64 << 20)
	maxReplyJSONLine    = 1 << 20
	// OMP may have flushed the final row just before Herdr's prompt receipt.
	// Allow generous clock/flush skew, but don't scan decades of history.
	reportReplyScanSkew = 10 * time.Minute
)

// ReplyEvidence contains *provider* message IDs, not a synthesized OMP
// answer. The original transcript belongs to OMP and stays in that file.
type ReplyEvidence struct {
	State     string
	SessionID string
	MessageID string
}

// FindOMPReportReply uses the unique marker included in the Herdr-accepted
// prompt to find exactly one matching user turn and a following terminal
// assistant message in an OMP provider transcript. Herdr 0.9.5 agent.get
// doesn't expose the session file path, so this is a scoped correlation
// rather than a claim that Herdr attested the path. Ambiguity fails closed.
// Never match a toolUse, partial stream, empty response, or another user turn.
func FindOMPReportReply(home, marker string) (ReplyEvidence, error) {
	return FindOMPReportReplySince(home, marker, 0)
}

// FindOMPReportReplySince scopes candidates to sessions modified close to
// or after Herdr accepted this batch. The random marker remains the only
// association proof: timestamp is a bounding optimization, never proof of
// a reply. If a bounded candidate set is incomplete, fail closed.
func FindOMPReportReplySince(home, marker string, acceptedAtMS int64) (ReplyEvidence, error) {
	if !validReportMarker(marker) {
		return ReplyEvidence{}, ErrInvalid
	}
	var earliestModNano int64
	if acceptedAtMS > 0 {
		earliestModNano = time.UnixMilli(acceptedAtMS).Add(-reportReplyScanSkew).UnixNano()
	}
	root := filepath.Join(home, ".omp", "agent", "sessions")
	if info, err := os.Lstat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ReplyEvidence{}, nil
		}
		return ReplyEvidence{}, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ReplyEvidence{}, ErrInvalid
	}
	type file struct {
		path     string
		modified int64
		size     int64
	}
	files := make([]file, 0, 32)
	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > maxReplyWalkEntries {
			return filepath.SkipAll
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Size() <= 0 || (earliestModNano > 0 && info.ModTime().UnixNano() < earliestModNano) {
			return nil
		}
		files = append(files, file{path, info.ModTime().UnixNano(), info.Size()})
		return nil
	})
	if err != nil {
		return ReplyEvidence{}, err
	}
	// A truncated discovery set is NOT a proof of uniqueness.
	if count > maxReplyWalkEntries {
		return ReplyEvidence{}, nil
	}
	slices.SortFunc(files, func(a, b file) int {
		if a.modified > b.modified {
			return -1
		}
		if a.modified < b.modified {
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	// Observing only the first N files could verify a token that also
	// appears in an excluded file. Never turn incomplete coverage into a
	// statement that the batch has exactly one authoritative reply.
	if len(files) > maxReplyFiles {
		return ReplyEvidence{}, nil
	}
	var total int64
	for _, f := range files {
		total += min(f.size, maxReplyFileTail)
		if total > maxReplyReadBudget {
			return ReplyEvidence{}, nil
		}
	}
	var matched ReplyEvidence
	matches := 0
	for _, f := range files {
		n := min(f.size, maxReplyFileTail)
		evidence, found, e := inspectOMPReportFile(f.path, f.size, n, marker)
		if e != nil {
			// An unreadable candidate may hold a second copy of the nonce.
			// Do not present a partial scan as unique authoritative evidence.
			return ReplyEvidence{}, e
		}
		if found {
			matches++
			if matches > 1 {
				return ReplyEvidence{}, nil
			}
			matched = evidence
		}
	}
	return matched, nil
}

func validReportMarker(marker string) bool {
	if len(marker) != 32 {
		return false
	}
	for _, c := range marker {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}

type ompReportRow struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	ParentSession string `json:"parentSession"`
	Message       struct {
		Role       string `json:"role"`
		StopReason string `json:"stopReason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func (r ompReportRow) text() string {
	var b strings.Builder
	for _, block := range r.Message.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}
func inspectOMPReportFile(path string, size, limit int64, marker string) (ReplyEvidence, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReplyEvidence{}, false, err
	}
	defer f.Close()
	// OMP may prepend a title, labels or other metadata before its session
	// header. Find the authoritative session row within a small bounded
	// prefix; never infer it from a filename or a model-generated message.
	var head ompReportRow
	h := bufio.NewReaderSize(io.LimitReader(f, maxReplyJSONLine), 4096)
	foundSession := false
	for i := 0; i < 32; i++ {
		line, e := h.ReadBytes('\n')
		if e != nil {
			break
		}
		var row ompReportRow
		if json.Unmarshal(line, &row) == nil && row.Type == "session" {
			head = row
			foundSession = head.ID != "" && head.ParentSession == ""
			break
		}
	}
	if !foundSession {
		return ReplyEvidence{}, false, nil
	}
	start := max(int64(0), size-limit)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ReplyEvidence{}, false, err
	}
	reader := bufio.NewReader(io.LimitReader(f, limit))
	if start > 0 {
		// Never parse a truncated first JSONL record.
		if _, err := reader.ReadBytes('\n'); err != nil {
			return ReplyEvidence{}, false, nil
		}
	}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), maxReplyJSONLine)
	match := false
	finished := false
	var result ReplyEvidence
	for scan.Scan() {
		var row ompReportRow
		if json.Unmarshal(scan.Bytes(), &row) != nil {
			continue
		}
		if row.Type != "message" {
			continue
		}
		if row.Message.Role == "user" {
			if strings.Contains(row.text(), reportReplyPrefix+marker+"\n") {
				if match {
					// Two user turns with the same marker, even in one session:
					// neither is a unique association with this delivery.
					return ReplyEvidence{}, true, nil
				}
				match = true
			} else if match && !finished {
				// A new human turn arrived before a definitive reply;
				// do not attribute a later Assistant answer to this report.
				return ReplyEvidence{}, true, nil
			}
			continue
		}
		if !match || finished || row.Message.Role != "assistant" {
			continue
		}
		switch row.Message.StopReason {
		case "stop":
			if row.ID != "" && strings.TrimSpace(row.text()) != "" {
				result = ReplyEvidence{State: ReplyVerified, SessionID: head.ID, MessageID: row.ID}
			}
			finished = true
		case "error", "aborted":
			if row.ID != "" {
				result = ReplyEvidence{State: ReplyInterrupted, SessionID: head.ID, MessageID: row.ID}
			}
			finished = true
		}
	}
	if scan.Err() != nil {
		return ReplyEvidence{}, match, nil
	}
	return result, match, nil
}
