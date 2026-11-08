// Package chatattach stores the files a Chat draft attaches (pasted, dropped or picked on the desktop; picked
// on the phone and uploaded to the Host). It is Shell-free so the desktop bridge and the remote Host share it.
package chatattach

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Pasted and dropped images reach the agent as a file path: the page hands
// the bytes over (base64), Go saves them here, and the draft carries the
// `@path` mention every CLI turns into an image or file. Inlining the base64
// into the prompt would be tokenised as text and wreck the context, so the
// path is the only form.
const (
	// DirName is the folder under the app's cache directory.
	DirName = "ChatAttachments"
	// MaxFile is the largest single attachment; a caller sizing a request body starts from it.
	MaxFile  = 32 << 20
	maxTotal = 200 << 20 // whole folder
	keep     = 14 * 24 * time.Hour
)

var extByMIME = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp",
	"image/heic": "heic", "image/svg+xml": "svg", "application/pdf": "pdf", "text/plain": "txt",
}

// Dir is where an app with this user-data directory keeps its attachments:
// ~/Library/Caches/<app>/ChatAttachments, not Application Support — the path
// rides in an `@path` mention, which agents split at whitespace, and
// "Application Support" has a space.
func Dir(userDataDir string) (string, error) {
	if userDataDir == "" {
		return "", errors.New("attachments need the installed app")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, filepath.Base(userDataDir), DirName), nil
}

// extension prefers the file's own extension (letters/digits only), else the
// MIME type's, else "bin".
func extension(name, mime string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext != "" && len(ext) <= 8 && strings.Trim(ext, "abcdefghijklmnopqrstuvwxyz0123456789") == "" {
		return ext
	}
	if ext, ok := extByMIME[strings.ToLower(mime)]; ok {
		return ext
	}
	return "bin"
}

// Save writes data under dir (created 0700, file 0600) and prunes the
// folder, returning the absolute path.
func Save(dir string, data []byte, mime, name string, now time.Time) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty attachment")
	}
	if len(data) > MaxFile {
		return "", fmt.Errorf("attachment is larger than %d MB", MaxFile>>20)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	token := make([]byte, 3)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	path := filepath.Join(dir, now.Format("20060102-150405")+"-"+hex.EncodeToString(token)+"."+extension(name, mime))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	Prune(dir, now)
	return path, nil
}

// Prune drops files older than 14 days, then the oldest ones until the
// folder is within 200 MB.
func Prune(dir string, now time.Time) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, item := range items {
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, item.Name())
		if now.Sub(info.ModTime()) > keep {
			_ = os.Remove(path)
			continue
		}
		files = append(files, file{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= maxTotal {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
