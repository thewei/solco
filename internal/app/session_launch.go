package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 LaunchService（RuntimeFor / Registry / Manager）、agent.RunLaunchTransaction、herdr.Manager.AgentKinds
 * [OUTPUT]: 对外提供 SessionStarter（桌面与手机共用的「启动新会话」服务层：StartSession / Kinds）、SessionSpec、SessionResult、SessionError、TerminalKind；LaunchService.Sessions()
 * [POS]: 界面无关的共享层——Agent 走 agent.RunLaunchTransaction，Terminal 只建 Tab/Workspace；同 requestId 幂等（与历史继续共用一个 LaunchRegistry）。手机 Host（remote_launch.go）与桌面任何将来的入口都只经这里
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// TerminalKind is the session kind that opens a plain shell instead of an agent.
const TerminalKind = "terminal"

// kindsTTL bounds how long the parsed `herdr agent start --help` list lives,
// so a Herdr upgrade shows up without restarting Solco.
const kindsTTL = 10 * time.Minute

// fallbackKinds are offered when the live CLI list cannot be read.
var fallbackKinds = []history.AgentID{history.AgentClaudeCode, history.AgentCodex, history.AgentPi, history.AgentOMP}

// SessionSpec is one new-session request: a Workspace (new Tab in it) or a
// directory (its Workspace found or created). Agent == "" opens a Terminal.
// An empty Prompt starts the agent only; for a Terminal it is the first command line, typed into the
// new shell with Enter.
type SessionSpec struct {
	RequestID   string
	WorkspaceID string
	CWD         string
	Agent       history.AgentID
	Prompt      string
}

// SessionResult is the new session's pane. PromptPending: the Agent exists but is blocked on a human
// (Claude Code's first-run "trust this folder?") before it could take the initial prompt, so that
// prompt was NOT delivered — the caller shows the Agent's question and keeps the text for the user.
type SessionResult struct {
	PaneID        string
	PromptPending bool
}

// SessionKind is one launchable kind (TerminalKind or an agent id).
type SessionKind struct {
	ID    string
	Label string
}

type SessionErrorCode string

const (
	SessionPathUnavailable SessionErrorCode = "path_unavailable"
	SessionConflict        SessionErrorCode = "launch_conflict"
	// SessionUncertain: the session may exist (the Agent was created but could not be verified or
	// prompted). Look at the tree before retrying.
	SessionUncertain SessionErrorCode = "launch_uncertain"
	SessionFailed    SessionErrorCode = "launch_failed"
)

// SessionError is a typed refusal; Detail never carries the prompt.
type SessionError struct {
	Code   SessionErrorCode
	Detail string
}

func (e *SessionError) Error() string { return string(e.Code) + ": " + e.Detail }

type SessionStarter struct {
	service *LaunchService
	// seams for tests
	runtime   func(instance string) agent.LaunchRuntime
	listKinds func(context.Context) ([]string, error)
	run       func(context.Context, agent.LaunchRuntime, agent.LaunchIntent) (agent.LaunchOutcome, *agent.LaunchFailure)
	// sendInput types one line (text + Enter) into a pane; nil = a Terminal first command is unavailable.
	sendInput func(ctx context.Context, instance, paneID, text string) error
	now       func() time.Time

	mu      sync.Mutex
	kinds   []SessionKind
	kindsAt time.Time
}

// Sessions is the one shared new-session service of this LaunchService.
func (s *LaunchService) Sessions() *SessionStarter {
	s.sessionsOnce.Do(func() {
		s.sessions = &SessionStarter{
			service:   s,
			runtime:   s.RuntimeFor,
			listKinds: s.manager.AgentKinds,
			run: func(ctx context.Context, rt agent.LaunchRuntime, intent agent.LaunchIntent) (agent.LaunchOutcome, *agent.LaunchFailure) {
				return agent.RunLaunchTransaction(ctx, rt, Preparer{}, intent, agent.DefaultTimings())
			},
			sendInput: func(ctx context.Context, instance, paneID, text string) error {
				return s.manager.SendPaneInput(ctx, instance, paneID, text, []string{"enter"})
			},
			now: time.Now,
		}
	})
	return s.sessions
}

