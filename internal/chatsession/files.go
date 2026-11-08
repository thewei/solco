package chatsession

import (
	"os"
	"path/filepath"
	"strings"
)

// maxFileHits bounds one `@` completion list.
const maxFileHits = 20

// ListFiles lists the entries of the project folder dir that match the
// composer's typed `@` token: shallow, a "sub/dir/" prefix descends into that
// subdirectory, contains-match on the last segment (case-insensitive), dotfiles
// only when the segment itself starts with a dot, directories end in "/".
// An unreadable or missing folder yields no suggestions, never an error.
//
// contained keeps the walk inside dir (a ".." segment or a symlink leading out
// lists nothing) — the phone Host sets it; the desktop, which is the user's own
// machine, does not.
func ListFiles(dir, token string, contained bool) []string {
	if dir == "" {
		return nil
	}
	sub, name := "", strings.ToLower(strings.TrimPrefix(token, "./"))
	if at := strings.LastIndex(name, "/"); at != -1 {
		sub, name = name[:at], name[at+1:]
	}
	target := filepath.Join(dir, filepath.FromSlash(sub))
	if contained && !within(dir, target) {
		return nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil
	}
	found := make([]string, 0, 8)
	for _, entry := range entries {
		entryName := entry.Name()
		if strings.HasPrefix(entryName, ".") && !strings.HasPrefix(name, ".") {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(entryName), name) {
			continue
		}
		label := entryName
		if entry.IsDir() {
			label += "/"
		}
		found = append(found, label)
		if len(found) >= maxFileHits {
			break
		}
	}
	return found
}

// within reports whether target, with symlinks resolved, is dir or below it.
func within(dir, target string) bool {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, real)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
