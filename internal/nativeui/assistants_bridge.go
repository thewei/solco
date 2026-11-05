package nativeui

/**
 * [INPUT]: 依赖 assistants_state 的门/客户端（assistantsClientFor）、internal/personalagent 的 CLI 客户端与类型、Shell.openPane、herdr.ParseRef
 * [OUTPUT]: AssistantsBridge（`mygo.BindAs("assistants", …)`）——Snapshot 轻读视图（概览 + 选中 Assistant 的 Approval/Inbox/Coordinator + 可选 agent 列表）、Drawer（Goal/Task/Run/Memory，抽屉开着才读）、EnsureChat（首条消息拉起 Coordinator，requestId 幂等）、BindCoordinator（不移动 Mac 选中项的静默绑定，无转录时落邀请态），命令（建/改名/启停 Assistant、建 Goal 与 PR Watch、Goal 动作、建/确认/重试 Task、批准/拒绝 Approval、打开 Run 的窗格）与 Select（页面导航）
 * [POS]: Assistants WebView 页（2026-11-03 决策）的唯一数据与命令口：全部读写走插件的 `assistant …` CLI（SQLite 单写者仍归插件），Solco 不缓存业务数据、不重算状态；IO 在桥 goroutine 上，窗口线程只被借来读 Herdr 投影与写选中项；错误原样带插件稳定码；日志与快照都不记提示词/简报/记忆正文
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/personalagent"
)

// assistantsCallTimeout bounds one bridge call (a handful of CLI reads).
const assistantsCallTimeout = 20 * time.Second

// AssistantsBridge is the page's bound service.
type AssistantsBridge struct{ shell *Shell }

// NewAssistantsBridge binds the page to the shell.
func NewAssistantsBridge(shell *Shell) *AssistantsBridge { return &AssistantsBridge{shell: shell} }

// AssistantsView is everything the page draws, one value per read.
type AssistantsView struct {
	Theme     string `json:"theme"`
	Available bool   `json:"available"`
	// Assistants are the overview rows (counters per Assistant).
	Assistants []personalagent.OverviewRow `json:"assistants"`
	NeedsYou   int                         `json:"needsYou"`
	// Selected is the Assistant the page is on ("" = none yet).
	Selected string            `json:"selected"`
	Detail   *AssistantsDetail `json:"detail,omitempty"`
	// Groups are the Group rows; SelectedGroup, when set, is the Group the
	// page is on instead of an Assistant's chat (no Detail is read then).
	Groups        []personalagent.OverviewGroup `json:"groups"`
	SelectedGroup string                        `json:"selectedGroup"`
	// Agents are the Herdr agents a Coordinator may run on (Chat-capable and
	// switched on in Settings → Agents, last used first); DefaultAgent is the
	// first of them.
	Agents       []AssistantsAgent `json:"agents"`
	DefaultAgent string            `json:"defaultAgent"`
	// AgentsReady is false while the launchable-agent catalog is still being
	// read (the first seconds of a window): an empty list then is "not yet",
	// not "none turned on".
	AgentsReady bool `json:"agentsReady"`
}

// AssistantsAgent is one choice of the Coordinator's agent chip: ID is the
// Herdr agent kind the plugin passes to `herdr agent start --kind`.
type AssistantsAgent struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AssistantsDetail is the selected Assistant's light page: what the
// conversation header, the Needs-You strip and the Chat need. The long lists
// (Goals, Tasks and their Runs) are the More drawer's, read by Drawer
// only while it is open.
type AssistantsDetail struct {
	Assistant personalagent.Assistant  `json:"assistant"`
	Approvals []personalagent.Approval `json:"approvals"`
	// Coordinator is the Assistant's own Coordinator Agent; CoordinatorOpen
	// says its pane is in this window's Herdr tree (so its Chat can bind).
	Coordinator     personalagent.Coordinator `json:"coordinator"`
	CoordinatorOpen bool                      `json:"coordinatorOpen"`
	// Report is the delivery state of an opt-in progress narration request.
	// Its words remain Solco metadata; OMP's answer stays in the real Chat.
	Report *personalagent.CoordinatorReport `json:"report,omitempty"`
	// Inbox is the plugin's one snapshot of what needs a person (approvals,
	// reviews, failed starts, stalls); the page draws it, never recomputes it.
	Inbox personalagent.Inbox `json:"inbox"`
	// ProfileAgent is the Agent the Assistant's own profile starts a Coordinator
	// on ("" = none chosen); read only while no Coordinator runs, for the chip.
	ProfileAgent string `json:"profileAgent"`
	// Facts are the newest ledger entries — sentences the core wrote about
	// committed state (accepted, delegated, verified, blocked, done…). The page
	// draws them as the system's own line, never as the Agent's words.
	Facts []personalagent.Fact `json:"facts"`
}

// AssistantsDrawer is the More drawer's content for one Assistant.
type AssistantsDrawer struct {
	Goals []personalagent.Goal `json:"goals"`
	// Plans are the plans of the Goals that have one, with each milestone's
	// state derived by the core from its task; Facts the Assistant's ledger.
	Plans []personalagent.GoalPlan `json:"plans"`
	Facts []personalagent.Fact     `json:"facts"`
	// Routines are the Assistant's scheduled work: drafts waiting for a person's
	// approval and the ones already enabled, with their next and last run.
	Routines []personalagent.Routine `json:"routines"`
	Tasks    []AssistantsTask        `json:"tasks"`
}

// CoordinatorChat is the answer to "bind the embedded Chat to this Assistant's
// Coordinator": Bound says the Chat is on it now; otherwise State/Reason say
// why not (starting, waiting for its session, not in this window, …).
type CoordinatorChat struct {
	AssistantID string `json:"assistantId"`
	State       string `json:"state"`
	PaneID      string `json:"paneId"`
	Bound       bool   `json:"bound"`
	// Invitation says the Chat is on the Coordinator's pane but the agent has
	// no transcript yet (a fresh Coordinator writes none until its first
	// prompt): the page shows the new-session Chat and keeps asking until the
	// conversation binds.
	Invitation bool   `json:"invitation"`
	Reason     string `json:"reason"`
}

// AssistantsTask is a Task with the Runs the page lists under it.
type AssistantsTask struct {
	personalagent.Task
	Runs []AssistantsRun `json:"runs"`
}

// AssistantsRun is one Run plus whether its pane can be opened from here.
type AssistantsRun struct {
	personalagent.Run
	CanOpen bool `json:"canOpen"`
}

// AssistantsGoalInput creates a Goal; Watch makes it a PR Watch.
type AssistantsGoalInput struct {
	AssistantID string `json:"assistantId"`
	Title       string `json:"title"`
	Objective   string `json:"objective"`
	// Watch, when set, adds one pr_watch commitment.
	Watch *AssistantsWatch `json:"watch,omitempty"`
}

// AssistantsWatch is the PR Watch spec the page collects.
type AssistantsWatch struct {
	Repo            string `json:"repo"`
	Number          int    `json:"number"`
	IntervalSeconds int    `json:"intervalSeconds"`
	Agent           string `json:"agent"`
	Model           string `json:"model"`
	RepoPath        string `json:"repoPath"`
}

// client returns the plugin client or the refusal the page shows.
func (b *AssistantsBridge) client() (*personalagent.Client, error) {
	var c *personalagent.Client
	b.shell.callOnWindow(func() {
		if b.shell.assistantsAvailable() {
			c = b.shell.assistantsClientFor()
		}
	})
	if c == nil {
		return nil, errors.New("Assistants are not available")
	}
	return c, nil
}

func assistantsCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), assistantsCallTimeout)
}

// Snapshot reads the overview and, for the Assistant the shell has selected
// (else the first), its page.
func (b *AssistantsBridge) Snapshot(ctx context.Context) (AssistantsView, error) {
	s := b.shell
	view := AssistantsView{Assistants: []personalagent.OverviewRow{}, Groups: []personalagent.OverviewGroup{}}
	var panes map[string]bool
	var client *personalagent.Client
	var session, selected, group string
	view.Agents = []AssistantsAgent{}
	s.callOnWindow(func() {
		s.ensureLaunchKinds()
		view.Agents, view.DefaultAgent = s.assistantAgents()
		view.AgentsReady = s.launchKinds != nil
		view.Theme = s.chatBridgeTheme()
		if s.assistantsAvailable() {
			client = s.assistantsClientFor()
			session = herdr.ParseRef(s.activeInstance).Session
			selected = s.assistants.selected
			group = s.assistants.selectedGroup
			panes = map[string]bool{}
			for _, p := range s.projection.Panes {
				panes[p.ID] = true
			}
		}
	})
	if client == nil {
		return view, nil
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	overview, err := client.Overview(ctx)
	if err != nil {
		return view, err
	}
	s.applyAssistantsOverviewFromBridge(overview)
	view.Available = true
	view.Assistants, view.NeedsYou = overview.Assistants, overview.NeedsYou
	if view.Assistants == nil {
		view.Assistants = []personalagent.OverviewRow{}
	}
	if overview.Groups != nil {
		view.Groups = overview.Groups
	}
	if group != "" {
		for _, g := range view.Groups {
			if g.ID == group {
				view.SelectedGroup = group
				return view, nil
			}
		}
	}
	if selected == "" && len(overview.Assistants) > 0 {
		selected = overview.Assistants[0].ID
	}
	known := false
	for _, row := range overview.Assistants {
		if row.ID == selected {
			known = true
		}
	}
	if !known {
		return view, nil
	}
	view.Selected = selected
	detail, err := assistantsDetail(ctx, client, selected, session, panes)
	if err != nil {
		return view, err
	}
	view.Detail = detail
	return view, nil
}

// assistantsDetail reads one Assistant's light page.
func assistantsDetail(ctx context.Context, client *personalagent.Client, id, session string, panes map[string]bool) (*AssistantsDetail, error) {
	list, err := client.Assistants(ctx)
	if err != nil {
		return nil, err
	}
	d := &AssistantsDetail{Approvals: []personalagent.Approval{}, Facts: []personalagent.Fact{}}
	found := false
	for _, a := range list {
		if a.ID == id {
			d.Assistant, found = a, true
		}
	}
	if !found {
		return nil, nil
	}
	if approvals, err := client.Approvals(ctx, id); err != nil {
		return nil, err
	} else if approvals != nil {
		d.Approvals = approvals
	}
	if facts, err := client.Facts(ctx, id, 6); err == nil && facts != nil {
		d.Facts = facts
	}
	d.Inbox = personalagent.Inbox{Items: []personalagent.InboxItem{}}
	if in, err := client.Inbox(ctx, id); err == nil && in.Items != nil {
		d.Inbox = in
	}
	if co, err := client.Coordinator(ctx, id); err == nil {
		d.Coordinator = co
		d.CoordinatorOpen = co.PaneID != "" && coordinatorInSession(co, session) && panes[co.PaneID]
		switch co.State {
		case personalagent.CoordinatorNone, personalagent.CoordinatorStopped, personalagent.CoordinatorFailed:
			if rt, err := client.Runtime(ctx, id); err == nil {
				d.ProfileAgent = rt.Profile.Agent
			}
		}
	}
	// Older plugins do not have the report CLI: keep their Assistants page
	// available while the plugin upgrade finishes.
	if report, err := client.CoordinatorReport(ctx, id); err == nil {
		d.Report = &report
	}
	return d, nil
}

// Drawer reads the More drawer's lists for one Assistant: its Goals, Tasks
// with their Runs (a Run can be opened only while it ran in the window's Herdr
// session and its pane is still in the tree). The page
// asks only while the drawer is open, so the chat's own polling stays light.
func (b *AssistantsBridge) Drawer(ctx context.Context, id string) (AssistantsDrawer, error) {
	out := AssistantsDrawer{Goals: []personalagent.Goal{}, Plans: []personalagent.GoalPlan{}, Facts: []personalagent.Fact{}, Routines: []personalagent.Routine{}, Tasks: []AssistantsTask{}}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	var panes map[string]bool
	var session string
	b.shell.callOnWindow(func() {
		session = herdr.ParseRef(b.shell.activeInstance).Session
		panes = map[string]bool{}
		for _, p := range b.shell.projection.Panes {
			panes[p.ID] = true
		}
	})
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	if goals, err := c.Goals(ctx, id); err != nil {
		return out, err
	} else if goals != nil {
		out.Goals = goals
	}
	for _, g := range out.Goals {
		if g.State == "cancelled" {
			continue
		}
		// A Goal with no plan answers not_found: that is "no plan yet", not an error.
		if plan, err := c.GoalPlan(ctx, g.ID); err == nil {
			out.Plans = append(out.Plans, plan)
		}
	}
	if facts, err := c.Facts(ctx, id, 40); err == nil && facts != nil {
		out.Facts = facts
	}
	// Best effort, like plans: an older plugin has no routines and that is "none".
	if routines, err := c.Routines(ctx, id); err == nil && routines != nil {
		out.Routines = routines
	}
	tasks, err := c.Tasks(ctx, id)
	if err != nil {
		return out, err
	}
	for _, t := range tasks {
		item := AssistantsTask{Task: t, Runs: []AssistantsRun{}}
		if _, runs, err := c.TaskDetail(ctx, t.ID); err == nil {
			for _, r := range runs {
				same := r.HerdrSession == "" || herdr.ParseRef(r.HerdrSession).Session == session || r.HerdrSession == session
				item.Runs = append(item.Runs, AssistantsRun{Run: r, CanOpen: r.PaneID != "" && same && panes[r.PaneID]})
			}
		}
		out.Tasks = append(out.Tasks, item)
	}
	return out, nil
}

// chatCapableAgents are the Solco agent ids whose conversation the Chat page
// can read (the Chat composer decision's provider set).
var chatCapableAgents = map[history.AgentID]bool{
	history.AgentClaudeCode: true, history.AgentCodex: true, history.AgentPi: true,
	history.AgentOMP: true, history.AgentCursor: true, history.AgentAntigravity: true,
}

// assistantAgents lists the Herdr agents a Coordinator may run on: the
// launchable kinds that are Chat-capable (so the conversation can be shown),
// the last-used one first. The Settings → Agents toggles already filter
// launchKinds. Window lane.
func (s *Shell) assistantAgents() ([]AssistantsAgent, string) {
	out := []AssistantsAgent{}
	seen := map[string]bool{}
	add := func(kind app.SessionKind) {
		if kind.ID != string(history.AgentOMP) || seen[kind.ID] {
			return
		}
		seen[kind.ID] = true
		out = append(out, AssistantsAgent{ID: agent.HerdrAgentKind(history.AgentID(kind.ID)), Label: kind.Label})
	}
	for _, id := range s.recentLaunchKinds {
		for _, kind := range s.launchKinds {
			if kind.ID == id {
				add(kind)
			}
		}
	}
	for _, kind := range s.launchKinds {
		add(kind)
	}
	if len(out) == 0 {
		return out, ""
	}
	return out, out[0].ID
}

// coordinatorInSession says the Coordinator ran in the window's Herdr session
// (a Coordinator that ran elsewhere is never opened or bound here).
func coordinatorInSession(co personalagent.Coordinator, session string) bool {
	return co.HerdrSession == "" || herdr.ParseRef(co.HerdrSession).Session == session || co.HerdrSession == session
}

// ArchiveAssistant preserves every record but hides this Assistant from live surfaces.
func (b *AssistantsBridge) ArchiveAssistant(ctx context.Context, requestID, id string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.ArchiveAssistant(ctx, requestID, id, revision)
		if err == nil {
			b.shell.applyOnWindow(func() { b.shell.assistants.archiveLoaded = false })
		}
		return err
	})
}

// StartCoordinator asks the daemon to bring the Assistant's Coordinator up as
// a real Herdr Agent on the chosen agent kind (the plugin owns the lifecycle).
func (b *AssistantsBridge) StartCoordinator(ctx context.Context, requestID, id, agentKind string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		agentKind, err := b.startAgent(ctx, c, id, agentKind)
		if err != nil {
			return err
		}
		_, err = c.StartCoordinator(ctx, requestID, id, agentKind, "")
		return err
	})
}

// startAgent decides the Agent kind for a Coordinator start: an explicit choice
// (the chip) wins; otherwise the Assistant's own profile ("" lets the plugin
// apply it, with its model — still fenced by the Settings → Agents toggles);
// with no profile, the last-used launchable Agent.
func (b *AssistantsBridge) startAgent(_ context.Context, _ *personalagent.Client, _ string, agentKind string) (string, error) {
	if agentKind != "" {
		return agentKind, b.agentAllowed(agentKind)
	}
	// During the OMP rollout, saved profiles for other providers must not
	// launch that provider even if they predate the restriction.
	var def string
	b.shell.callOnWindow(func() { _, def = b.shell.assistantAgents() })
	if def == "" {
		return "", errors.New("invalid: omp is not available in Settings → Agents")
	}
	return def, nil
}

// agentAllowed fences the chip's choice: "" is the plugin default, anything
// else must be one of the agents Settings offers (a switched-off agent never
// starts a Coordinator).
func (b *AssistantsBridge) agentAllowed(agentKind string) error {
	if agentKind == "" {
		return nil
	}
	var list []AssistantsAgent
	b.shell.callOnWindow(func() { list, _ = b.shell.assistantAgents() })
	for _, a := range list {
		if a.ID == agentKind {
			return nil
		}
	}
	return errors.New("invalid: that agent is not available for a Coordinator")
}

// EnsureChat is the conversation's first-Send entry (2026-11-04): it starts the
// Assistant's Coordinator when there is none (never started, stopped or
// failed; starting/ready is left alone) and answers like BindCoordinator, so
// the page can hold the person's text until Bound. The requestId makes a
// double Send one start; a paused Assistant is refused by the plugin.
func (b *AssistantsBridge) EnsureChat(ctx context.Context, requestID, id, agentKind string) (CoordinatorChat, error) {
	out := CoordinatorChat{AssistantID: id, State: personalagent.CoordinatorNone}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	rctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	co, err := c.Coordinator(rctx, id)
	if err != nil {
		return out, err
	}
	switch co.State {
	case personalagent.CoordinatorNone, personalagent.CoordinatorStopped, personalagent.CoordinatorFailed:
		if agentKind, err = b.startAgent(rctx, c, id, agentKind); err != nil {
			return out, err
		}
		if co, err = c.StartCoordinator(rctx, requestID, id, agentKind, ""); err != nil {
			return out, err
		}
		b.shell.applyOnWindow(func() { b.shell.refreshAssistants(true) })
	}
	return b.bindState(id, co), nil
}

// StopCoordinator marks the Coordinator stopped.
func (b *AssistantsBridge) StopCoordinator(ctx context.Context, requestID, id string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.StopCoordinator(ctx, requestID, id)
		return err
	})
}

// ResolveCoordinatorReport is an explicit choice after the person checks
// whether the progress prompt reached their original OMP conversation.
// "retry" may duplicate a message, so the UI must confirm that risk.
func (b *AssistantsBridge) ResolveCoordinatorReport(ctx context.Context, requestID, id string, throughSeq int64, action string, confirmed bool) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.ResolveCoordinatorReport(ctx, requestID, id, throughSeq, action, confirmed)
		return err
	})
}

// PublishTask releases a draft the Coordinator (or the person) filed.
func (b *AssistantsBridge) PublishTask(ctx context.Context, requestID, id string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.PublishTask(ctx, requestID, id, revision)
		return err
	})
}

// BindCoordinator points the embedded Chat at this Assistant's Coordinator.
// The pane comes only from the plugin's own record of the Coordinator's run —
// never from a pane title, a folder or a name — and the Chat binds only when
// Herdr (or a hook) has reported the Agent's session, so two Assistants'
// Chats can never read each other's conversation.
func (b *AssistantsBridge) BindCoordinator(ctx context.Context, assistantID string) (CoordinatorChat, error) {
	out := CoordinatorChat{AssistantID: assistantID, State: personalagent.CoordinatorNone}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	rctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	co, err := c.Coordinator(rctx, assistantID)
	if err != nil {
		return out, err
	}
	return b.bindState(assistantID, co), nil
}

// bindState binds the Chat when the Coordinator is ready and says why not
// otherwise.
func (b *AssistantsBridge) bindState(assistantID string, co personalagent.Coordinator) CoordinatorChat {
	out := CoordinatorChat{AssistantID: assistantID, State: co.State, PaneID: co.PaneID}
	if co.State != personalagent.CoordinatorReady {
		out.Reason = coordinatorReason(co.State)
		return out
	}
	b.shell.callOnWindow(func() { out = b.shell.bindAssistantCoordinator(out, co) })
	return out
}

func coordinatorReason(state string) string {
	switch state {
	case personalagent.CoordinatorStarting:
		return "The Coordinator is starting."
	case personalagent.CoordinatorFailed:
		return "The Coordinator could not start."
	case personalagent.CoordinatorBlocked:
		return "The Coordinator is waiting for a permission or trust decision."
	case personalagent.CoordinatorUncertain:
		return "The original Coordinator launch is uncertain; do not start a duplicate."
	case personalagent.CoordinatorStopped:
		return "The Coordinator is stopped."
	}
	return "This Assistant has no Coordinator yet."
}

// bindAssistantCoordinator binds the Chat to a ready Coordinator's pane. Call
// on the window lane.
func (s *Shell) bindAssistantCoordinator(out CoordinatorChat, co personalagent.Coordinator) CoordinatorChat {
	if co.PaneID == "" || !coordinatorInSession(co, herdr.ParseRef(s.activeInstance).Session) {
		out.Reason = "The Coordinator is not open in this window."
		return out
	}
	card, ok := s.agentCardForPane(co.PaneID)
	if !ok {
		out.Reason = "The Coordinator's pane is not in this window."
		return out
	}
	if !s.bindChatToCard(card) {
		out.Reason = "The Coordinator has not reported its session yet."
		return out
	}
	out.Bound = true
	out.Invitation = s.chatConversationID == ""
	return out
}

// Select records the Assistant the page is on (the sidebar row follows).
func (b *AssistantsBridge) Select(id string) {
	b.shell.applyOnWindow(func() {
		b.shell.assistants.selected = id
		b.shell.assistants.selectedGroup = ""
	})
}

// SelectGroup records the Group the page is on (the sidebar row follows).
func (b *AssistantsBridge) SelectGroup(id string) {
	b.shell.applyOnWindow(func() { b.shell.assistants.selectedGroup = id })
}

// CreateAssistant adds an Assistant and selects it.
func (b *AssistantsBridge) CreateAssistant(ctx context.Context, requestID, name, description string) (string, error) {
	c, err := b.client()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	a, err := c.CreateAssistant(ctx, requestID, strings.TrimSpace(name), strings.TrimSpace(description))
	if err != nil {
		return "", err
	}
	b.Select(a.ID)
	b.shell.applyOnWindow(func() { b.shell.refreshAssistants(true) })
	return a.ID, nil
}

// RenameAssistant renames one Assistant.
func (b *AssistantsBridge) RenameAssistant(ctx context.Context, requestID, id, name string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.RenameAssistant(ctx, requestID, id, strings.TrimSpace(name), revision)
		return err
	})
}

// SetAssistantEnabled pauses or resumes one Assistant.
func (b *AssistantsBridge) SetAssistantEnabled(ctx context.Context, requestID, id string, enabled bool, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.SetAssistantEnabled(ctx, requestID, id, enabled, revision)
		return err
	})
}

// CreateGoal drafts a Goal (a PR Watch when Watch is set).
func (b *AssistantsBridge) CreateGoal(ctx context.Context, requestID string, in AssistantsGoalInput) error {
	input := personalagent.CreateGoalInput{AssistantID: in.AssistantID, Title: strings.TrimSpace(in.Title), Objective: strings.TrimSpace(in.Objective)}
	if w := in.Watch; w != nil {
		spec, err := json.Marshal(map[string]any{
			"repo": strings.TrimSpace(w.Repo), "number": w.Number, "intervalSeconds": w.IntervalSeconds,
			"agent": w.Agent, "model": w.Model, "repoPath": strings.TrimSpace(w.RepoPath),
		})
		if err != nil {
			return err
		}
		input.Commitments = []personalagent.CommitmentInput{{
			Kind: "pr_watch", Spec: string(spec),
			SuccessCriteria: fmt.Sprintf("%s#%d is reviewed and settled", w.Repo, w.Number),
			ExitCondition:   "merged or closed",
		}}
	}
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.CreateGoal(ctx, requestID, input)
		return err
	})
}

// GoalAction runs approve | pause | resume | cancel | complete on a Goal.
func (b *AssistantsBridge) GoalAction(ctx context.Context, requestID, action, id string, revision int, evidence string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.GoalAction(ctx, requestID, action, id, revision, evidence)
		return err
	})
}

// RoutineAction withdraws a draft routine or pauses/resumes an enabled one
// (action cancel | pause | resume). Enabling is the approval card's answer.
func (b *AssistantsBridge) RoutineAction(ctx context.Context, requestID, action, name string) error {
	if action != "cancel" && action != "pause" && action != "resume" {
		return errors.New("unknown routine action")
	}
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.RoutineAction(ctx, requestID, action, name)
		return err
	})
}

// CreateTask queues a task for an Assistant.
func (b *AssistantsBridge) CreateTask(ctx context.Context, requestID string, in personalagent.CreateTaskInput) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.CreateTask(ctx, requestID, in)
		return err
	})
}

// AckTask clears a finished task's needs-you.
func (b *AssistantsBridge) AckTask(ctx context.Context, requestID, id string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.AckTask(ctx, requestID, id, revision)
		return err
	})
}

// RetryTask re-queues a failed or cancelled task.
func (b *AssistantsBridge) RetryTask(ctx context.Context, requestID, id string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.RetryTask(ctx, requestID, id, revision)
		return err
	})
}

// DecideApproval answers one Approval; the hash binds the decision to the
// exact action the person saw.
func (b *AssistantsBridge) DecideApproval(ctx context.Context, requestID, id, actionHash string, approve bool) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.DecideApproval(ctx, requestID, id, actionHash, approve)
		return err
	})
}

// OpenPane selects a Run's pane in the Workspace (only a pane the tree still
// lists; no new Herdr method, no --takeover).
func (b *AssistantsBridge) OpenPane(paneID string) bool {
	s := b.shell
	opened := false
	s.callOnWindow(func() {
		if paneByID(s.projection, paneID) == nil {
			return
		}
		s.openPane(paneID)
		opened = true
	})
	return opened
}

// write runs one command with the gate check and a bounded context, then asks
// the sidebar to re-read its counters.
func (b *AssistantsBridge) write(ctx context.Context, run func(context.Context, *personalagent.Client) error) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	if err := run(ctx, c); err != nil {
		return err
	}
	b.shell.applyOnWindow(func() { b.shell.refreshAssistants(true) })
	return nil
}

// bindChatToCard is openChat's binding without its navigation: a page that
// embeds the Chat (the Assistants conversation) keeps the route and the Mac
// user's Workspace selection where they are (2026-11-04). It reports whether
// the Chat is on the card now — bound to its transcript, or latched on the
// new-session invitation when the agent has no transcript yet (a fresh
// Coordinator writes none until its first prompt).
func (s *Shell) bindChatToCard(card agent.AgentCardModel) bool {
	s.chatBriefPane = card.PaneID
	s.rebindChatForCard(card)
	return s.chatPaneID == card.PaneID && (s.chatConversationID != "" || s.chatSourcelessPaneID == card.PaneID)
}

// assistantBriefPrefix opens the plugin's standing Coordinator brief
// (personalagent.CoordinatorPrompt); the Chat of an Assistants-bound pane
// leaves that first row out.
const assistantBriefPrefix = "You are the Coordinator of the Solco Assistant"

// assistantBrief recognises that brief row. The daemon types every prompt behind
// a `Solco-Run-Id: <id>` marker line, so the marker is dropped before the
// prefix check and from the text the page shows.
func assistantBrief(turn chatsession.ChatTurn) (string, bool) {
	if !turn.User {
		return "", false
	}
	text := strings.TrimSpace(turn.Text)
	if strings.HasPrefix(text, "Solco-Run-Id: ") {
		if _, rest, found := strings.Cut(text, "\n"); found {
			text = strings.TrimSpace(rest)
		}
	}
	return text, strings.HasPrefix(text, assistantBriefPrefix)
}

// openPane shows one pane of this session.
func (s *Shell) openPane(id string) {
	s.hideQuickPanel()
	s.router.Push(routeWorkspace)
	s.selectPane(id)
}
