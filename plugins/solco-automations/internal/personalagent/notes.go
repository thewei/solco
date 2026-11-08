package personalagent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Memory is two Markdown files (2026-11-07, replacing the item/candidate/
// approval workspace): USER.md is the person's, ASSISTANT.md is the
// Assistant's. Either may edit its own at any time — the Assistant writes what
// it needs to remember at once, no approval — and the person may also edit the
// Assistant's file. Every reader (a launched Agent's context, the desktop page,
// the phone) sees the two merged. The files are the whole store: there is no
// row, version table, review or share list behind them.
//
//	<MemoryRoot>/<assistantId>/USER.md   ASSISTANT.md
//
// Safety kept from the old design, because the folder is writable by anyone
// who can run as this user: directory handles (os.Root) so a symlink swap
// cannot redirect a write, O_NOFOLLOW reads, atomic replace, a size cap, and
// credential-shaped text refused. Concurrent edits are fenced by a content
// hash (`rev`): a write names the rev it read and is refused with
// stale_revision when the file moved on, so the person's edit is never
// silently overwritten by the Assistant's tidy-up or the other way round.

const (
	NotesUserFile      = "USER.md"
	NotesAssistantFile = "ASSISTANT.md"
	// MaxNotesBytes caps each file; larger is a document, not a memory.
	MaxNotesBytes = 64 << 10
	maxNoteLine   = 2 << 10
	notesLockFile = ".lock"
)

// Which file a write targets.
const (
	NotesUser      = "user"
	NotesAssistant = "assistant"
)

var memorySafeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// NoteFile is one file's text with the hash a later write must name.
type NoteFile struct {
	Text string `json:"text"`
	Rev  string `json:"rev"`
}

// Notes is an Assistant's whole memory.
type Notes struct {
	AssistantID string   `json:"assistantId"`
	User        NoteFile `json:"user"`
	Assistant   NoteFile `json:"assistant"`
}

func noteRev(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func notesDirPath(id string) (string, error) {
	if !memorySafeID.MatchString(id) {
		return "", fmt.Errorf("%w: bad assistant id", ErrInvalid)
	}
	return filepath.Join(MemoryRoot(), id), nil
}

// openNotes opens the Assistant's folder as a directory handle. With create it
// makes the folder (0700) when missing. A symlinked root or folder is refused.
func openNotes(id string, create bool) (*os.Root, string, error) {
	dir, err := notesDirPath(id)
	if err != nil {
		return nil, "", err
	}
	root := filepath.Dir(dir)
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, dir, err
		}
	}
	rfi, err := os.Lstat(root)
	if err != nil {
		return nil, dir, err
	}
	if !rfi.IsDir() || rfi.Mode()&os.ModeSymlink != 0 {
		return nil, dir, fmt.Errorf("%w: memory root must be a real directory", ErrInvalid)
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, dir, err
	}
	defer base.Close()
	if create {
		if err := base.Mkdir(id, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, dir, err
		}
	}
	fi, err := base.Lstat(id)
	if err != nil {
		return nil, dir, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, dir, fmt.Errorf("%w: memory folder must be a real directory", ErrInvalid)
	}
	child, err := base.OpenRoot(id)
	if err != nil {
		return nil, dir, err
	}
	if actual, err := child.Stat("."); err != nil || !os.SameFile(fi, actual) {
		child.Close()
		return nil, dir, fmt.Errorf("%w: memory folder changed while opening", ErrInvalid)
	}
	return child, dir, nil
}

// readNote reads one file (absent = empty). lenient truncates an oversized file
// for read-only uses (a launch context); a write path refuses it instead.
func readNote(r *os.Root, name string, lenient bool) (NoteFile, error) {
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return NoteFile{Rev: noteRev("")}, nil
	}
	if err != nil {
		return NoteFile{}, fmt.Errorf("%w: %s cannot be read (is it a link?)", ErrInvalid, name)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return NoteFile{}, fmt.Errorf("%w: %s must be a regular file", ErrInvalid, name)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxNotesBytes+1))
	if err != nil {
		return NoteFile{}, err
	}
	if len(b) > MaxNotesBytes {
		if !lenient {
			return NoteFile{}, fmt.Errorf("%w: %s is over %d KiB; shorten it first", ErrInvalid, name, MaxNotesBytes>>10)
		}
		b = b[:MaxNotesBytes]
	}
	text := strings.ToValidUTF8(string(b), "")
	return NoteFile{Text: text, Rev: noteRev(text)}, nil
}

