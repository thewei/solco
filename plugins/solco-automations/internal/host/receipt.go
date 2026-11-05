// [INPUT]: an automation whose Agent transcript Solco pinned (--session-dir) and whose prompt opens with Solco-Run-Id
// [OUTPUT]: whether that transcript already holds the prompt as a user turn
// [POS]: Gap W10 — the one proof that stops a stalled-looking submit from being typed twice
// [PROTOCOL]: Update header on changes; read project CLAUDE.md
package host

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

const runMarkerPrefix = "Solco-Run-Id: "

// promptDelivered is true only when the Agent's own transcript, in the session
// directory Solco pinned, holds a user turn carrying the prompt's run marker
// line. Without a marker or a pinned directory it proves nothing and says no.
func promptDelivered(a config.Automation) bool {
	first, _, _ := strings.Cut(strings.TrimLeft(a.Prompt, "\n"), "\n")
	if !strings.HasPrefix(first, runMarkerPrefix) || strings.TrimSpace(first) == runMarkerPrefix {
		return false
	}
	dir := ""
	for i := 0; i+1 < len(a.AgentArgs); i++ {
		if a.AgentArgs[i] == "--session-dir" {
			dir = a.AgentArgs[i+1]
		}
	}
	if dir == "" {
		return false
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	files := 0
	found := false
	_ = filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
			return nil
		}
		if files++; files > 8 {
			return filepath.SkipAll
		}
		found = transcriptHolds(path, first)
		return nil
	})
	return found
}

func transcriptHolds(path, line string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), 4<<20)
	for scan.Scan() {
		var row struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scan.Bytes(), &row) != nil || row.Type != "message" || row.Message.Role != "user" {
			continue
		}
		for _, c := range row.Message.Content {
			if strings.Contains(c.Text, line+"\n") || strings.TrimSpace(c.Text) == line {
				return true
			}
		}
	}
	return false
}
