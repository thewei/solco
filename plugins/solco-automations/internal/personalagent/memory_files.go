package personalagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// MemoryFiles is the folder projection of an Assistant's memory: one Markdown
// file per item (approved → facts/, candidates → inbox/), editable with any
// editor. The SQLite rows stay the only authority — a file is trusted only
// after Sync has checked it, and a file never overwrites a newer row.
//
//	<Root>/<assistantId>/memory/
//	  README.md  INDEX.md   facts/<id>.md   inbox/<id>.md   notes/   (drop a .md anywhere → a candidate)
type MemoryFiles struct {
	Store *Store
	Root  string
}

// SyncReport counts what one Sync did.
type SyncReport struct {
	Edited    int `json:"edited"`    // a person's edit became a new revision
	Conflicts int `json:"conflicts"` // an edit on an outdated file was kept as a candidate beside the newer text
	Imported  int `json:"imported"`  // a new file became a candidate
	Refused   int `json:"refused"`   // an edit that could not be accepted (credential, too big, empty); the file was restored
	Written   int `json:"written"`
	Removed   int `json:"removed"`
}

const (
	memoryFileMax   = MaxMemoryText + 2<<10 // body plus front matter
	memoryImportMax = 20                    // new files taken per Sync
)

var memorySafeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Dir is the Assistant's memory folder.
func (f *MemoryFiles) Dir(assistantID string) (string, error) {
	if !memorySafeID.MatchString(assistantID) {
		return "", fmt.Errorf("%w: bad assistant id", ErrInvalid)
	}
	return filepath.Join(f.Root, assistantID, "memory"), nil
}

type fileRec struct {
	rel      string
	memoryID string
	revision int
	hash     string
}

func hashOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func renderMemory(m MemoryItem) []byte {
	clean := func(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\nassistantId: %s\nrevision: %d\nkind: %s\nstate: %s\nsourceRef: %s\nupdatedAt: %s\n",
		m.ID, m.AssistantID, m.Revision, clean(m.Kind), m.State, clean(m.SourceRef), time.UnixMilli(m.UpdatedAt).UTC().Format(time.RFC3339))
	b.WriteString("---\n")
	b.WriteString(strings.TrimSpace(m.Content))
	b.WriteString("\n")
	return []byte(b.String())
}

// memoryBody is the text after an optional front-matter block.
func memoryBody(raw []byte) string {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if strings.HasPrefix(s, "---\n") {
		rest := s[4:]
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			return strings.TrimSpace(rest[i+5:])
		}
		if strings.HasSuffix(rest, "\n---") {
			return ""
		}
	}
	return strings.TrimSpace(s)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// readSmall reads a regular, non-symlink file no larger than limit.