// writeNote replaces one file atomically inside the folder handle.
func writeNote(r *os.Root, name, text string) error {
	var tmp string
	var f *os.File
	for i := 0; i < 5; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		tmp = ".tmp-" + hex.EncodeToString(b[:])
		var err error
		f, err = r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if f == nil {
		return errors.New("cannot allocate a temporary memory file")
	}
	defer r.Remove(tmp)
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return r.Rename(tmp, name)
}

// lockNotes takes the folder's exclusive lock: the broker's CLI runs in its own
// process, so a mutex would not fence it against the desktop or the daemon.
func lockNotes(r *os.Root) (func(), error) {
	f, err := r.OpenFile(notesLockFile, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func noteCheck(text string) error {
	if len(text) > MaxNotesBytes {
		return fmt.Errorf("%w: memory is at most %d KiB per file", ErrInvalid, MaxNotesBytes>>10)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: memory must be text", ErrInvalid)
	}
	if looksLikeSecret(text) {
		return fmt.Errorf("%w: that looks like a credential; memory never keeps credentials", ErrInvalid)
	}
	return nil
}

// ensureNotes opens the Assistant's folder (making it) and carries a pre-two-file
// memory over once. The caller closes the handle.
func (s *Store) ensureNotes(id string) (*os.Root, string, error) {
	if _, err := s.GetAssistant(id); err != nil {
		return nil, "", err
	}
	r, dir, err := openNotes(id, true)
	if err != nil {
		return nil, "", err
	}
	if err := s.seedLegacyNotes(r, id); err != nil {
		r.Close()
		return nil, "", err
	}
	return r, dir, nil
}

// ReadNotes returns both files. An Assistant from before the two-file memory
// has its approved items carried into ASSISTANT.md once (the old tables stay
// untouched and are never read again).
func (s *Store) ReadNotes(id string) (Notes, error) {
	r, _, err := s.ensureNotes(id)
	if err != nil {
		return Notes{}, err
	}
	defer r.Close()
	n := Notes{AssistantID: id}
	if n.User, err = readNote(r, NotesUserFile, false); err != nil {
		return Notes{}, err
	}
	if n.Assistant, err = readNote(r, NotesAssistantFile, false); err != nil {
		return Notes{}, err
	}
	return n, nil
}

// seedLegacyNotes writes an Assistant's approved pre-2026-11-07 memory items as
// bullets into ASSISTANT.md, once (`notes_seeded:<id>` in meta). Candidates were
// never approved and are left behind; nothing is deleted.
func (s *Store) seedLegacyNotes(r *os.Root, id string) error {
	key := "notes_seeded:" + id
	var one string
	if s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&one) == nil {
		return nil
	}
	unlock, err := lockNotes(r)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := readNote(r, NotesAssistantFile, false)
	if err != nil {
		return err
	}
	if cur.Text == "" {
		rows, err := s.db.Query(`SELECT content FROM memory_items WHERE assistant_id=? AND state='approved' AND content<>'' ORDER BY created_at, rowid`, id)
		if err == nil {
			var b strings.Builder
			for rows.Next() {
				var c string
				if rows.Scan(&c) == nil && !looksLikeSecret(c) {
					b.WriteString("- " + strings.Join(strings.Fields(c), " ") + "\n")
				}
			}
			rows.Close()
			if b.Len() > 0 && b.Len() <= MaxNotesBytes {
				if err := writeNote(r, NotesAssistantFile, b.String()); err != nil {
					return err
				}
			}
		}
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO meta(key,value) VALUES(?,?)`, key, "1")
	return wrapBusy(err)
}

// WriteNotes replaces one file. expectedRev is the rev the caller read; a file
// that changed since is refused (ErrStaleRevision) so nobody's edit is lost.
func (s *Store) WriteNotes(id, which, text, expectedRev string) (Notes, error) {
	name := NotesUserFile
	switch which {
	case NotesUser:
	case NotesAssistant:
		name = NotesAssistantFile
	default:
		return Notes{}, fmt.Errorf("%w: write the user or the assistant file", ErrInvalid)
	}
	if err := noteCheck(text); err != nil {
		return Notes{}, err
	}
	if err := s.fencedWrite(id, name, text, expectedRev); err != nil {
		return Notes{}, err
	}
	return s.ReadNotes(id)
}

func (s *Store) fencedWrite(id, name, text, expectedRev string) error {
	r, _, err := s.ensureNotes(id)
	if err != nil {
		return err
	}
	defer r.Close()
	unlock, err := lockNotes(r)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := readNote(r, name, false)
	if err != nil {
		return err
	}
	if expectedRev == "" || cur.Rev != expectedRev {
		return ErrStaleRevision
	}
	if cur.Text == text {
		return nil
	}
	return writeNote(r, name, text)
}

// NoteAck is the small answer to AddAssistantNote: no memory text travels back.
type NoteAck struct {
	Added bool   `json:"added"`
	Bytes int    `json:"bytes"`
	Rev   string `json:"rev"`
}

// AddAssistantNote appends one line to ASSISTANT.md — what an Assistant does the
// moment it learns something worth keeping. A line already there (up to case
// and spacing) is not repeated, which also makes a retried call harmless.
func (s *Store) AddAssistantNote(id, note string) (NoteAck, error) {
	note = strings.Join(strings.Fields(note), " ")
	if note == "" {
		return NoteAck{}, fmt.Errorf("%w: a note needs text", ErrInvalid)
	}
	if len(note) > maxNoteLine {
		return NoteAck{}, fmt.Errorf("%w: a note is one short line (at most %d bytes); put long text through memory.write", ErrInvalid, maxNoteLine)
	}
	if err := noteCheck(note); err != nil {
		return NoteAck{}, err
	}
	r, _, err := s.ensureNotes(id)
	if err != nil {
		return NoteAck{}, err
	}
	defer r.Close()
	unlock, err := lockNotes(r)
	if err != nil {
		return NoteAck{}, err
	}
	defer unlock()
	cur, err := readNote(r, NotesAssistantFile, false)
	if err != nil {
		return NoteAck{}, err
	}
	want := normalNote(note)
	for _, line := range strings.Split(cur.Text, "\n") {
		if normalNote(line) == want {
			return NoteAck{Bytes: len(cur.Text), Rev: cur.Rev}, nil
		}
	}
	next := cur.Text
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += "- " + strings.TrimPrefix(note, "- ") + "\n"
	if len(next) > MaxNotesBytes {
		return NoteAck{}, fmt.Errorf("%w: the assistant's memory is full (%d KiB); tidy it with memory.write first", ErrInvalid, MaxNotesBytes>>10)
	}
	if err := writeNote(r, NotesAssistantFile, next); err != nil {
		return NoteAck{}, err
	}
	return NoteAck{Added: true, Bytes: len(next), Rev: noteRev(next)}, nil
}

var noteSpace = regexp.MustCompile(`\s+`)

func normalNote(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	return strings.ToLower(noteSpace.ReplaceAllString(line, " "))
}

// NotesFolder makes sure the folder and both files exist (empty until somebody
// writes) and returns the folder's path — "Open folder", and the file the person
// opens in their own editor, must have something to open. The path is not part of
// the Notes wire.
func (s *Store) NotesFolder(id string) (string, error) {
	r, dir, err := s.ensureNotes(id)
	if err != nil {
		return "", err
	}
	defer r.Close()
	unlock, err := lockNotes(r)
	if err != nil {
		return "", err
	}
	defer unlock()
	for _, name := range []string{NotesUserFile, NotesAssistantFile} {
		if _, err := r.Stat(name); errors.Is(err, os.ErrNotExist) {
			if err := writeNote(r, name, ""); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}
