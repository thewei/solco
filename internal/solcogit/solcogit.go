// Package solcogit is Solco's one-way link to the Solco Git app: it finds the
// app on this machine and asks it to open a repository, in a view, on a file.
//
// [INPUT]: the file system (the app bundle's usual places), the SOLCO_GIT_APP override, and macOS `open`.
// [OUTPUT]: URL (the solcogit://open link), Locator (finds the bundle, cached), Link (Available + Open), View, the two refusals.
// [POS]: 2026-11-07 decision "Solco Git and the right panel", items 2 and 5. The link goes Solco → Solco Git only; nothing here imports Solco Git, and nothing in the link is logged.
// [PROTOCOL]: update this header on change, then check CLAUDE.md.
package solcogit

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// Scheme is the URL scheme Solco Git registers (its mygo.config.ts).
	Scheme = "solcogit"
	// BundleName is the app's bundle directory name.
	BundleName = "Solco Git.app"
	// EnvApp names one bundle explicitly — a development build, a sandbox's
	// copy — and wins over the standard places.
	EnvApp = "SOLCO_GIT_APP"
	// MaxLinkLen is the longest link Solco Git accepts; a longer one is not made.
	MaxLinkLen = 8192

	// recheckEvery bounds how often Path touches the file system: the UI
	// asks every frame whether to draw the entry.
	recheckEvery = 5 * time.Second
	// openTimeout bounds the `open` call.
	openTimeout = 5 * time.Second
)

// View is the Solco Git view a link opens.
type View string

const (
	Changes View = "changes"
	Commits View = "commits"
	Files   View = "files"
)

var (
	// ErrNotInstalled: Solco Git is not on this machine (or this is not macOS).
	ErrNotInstalled = errors.New("Solco Git is not installed")
	// ErrBadTarget: the repository, view or file cannot be put in a link.
	ErrBadTarget = errors.New("nothing to open in Solco Git")
)

// URL builds the link that opens repo (an absolute path; Solco Git resolves
// the repository root from it) in view, on file (relative to the repository,
// "" for none). ok is false when the target cannot be put in a link.
func URL(repo string, view View, file string) (link string, ok bool) {
	if !filepath.IsAbs(repo) || strings.ContainsRune(repo, 0) {
		return "", false
	}
	switch view {
	case Changes, Commits, Files:
	default:
		return "", false
	}
	query := url.Values{}
	query.Set("repo", repo)
	query.Set("view", string(view))
	if file != "" {
		clean := filepath.Clean(file)
		if filepath.IsAbs(file) || strings.ContainsRune(file, 0) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", false
		}
		query.Set("file", filepath.ToSlash(clean))
	}
	link = Scheme + "://open?" + query.Encode()
	if len(link) > MaxLinkLen {
		return "", false
	}
	return link, true
}

// Locator finds the Solco Git bundle: the SOLCO_GIT_APP override, then
// /Applications, then ~/Applications. The answer is cached for a few seconds.
type Locator struct {
	mu        sync.Mutex
	checkedAt time.Time
	found     string

	// Seams for tests.
	goos   string
	now    func() time.Time
	stat   func(string) (os.FileInfo, error)
	getenv func(string) string
	home   func() (string, error)
}

// NewLocator is the production locator.
func NewLocator() *Locator {
	return &Locator{goos: runtime.GOOS, now: time.Now, stat: os.Stat, getenv: os.Getenv, home: os.UserHomeDir}
}

// Path is the bundle's path, or "" when Solco Git is not found. Only macOS has
// the app.
func (l *Locator) Path() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.goos != "darwin" {
		return ""
	}
	if now := l.now(); l.checkedAt.IsZero() || now.Sub(l.checkedAt) >= recheckEvery {
		l.checkedAt, l.found = now, l.search()
	}
	return l.found
}

func (l *Locator) search() string {
	candidates := []string{}
	if explicit := l.getenv(EnvApp); explicit != "" {
		candidates = append(candidates, explicit)
	}
	candidates = append(candidates, filepath.Join("/Applications", BundleName))
	if home, err := l.home(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", BundleName))
	}
	for _, path := range candidates {
		if info, err := l.stat(path); err == nil && info.IsDir() && strings.HasSuffix(path, ".app") {
			return path
		}
	}
	return ""
}

// Link is what the UI holds: whether Solco Git is there, and the way to ask it
// to open something.
type Link struct {
	locator *Locator
	run     func(appPath, link string) error
}

// NewLink is the production link.
func NewLink() *Link { return &Link{locator: NewLocator(), run: openWith} }

// Available reports whether Solco Git is on this machine; the entries stay
// hidden until it is.
func (k *Link) Available() bool { return k.locator.Path() != "" }

// Open asks Solco Git to open repo in view on file (see URL). The call
// returns once macOS has handed the link over, not when the window is up.
func (k *Link) Open(repo string, view View, file string) error {
	app := k.locator.Path()
	if app == "" {
		return ErrNotInstalled
	}
	link, ok := URL(repo, view, file)
	if !ok {
		return ErrBadTarget
	}
	return k.run(app, link)
}

// openWith is `open -a <bundle> <link>`: argv only (no shell), the link goes
// to exactly that bundle, so a second copy that also registered the scheme is
// not picked by accident.
func openWith(appPath, link string) error {
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "open", "-a", appPath, link).Run()
}
