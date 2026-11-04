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
		if spec.Kind == KindGroupAgent && spec.GroupID != "" && spec.AssistantID != "" {
			// Each Group Agent has a folder of its own, apart from the Coordinator's.
			repo = filepath.Join(repo, "groups", spec.GroupID, spec.AssistantID)
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
		Prompt: spec.Prompt, TimeoutMinutes: minutes, Session: l.Session, AgentArgs: spec.AgentArgs,
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

// HasAgent only confirms a pane when Herdr registers the expected provider.
// A workspace marker alone cannot prove the launch or its first prompt
// succeeded. In particular, an empty pane must remain uncertain.
func (l *HostLauncher) HasAgent(sess Session, expected string) (bool, error) {
	if sess.PaneID == "" || expected == "" {
		return false, nil
	}
	client := herdr.Client{Session: sess.HerdrSession}
	kind, status, err := client.AgentIdentity(sess.PaneID)
	if err != nil {
		return false, err
	}
	// Idle/blocked is not proof that the original prompt was delivered.
	// Only an actually working Agent is safe to reattach automatically.
	if kind != expected || status != "working" {
		return false, nil
	}
	// Do not interpret a startup trust or permission dialog as a running worker.
	if screen, err := client.PaneRead(sess.PaneID, 60); err == nil {
		if _, blocked := host.StartupDialog(screen); blocked {
			return false, nil
		}
	}
	return true, nil
}

// HerdrPaneGone verifies a previously owned pane is no longer present. An
// unavailable Herdr server is NOT evidence of closure. Do not release or
// close a pane automatically when permanently deleting an Assistant.
func HerdrPaneGone(session, workspace, pane string) (bool, error) {
	if workspace == "" || pane == "" {
		return false, fmt.Errorf("missing recorded Herdr identity")
	}
	client := herdr.Client{Session: session}
	workspaces, err := client.WorkspaceList()
	if err != nil {
		return false, err
	}
	for _, w := range workspaces {
		if w.WorkspaceID != workspace {
			continue
		}
		ids, err := client.PaneIDs(workspace)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			if id == pane {
				return false, nil
			}
		}
		return true, nil
	}
	return true, nil
}
