package macos

import (
	"os"
	"path/filepath"
	"strings"
)

// HelperName is the helper executable's file name.
const HelperName = "solco-remote-helper"

// EnvHelper overrides the helper path (development and tests).
const EnvHelper = "SOLCO_REMOTE_HELPER"

// Located is where the helper was found and how much to trust it.
type Located struct {
	Path string
	// Bundled is true for the copy inside the app bundle (Contents/Helpers); only that copy is
	// signature-checked at launch — an explicit override or a sibling dev build is the developer's call.
	Bundled bool
}

// Locate finds the helper: the SOLCO_REMOTE_HELPER override, then Contents/Helpers next to the
// running app's Contents/MacOS, then a sibling checkout's SwiftPM build (development).
func Locate(exe, cwd string, getenv func(string) string) (Located, bool) {
	if p := getenv(EnvHelper); p != "" {
		if isExecutable(p) {
			return Located{Path: p}, true
		}
		return Located{}, false
	}
	if exe != "" {
		dir := filepath.Dir(exe)
		if strings.HasSuffix(dir, filepath.Join("Contents", "MacOS")) {
			p := filepath.Join(filepath.Dir(dir), "Helpers", HelperName)
			if isExecutable(p) {
				return Located{Path: p, Bundled: true}, true
			}
		}
	}
	// Development: <anything>/solco-remote-helper/.build/{release,debug}/ beside the repo or its
	// worktree parent, searched up to four levels from the working directory.
	for dir, i := cwd, 0; dir != "" && dir != "/" && i < 4; dir, i = filepath.Dir(dir), i+1 {
		for _, cfg := range []string{"release", "debug"} {
			p := filepath.Join(filepath.Dir(dir), "solco-remote-helper", ".build", cfg, HelperName)
			if isExecutable(p) {
				return Located{Path: p}, true
			}
		}
	}
	return Located{}, false
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}
