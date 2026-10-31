package nativeui

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
// the bytes to the bridge (base64 over the bound call, like a recording),
// Go saves them here, and the draft carries the `@path` mention every CLI
// turns into an image or file. Inlining the base64 into the prompt would be
// tokenised as text and wreck the context, so the path is the only form.
const (
	chatAttachDir      = "ChatAttachments"
	chatAttachMaxFile  = 32 << 20  // one file
	chatAttachMaxTotal = 200 << 20 // whole folder
	chatAttachKeep     = 14 * 24 * time.Hour
)

var chatAttachExt = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp",
	"image/heic": "heic", "image/svg+xml": "svg", "application/pdf": "pdf", "text/plain": "txt",
}

// chatAttachExtension prefers the file's own extension (letters/digits only),
// else the MIME type's, else "bin".
func chatAttachExtension(name, mime string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext != "" && len(ext) <= 8 && strings.Trim(ext, "abcdefghijklmnopqrstuvwxyz0123456789") == "" {
		return ext
	}
	if ext, ok := chatAttachExt[strings.ToLower(mime)]; ok {
		return ext
	}
	return "bin"
}

// saveChatAttachment writes data under dir (created 0700, file 0600) and
// prunes the folder, returning the absolute path.
func saveChatAttachment(dir string, data []byte, mime, name string, now time.Time) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty attachment")
	}
	if len(data) > chatAttachMaxFile {
		return "", fmt.Errorf("attachment is larger than %d MB", chatAttachMaxFile>>20)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	token := make([]byte, 3)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	path := filepath.Join(dir, now.Format("20060102-150405")+"-"+hex.EncodeToString(token)+"."+chatAttachExtension(name, mime))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	pruneChatAttachments(dir, now)
	return path, nil
}

// pruneChatAttachments drops files older than 14 days, then the oldest ones
// until the folder is within 200 MB.
func pruneChatAttachments(dir string, now time.Time) {
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
		if now.Sub(info.ModTime()) > chatAttachKeep {
			_ = os.Remove(path)
			continue
		}
		files = append(files, file{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= chatAttachMaxTotal {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
