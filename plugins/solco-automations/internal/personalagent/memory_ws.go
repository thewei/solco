package personalagent

import (
	"database/sql"
	"regexp"
	"strings"
	"time"
)

// MemoryVersion is one revision of a memory item. A forgotten item keeps the
// fact that it changed, never what it said.
type MemoryVersion struct {
	MemoryID string `json:"memoryId"`
	Revision int    `json:"revision"`
	Author   string `json:"author"`
	State    string `json:"state"`
	Content  string `json:"content"`
	At       int64  `json:"at"`
}

func recordVersionTx(tx *sql.Tx, m MemoryItem, author string, now int64) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO memory_versions(memory_id,revision,author,state,content,at) VALUES(?,?,?,?,?,?)`,
		m.ID, m.Revision, author, m.State, m.Content, now)
	return err
}

// MemoryHistory lists an item's versions, newest first.
func (s *Store) MemoryHistory(id string) ([]MemoryVersion, error) {
	if _, err := getMemoryTx(s.db, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT memory_id,revision,author,state,content,at FROM memory_versions WHERE memory_id=? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []MemoryVersion{}
	for rows.Next() {
		var v MemoryVersion
		if err := rows.Scan(&v.MemoryID, &v.Revision, &v.Author, &v.State, &v.Content, &v.At); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, wrapBusy(rows.Err())
}

// StaleCandidateDays is how long a candidate may wait for a person before the
// review lists it.
const StaleCandidateDays = 14

// MemoryReview is the deterministic tidy-up report: nothing in it changes a
// memory, a person decides every line.
type MemoryReview struct {
	AssistantID     string       `json:"assistantId"`
	Approved        int          `json:"approved"`
	Candidates      int          `json:"candidates"`
	Max             int          `json:"max"`
	Duplicates      [][]string   `json:"duplicates"`
	StaleCandidates []string     `json:"staleCandidates"`
	Conflicts       []string     `json:"conflicts"`
	Items           []MemoryItem `json:"items"`
}

var spaceRun = regexp.MustCompile(`\s+`)

func normalMemory(text string) string {
	return strings.ToLower(strings.TrimSpace(spaceRun.ReplaceAllString(text, " ")))
}

// ReviewMemory reads one Assistant's memory and lists what looks wrong:
// duplicate wording, candidates nobody answered, and edits that collided.
func (s *Store) ReviewMemory(assistantID string) (MemoryReview, error) {
	list, err := s.ListMemory(assistantID)
	if err != nil {
		return MemoryReview{}, err
	}
	r := MemoryReview{AssistantID: assistantID, Max: MaxMemoryKept, Duplicates: [][]string{}, StaleCandidates: []string{}, Conflicts: []string{}, Items: []MemoryItem{}}
	now := time.UnixMilli(s.nowMS())
	seen := map[string][]string{}
	var order []string
	for _, m := range list {
		if m.State == MemoryForgotten {
			continue
		}
		r.Items = append(r.Items, m)
		switch m.State {
		case MemoryApproved:
			r.Approved++
		case MemoryCandidate:
			r.Candidates++
			if now.Sub(time.UnixMilli(m.CreatedAt)) >= StaleCandidateDays*24*time.Hour {
				r.StaleCandidates = append(r.StaleCandidates, m.ID)
			}
			if m.Kind == "conflict" {
				r.Conflicts = append(r.Conflicts, m.ID)
			}
		}
		k := normalMemory(m.Content)
		if _, ok := seen[k]; !ok {
			order = append(order, k)
		}
		seen[k] = append(seen[k], m.ID)
	}
	for _, k := range order {
		if len(seen[k]) > 1 {
			r.Duplicates = append(r.Duplicates, seen[k])
		}
	}
	return r, nil
}
