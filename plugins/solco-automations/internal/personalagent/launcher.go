package personalagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
	"github.com/thewei/solco/plugins/solco-automations/internal/host"
)

// HostLauncher drives the real Herdr through the same provision/work
// choreography the cron runner uses. It never uses the hc backend: Assistants
// run only in the Herdr session the user sees.
type HostLauncher struct {
	Host host.Host
	// Session names the Herdr session ("" = the environment's).
	Session string
	// WorkDirRoot holds the controlled folders of repo-less personal tasks.
	WorkDirRoot string
	client      herdr.Client
}

// NewHostLauncher returns the production Launcher for a Herdr session.
func NewHostLauncher(session, workDirRoot string) *HostLauncher {
	return &HostLauncher{Host: host.New(), Session: session, WorkDirRoot: workDirRoot, client: herdr.Client{Session: session}}
}

func (l *HostLauncher) automation(spec LaunchSpec) config.Automation {
	if spec.Auto != nil {
		// A legacy rule: its own definition, named so the run's workspace label
		// carries the run id (Find) and its branch keeps the rule's prefix
		// (`auto/<rule>-`, which the cleanup TTL matches).
		a := *spec.Auto
		a.Name = spec.Auto.Name + " " + spec.RunID
		if a.Session == "" {
			a.Session = l.Session
		}
		return a
	}
	repo := spec.RepoPath
	if spec.WorkDir != "" {
		repo = spec.WorkDir
	}
	if repo == "" {
		repo = filepath.Join(l.WorkDirRoot, "personal")
		if spec.Kind == KindCoordinator && spec.AssistantID != "" {
			// Each Coordinator has a folder of its own.
			repo = filepath.Join(repo, "coordinators", spec.AssistantID)
		}
		_ = os.MkdirAll(repo, 0o700)
	}
	mode := config.WorkspaceRoot
	if spec.Kind == KindCoding && spec.WorkDir == "" {
		mode = config.WorkspaceWorktree
	}
	minutes := int(spec.Timeout / time.Minute)
	if minutes < 1 {
		minutes = 45
	}
	return config.Automation{
		Name: spec.RunID + " " + spec.titleHint(), Repo: repo, Workspace: mode, Agent: spec.Agent, Model: spec.Model,
		Prompt: spec.Prompt, TimeoutMinutes: minutes, Session: l.Session,
	}
}

func (s LaunchSpec) titleHint() string {
	// "assistant: <title> (<run id>)" → <title>
	t := strings.TrimPrefix(s.Label, "assistant: ")
	if i := strings.LastIndex(t, " ("); i > 0 {
		t = t[:i]
	}
	return t
}

// Provision opens the workspace for a run.
func (l *HostLauncher) Provision(spec LaunchSpec) (Session, error) {
	hs, err := l.Host.Provision(l.automation(spec))
	return Session{HerdrSession: hs.HerdrSession, WorkspaceID: hs.WorkspaceID, PaneID: hs.PaneID, Branch: hs.Branch}, err
}

// Work starts the Agent, hands it the prompt and waits.
func (l *HostLauncher) Work(ctx context.Context, sess Session, spec LaunchSpec) error {
	return l.mapErr(l.Host.Do(host.Session{WorkspaceID: sess.WorkspaceID, PaneID: sess.PaneID, HerdrSession: sess.HerdrSession, Branch: sess.Branch}, l.automation(spec), spec.Timeout))
}

// Await waits for an Agent that is already working: Herdr's own wait on the pane.
func (l *HostLauncher) Await(ctx context.Context, sess Session, spec LaunchSpec) error {
	deadline := time.Now().Add(spec.Timeout)
	c := herdr.Client{Session: sess.HerdrSession}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.AgentWait(sess.PaneID, 30*time.Second)
		if err == nil {
			return nil
		}
		if herdr.HasCode(err, herdr.CodeWorkspaceGone) {
			return ErrWorkCancelled
		}
		if herdr.HasCode(err, herdr.CodeAgentGone) {
			// herdr uses one code for "workspace closed" and "no agent here".
			if l.workspaceOpen(sess.WorkspaceID) {
				return ErrAgentMissing
			}
			return ErrWorkCancelled
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the agent was still working after %s", spec.Timeout)
		}
	}
}

func (l *HostLauncher) workspaceOpen(id string) bool {
	list, err := herdr.Client{Session: l.Session}.WorkspaceList()
	if err != nil {
		return false
	}
	for _, w := range list {
		if w.WorkspaceID == id {
			return true
		}
	}
	return false
}

func (l *HostLauncher) mapErr(err error) error {
	var blocked *host.BlockedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, host.ErrCancelled):
		return ErrWorkCancelled
	case errors.As(err, &blocked):
		return &BlockedError{Reason: blocked.What}
	}
	return err
}

// Find looks for the workspace Herdr holds for a run marker (the run id is in
// the workspace label).
func (l *HostLauncher) Find(marker string) (Session, bool, error) {
	if marker == "" {
		return Session{}, false, nil
	}
	list, err := l.client.WorkspaceList()
	if err != nil {
		return Session{}, false, err
	}
	for _, w := range list {
		if !strings.Contains(w.Label, marker) {
			continue
		}
		panes, err := l.client.PaneIDs(w.WorkspaceID)
		if err != nil {
			return Session{}, false, err
		}
		if len(panes) == 0 {
			return Session{}, false, nil
		}
		return Session{HerdrSession: l.Session, WorkspaceID: w.WorkspaceID, PaneID: panes[0]}, true, nil
	}
	return Session{}, false, nil
}