// Kinds is Terminal plus every Solco-known agent that Herdr can start.
func (l *SessionStarter) Kinds() []SessionKind {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kinds != nil && l.now().Sub(l.kindsAt) < kindsTTL {
		return l.kinds
	}
	supported := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if names, err := l.listKinds(ctx); err == nil {
		for _, name := range names {
			supported[name] = true
		}
	}
	kinds := []SessionKind{{ID: TerminalKind, Label: "Terminal"}}
	add := func(id history.AgentID) {
		kinds = append(kinds, SessionKind{ID: string(id), Label: id.DisplayName()})
	}
	if len(supported) == 0 {
		for _, id := range fallbackKinds {
			add(id)
		}
	} else {
		for _, id := range history.AllAgents {
			if supported[agent.HerdrAgentKind(id)] {
				add(id)
			}
		}
	}
	l.kinds, l.kindsAt = kinds, l.now()
	return kinds
}

// StartSession runs one launch and returns the new pane. The same RequestID
// replays the first answer (success or refusal) and never launches twice; the
// same id with a different body is a SessionConflict.
func (l *SessionStarter) StartSession(ctx context.Context, instance string, spec SessionSpec) (SessionResult, error) {
	rt := l.runtime(instance)
	registry := l.service.Registry()
	// Herdr matches a Workspace by its resolved directory (macOS /tmp is /private/tmp); a path typed
	// on the phone must find the Workspace the desktop already shows rather than create a twin.
	if resolved, err := filepath.EvalSymlinks(spec.CWD); err == nil {
		spec.CWD = resolved
	}

	if spec.Agent == "" {
		// Same durable idempotency as an agent launch (a retry after a lost answer must not open a
		// second tab). Validate wants a known provider, so a placeholder carries the request; the
		// "terminal:" project prefix keeps it distinct from any agent request.
		request := agent.StartAgentRequest{RequestID: spec.RequestID, Instance: instance, ProjectID: "terminal:" + sessionProject(spec), Provider: history.AgentClaudeCode, CWD: spec.CWD, Prompt: spec.Prompt}
		rec, err := registry.Submit(ctx, request, func(ctx context.Context, _ agent.StartAgentRequest) (agent.LaunchOutcomeRecord, error) {
			pane, err := l.openTerminal(ctx, rt, spec)
			if err != nil {
				return agent.LaunchOutcomeRecord{AgentKey: pane}, err
			}
			// The pane exists either way; a command that could not be typed is reported, not rolled back.
			pending := spec.Prompt != "" && l.typeFirstCommand(ctx, instance, pane, spec.Prompt) != nil
			return agent.LaunchOutcomeRecord{Launched: true, AgentKey: pane, PromptPending: pending}, nil
		})
		return SessionResult{PaneID: rec.AgentKey, PromptPending: rec.PromptPending}, sessionError(err)
	}

	request := agent.StartAgentRequest{RequestID: spec.RequestID, Instance: instance, ProjectID: sessionProject(spec), Provider: spec.Agent, CWD: spec.CWD, Prompt: spec.Prompt}
	rec, err := registry.Submit(ctx, request, func(ctx context.Context, r agent.StartAgentRequest) (agent.LaunchOutcomeRecord, error) {
		outcome, failure := l.run(ctx, rt, agent.LaunchIntent{
			OperationID:       r.RequestID,
			WorkspaceID:       spec.WorkspaceID,
			ProjectPath:       r.CWD,
			Mode:              agent.LaunchModeBuild,
			Permission:        agent.PermissionAskApproval,
			Agent:             r.Provider,
			Prompt:            r.Prompt,
			SkipInitialPrompt: r.Prompt == "",
		})
		if failure != nil {
			if pending, ok := l.blockedOnHuman(ctx, rt, failure, r.Prompt != ""); ok {
				return pending, nil
			}
			return agent.LaunchOutcomeRecord{}, failure
		}
		return agent.LaunchOutcomeRecord{Launched: true, AgentKey: outcome.PaneID}, nil
	})
	return SessionResult{PaneID: rec.AgentKey, PromptPending: rec.PromptPending}, sessionError(err)
}

