package personalagent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
)

// memoryTree holds directory *handles*, not just validated names. Every
// import, projection and deletion stays beneath these handles even if another
// process renames a directory after the check. os.Root prevents symlinks from
// escaping a root and the checked child roots reject symlinked categories.
type memoryTree struct {
	assistant *os.Root
	memory    *os.Root
	dirs      map[string]*os.Root
}

func checkedMemoryChild(parent *os.Root, name string) (*os.Root, error) {
	fi, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: memory path %s must be a real directory", ErrInvalid, name)
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	actual, err := child.Stat(".")
	if err != nil || !os.SameFile(fi, actual) {
		child.Close()
		return nil, fmt.Errorf("%w: memory directory changed while opening %s", ErrInvalid, name)
	}
	return child, nil
}

func openMemoryTree(root, assistantID string) (_ *memoryTree, err error) {
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	opened, err := base.Stat(".")
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !os.SameFile(fi, opened) {
		return nil, fmt.Errorf("%w: memory root changed or is a symlink", ErrInvalid)
	}
	assistant, err := checkedMemoryChild(base, assistantID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			assistant.Close()
		}
	}()
	memory, err := checkedMemoryChild(assistant, "memory")
	if err != nil {
		return nil, err
	}
	tree := &memoryTree{assistant: assistant, memory: memory, dirs: map[string]*os.Root{"": memory}}
	for _, sub := range []string{"facts", "inbox", "notes"} {
		child, e := checkedMemoryChild(memory, sub)
		if e != nil {
			tree.Close()
			return nil, e
		}
		tree.dirs[sub] = child
	}
	return tree, nil
}

func (t *memoryTree) Close() error {
	for name, r := range t.dirs {
		if name != "" {
			_ = r.Close()
		}
	}
	if t.memory != nil {
		_ = t.memory.Close()
	}
	if t.assistant != nil {
		return t.assistant.Close()
	}
	return nil
}

func (t *memoryTree) target(rel string) (*os.Root, string, error) {
	if rel == "" || path.IsAbs(rel) || strings.Contains(rel, "\\") {
		return nil, "", ErrInvalid
	}
	sub, name, has := strings.Cut(rel, "/")
	if !has {
		sub = ""
		name = rel
	}
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return nil, "", ErrInvalid
	}
	r, ok := t.dirs[sub]
	if !ok {
		return nil, "", ErrInvalid
	}
	return r, name, nil
}

func (t *memoryTree) readSmall(rel string, limit int64) ([]byte, bool) {
	r, name, err := t.target(rel)
	if err != nil {
		return nil, false
	}
	// NOFOLLOW closes a leaf-symlink swap; NONBLOCK keeps a swapped FIFO from
	// blocking a scheduler thread. The open directory root stops parent escapes.
	file, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > limit {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(file, limit+1))
	return b, err == nil && int64(len(b)) <= limit
}

func (t *memoryTree) exists(rel string) bool {
	r, name, err := t.target(rel)
	if err != nil {
		return false
	}
	_, err = r.Lstat(name)
	return err == nil
}

func (t *memoryTree) remove(rel string) error {
	r, name, err := t.target(rel)
	if err != nil {
		return err
	}
	return r.Remove(name)
}

func (t *memoryTree) readDir(sub string) ([]os.DirEntry, error) {
	r, ok := t.dirs[sub]
	if !ok {
		return nil, ErrInvalid
	}
	d, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadDir(0)
}

// writeFileAtomic creates a temp leaf exclusively *inside the already-opened
// category root*. Rename is also relative to that root, so replacement of
// facts/ with a symlink to a second Assistant cannot redirect the projection.
func (t *memoryTree) writeFileAtomic(rel string, data []byte) error {
	r, name, err := t.target(rel)
	if err != nil {
		return err
	}
	var tmp string
	var file *os.File
	for i := 0; i < 5; i++ {
		var b [12]byte
		if _, err = rand.Read(b[:]); err != nil {
			return err
		}
		tmp = ".tmp-" + hex.EncodeToString(b[:])
		file, err = r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if file == nil {
		return fmt.Errorf("cannot allocate temporary memory file")
	}
	defer r.Remove(tmp)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return r.Rename(tmp, name)
}
