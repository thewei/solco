package personalagent

import (
	"os"
	"path/filepath"

	"github.com/thewei/solco/plugins/solco-automations/internal/hostpath"
)

// DBPath is the one database file. SOLCO_ASSISTANTS_DB overrides it (tests and
// isolated acceptance runs); otherwise it sits in the plugin's state directory.
func DBPath() string {
	if p := os.Getenv("SOLCO_ASSISTANTS_DB"); p != "" {
		return p
	}
	return filepath.Join(hostpath.StateDir(), "assistants.sqlite")
}

// WorkDirRoot holds the controlled folders of repo-less personal tasks.
func WorkDirRoot() string {
	if p := os.Getenv("SOLCO_ASSISTANTS_WORKDIR"); p != "" {
		return p
	}
	return filepath.Join(hostpath.StateDir(), "assistants-work")
}
