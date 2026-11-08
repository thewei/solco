package personalagent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// assistantIDRe is an Assistant id as the core makes them; anything else never
// reaches a glob pattern.
var assistantIDRe = regexp.MustCompile(`^a_[0-9a-f]{1,32}$`)

// LastCoordinatorSession is the newest OMP transcript the Assistant's
// Coordinator has written — the conversation a restarted Coordinator picks up
// again (2026-11-07: one Coordinator, one conversation, however many times its
// Agent process is stopped and started). A Coordinator always runs in its own
// folder (`…/personal/coordinators/<assistantId>`) and OMP files a session under
// a folder named after the working directory, so the Assistant's id at the end
// of that folder's name finds it without relying on what Herdr reported. ""
// when there is none: the Coordinator then starts a conversation of its own.
// Read-only; symlinks and empty files are never followed or offered.
func LastCoordinatorSession(home, assistantID string) string {
	if home == "" || !assistantIDRe.MatchString(assistantID) {
		return ""
	}
	root := filepath.Join(home, ".omp", "agent", "sessions")
	dirs, err := filepath.Glob(filepath.Join(root, "*-coordinators-"+assistantID))
	if err != nil {
		return ""
	}
	best, bestAt := "", int64(0)
	for _, dir := range dirs {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".jsonl") || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				continue
			}
			if at := info.ModTime().UnixNano(); at > bestAt {
				best, bestAt = filepath.Join(dir, entry.Name()), at
			}
		}
	}
	return best
}

// resumeSession is the transcript a Coordinator launch continues: the
// dispatcher's own hook when set (tests), otherwise the user's real folder.
func (d *Dispatcher) resumeSession(assistantID string) string {
	if d.ResumeSession != nil {
		return d.ResumeSession(assistantID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return LastCoordinatorSession(home, assistantID)
}