// blockedOnHuman recognises the one post-commit failure that is not uncertain: the Agent was created
// and Herdr reports it blocked (never ready, so no prompt went out). It is a started session that
// needs its user, recorded as such so a replay answers the same way.
func (l *SessionStarter) blockedOnHuman(ctx context.Context, rt agent.LaunchRuntime, failure *agent.LaunchFailure, hadPrompt bool) (agent.LaunchOutcomeRecord, bool) {
	if failure.Kind != "agent-created" || failure.Phase != agent.PhaseNotReady || failure.PaneID == "" {
		return agent.LaunchOutcomeRecord{}, false
	}
	started, err := rt.AgentByPane(ctx, failure.PaneID)
	if err != nil || started == nil || started.Status != "blocked" {
		return agent.LaunchOutcomeRecord{}, false
	}
	return agent.LaunchOutcomeRecord{Launched: true, AgentKey: failure.PaneID, PromptPending: hadPrompt}, true
}

// typeFirstCommand types the Terminal's first command line into its shell. The PTY queues typeahead until
// the shell reads, so no readiness wait is needed; a pane that is not yet attachable gets a few quick retries.
func (l *SessionStarter) typeFirstCommand(ctx context.Context, instance, paneID, text string) error {
	if l.sendInput == nil {
		return errors.New("pane input unavailable")
	}
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = l.sendInput(ctx, instance, paneID, text); err == nil {
			return nil
		}
		sleepContext(ctx, 300*time.Millisecond)
	}
	return err
}

func sessionProject(spec SessionSpec) string {
	if spec.WorkspaceID != "" {
		return spec.WorkspaceID
	}
	return "path:" + spec.CWD
}

// openTerminal creates a Tab (existing workspace, or the one matching the
// path, or a new workspace) and returns its pane, never taking focus.
func (l *SessionStarter) openTerminal(ctx context.Context, rt agent.LaunchRuntime, spec SessionSpec) (string, error) {
	cwd, err := Preparer{}.Prepare(ctx, spec.CWD, "")
	if err != nil {
		return "", &SessionError{Code: SessionPathUnavailable, Detail: err.Error()}
	}
	workspace := spec.WorkspaceID
	if workspace == "" {
		if workspace, err = rt.WorkspaceIDForPath(ctx, cwd); err != nil {
			return "", err
		}
	}
	if workspace == "" {
		created, err := rt.CreateWorkspaceWithoutFocus(ctx, cwd)
		if err != nil {
			return "", err
		}
		return created.Tab.PaneID, nil
	}
	tab, err := rt.CreateTabWithoutFocus(ctx, workspace, cwd)
	if err != nil {
		return "", err
	}
	return tab.PaneID, nil
}

// sessionError maps the transaction's taxonomy onto SessionError.
func sessionError(err error) error {
	var typed *SessionError
	var conflict *agent.ConflictingRequestError
	var failure *agent.LaunchFailure
	switch {
	case err == nil, errors.As(err, &typed):
		return err
	case errors.As(err, &conflict):
		return &SessionError{Code: SessionConflict, Detail: "requestId reused with a different body"}
	case errors.As(err, &failure):
		switch {
		case failure.Kind == "project-unavailable" || failure.Kind == "branch-preparation":
			return &SessionError{Code: SessionPathUnavailable, Detail: failure.Detail}
		case failure.Committed || failure.Uncertain:
			return &SessionError{Code: SessionUncertain, Detail: failure.Kind}
		}
		return &SessionError{Code: SessionFailed, Detail: failure.Kind + ": " + failure.Detail}
	}
	return &SessionError{Code: SessionFailed, Detail: err.Error()}
}