func readSmall(path string, limit int64) ([]byte, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > limit {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (f *MemoryFiles) records(assistantID string) (map[string]fileRec, error) {
	rows, err := f.Store.db.Query(`SELECT rel,memory_id,revision,hash FROM memory_files WHERE assistant_id=?`, assistantID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := map[string]fileRec{}
	for rows.Next() {
		var r fileRec
		if err := rows.Scan(&r.rel, &r.memoryID, &r.revision, &r.hash); err != nil {
			return nil, err
		}
		out[r.rel] = r
	}
	return out, wrapBusy(rows.Err())
}

func (f *MemoryFiles) putRecord(assistantID string, r fileRec) error {
	_, err := f.Store.db.Exec(`INSERT OR REPLACE INTO memory_files(assistant_id,rel,memory_id,revision,hash) VALUES(?,?,?,?,?)`,
		assistantID, r.rel, r.memoryID, r.revision, r.hash)
	return wrapBusy(err)
}

func (f *MemoryFiles) dropRecord(assistantID, rel string) error {
	_, err := f.Store.db.Exec(`DELETE FROM memory_files WHERE assistant_id=? AND rel=?`, assistantID, rel)
	return wrapBusy(err)
}

// Sync reconciles one Assistant's folder with its memory: first what a person
// changed on disk (checked, then committed as ordinary memory edits), then
// what changed in the core (projected into the files).
func (f *MemoryFiles) Sync(assistantID string) (SyncReport, error) {
	var rep SyncReport
	dir, err := f.Dir(assistantID)
	if err != nil {
		return rep, err
	}
	if _, err := f.Store.GetAssistant(assistantID); err != nil {
		return rep, err
	}
	for _, d := range []string{"", "facts", "inbox", "notes"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return rep, err
		}
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(dir), ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return rep, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return rep, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	recs, err := f.records(assistantID)
	if err != nil {
		return rep, err
	}
	items, err := f.Store.ListMemory(assistantID)
	if err != nil {
		return rep, err
	}
	byID := map[string]MemoryItem{}
	for _, m := range items {
		byID[m.ID] = m
	}

	// 1. What a person edited in an existing file.
	rels := make([]string, 0, len(recs))
	for rel := range recs {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		rec := recs[rel]
		raw, ok := readSmall(filepath.Join(dir, rel), memoryFileMax)
		if !ok {
			if _, err := os.Lstat(filepath.Join(dir, rel)); err == nil {
				rep.Refused++ // a symlink, a directory or an oversized file: never read, rewritten below
			}
			continue
		}
		h := hashOf(raw)
		if h == rec.hash {
			continue
		}
		m, known := byID[rec.memoryID]
		if !known || m.State == MemoryForgotten {
			continue // removed in pass 3
		}
		body := memoryBody(raw)
		if normalMemory(body) == normalMemory(m.Content) {
			continue // only the front matter moved; restored in pass 3
		}
		if body == "" {
			rep.Refused++
			continue
		}
		rid := "file:" + rec.memoryID + ":" + h[:16]
		if rec.revision == m.Revision {
			_, _, err := f.Store.correctAs(MemByFiles, rid, m.ID, body, m.Revision)
			if err == nil {
				rep.Edited++
				continue
			}
			if !errors.Is(err, ErrStaleRevision) {
				rep.Refused++
				continue
			}
			// Raced with a change in the core: handled as the conflict it is.
		}
		// The text changed in the core after this file was written: keep both.
		if _, _, err := f.Store.proposeAs(MemByFiles, rid, ProposeMemoryInput{AssistantID: assistantID, Content: body,
			SourceRef: fmt.Sprintf("conflict:%s@%d", m.ID, rec.revision), Kind: "conflict"}); err != nil {
			rep.Refused++
			continue
		}
		rep.Conflicts++
	}

	// 2. New files become candidates; nothing a person drops in is trusted blindly.
	for _, sub := range []string{"facts", "inbox", "notes"} {
		entries, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, e := range entries {
			rel := sub + "/" + e.Name()
			if _, tracked := recs[rel]; tracked || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if rep.Imported >= memoryImportMax {
				break
			}
			raw, ok := readSmall(filepath.Join(dir, rel), memoryFileMax)
			body := memoryBody(raw)
			if !ok || body == "" || len(body) > MaxMemoryText {
				rep.Refused++
				continue
			}
			if _, _, err := f.Store.proposeAs(MemByFiles, "file-new:"+hashOf([]byte(rel + "\x00" + body))[:24],
				ProposeMemoryInput{AssistantID: assistantID, Content: body, SourceRef: "file:" + rel, Kind: "note"}); err != nil {
				rep.Refused++
				continue
			}
			rep.Imported++
			_ = os.Remove(filepath.Join(dir, rel)) // its text is now a candidate; the canonical file is written below
		}
	}

	// 3. Project the core into the folder.
	items, err = f.Store.ListMemory(assistantID)
	if err != nil {
		return rep, err
	}
	byID = map[string]MemoryItem{}
	for _, m := range items {
		byID[m.ID] = m
	}
	recs, err = f.records(assistantID)
	if err != nil {
		return rep, err
	}
	for rel, rec := range recs {
		m, known := byID[rec.memoryID]
		want := ""
		if known && m.State == MemoryApproved {
			want = "facts/" + m.ID + ".md"
		} else if known && m.State == MemoryCandidate {
			want = "inbox/" + m.ID + ".md"
		}
		if rel != want {
			if err := os.Remove(filepath.Join(dir, rel)); err == nil {
				rep.Removed++
			}
			if err := f.dropRecord(assistantID, rel); err != nil {
				return rep, err
			}
		}
	}
	for _, m := range items {
		var rel string
		switch m.State {
		case MemoryApproved:
			rel = "facts/" + m.ID + ".md"
		case MemoryCandidate:
			rel = "inbox/" + m.ID + ".md"
		default:
			continue
		}
		data := renderMemory(m)
		path := filepath.Join(dir, rel)
		if cur, ok := readSmall(path, memoryFileMax); !ok || hashOf(cur) != hashOf(data) {
			if err := writeFileAtomic(path, data); err != nil {
				return rep, err
			}
			rep.Written++
		}
		want := fileRec{rel: rel, memoryID: m.ID, revision: m.Revision, hash: hashOf(data)}
		if recs[rel] == want {
			continue
		}
		if err := f.putRecord(assistantID, want); err != nil {
			return rep, err
		}
	}
	if err := f.writeIndex(dir, items); err != nil {
		return rep, err
	}
	return rep, nil
}

const memoryReadme = `# Memory

One Markdown file per remembered item.

- facts/   what this Assistant remembers (approved)
- inbox/   candidates waiting for your answer
- notes/   drop a .md file here (or in facts/ or inbox/): it becomes a candidate

Edit a file and save: Solco checks it and keeps it as a new revision. If the item
changed in Solco meanwhile, your text is kept as a separate candidate next to the
newer one — nothing is overwritten silently. Deleting a file does not forget the
item; forget it in Solco.

These files are plain text. Do not put passwords or API keys here — Solco refuses
text that looks like a credential.
`

func (f *MemoryFiles) writeIndex(dir string, items []MemoryItem) error {
	var b strings.Builder
	b.WriteString("# Index\n\n")
	n := 0
	for _, m := range items {
		if m.State == MemoryForgotten {
			continue
		}
		line, _, _ := strings.Cut(strings.TrimSpace(m.Content), "\n")
		if len(line) > 120 {
			line = line[:120] + "…"
		}
		fmt.Fprintf(&b, "- %s · %s · %s — %s\n", m.ID, m.State, m.Kind, line)
		n++
	}
	if n == 0 {
		b.WriteString("Nothing remembered yet.\n")
	}
	for name, data := range map[string][]byte{"INDEX.md": []byte(b.String()), "README.md": []byte(memoryReadme)} {
		path := filepath.Join(dir, name)
		if cur, ok := readSmall(path, 1<<20); ok && string(cur) == string(data) {
			continue
		}
		if err := writeFileAtomic(path, data); err != nil {
			return err
		}
	}
	return nil
}
