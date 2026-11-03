package nativeui

/**
 * [INPUT]: 依赖 assistants_state 的门/客户端（assistantsClientFor）、internal/personalagent 的 CLI 客户端与类型、Shell.openPane、herdr.ParseRef
 * [OUTPUT]: AssistantsBridge（`mygo.BindAs("assistants", …)`）——Snapshot 读视图（概览 + 选中 Assistant 的 Goal/Task/Approval/Memory/Run），命令（建/改名/启停 Assistant、建 Goal 与 PR Watch、Goal 动作、建/确认/重试 Task、批准/拒绝 Approval、打开 Run 的窗格）与 Select（页面导航）
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
	"github.com/thewei/solco/internal/herdr"
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
	// NewForm asks the page to open its New Assistant form (the sidebar's "+");
	// reading it clears it.
	NewForm bool `json:"newForm"`
}

// AssistantsDetail is the selected Assistant's page.
type AssistantsDetail struct {
	Assistant personalagent.Assistant    `json:"assistant"`
	Goals     []personalagent.Goal       `json:"goals"`
	Tasks     []AssistantsTask           `json:"tasks"`
	Approvals []personalagent.Approval   `json:"approvals"`
	Memory    []personalagent.MemoryItem `json:"memory"`
	// Coordinator is the Assistant's own Coordinator Agent; CoordinatorOpen
	// says its pane is in this window's Herdr tree (so its Chat can bind).
	Coordinator     personalagent.Coordinator `json:"coordinator"`
	CoordinatorOpen bool                      `json:"coordinatorOpen"`
	// Inbox is the plugin's one snapshot of what needs a person (approvals,
	// reviews, failed starts, stalls); the page draws it, never recomputes it.
	Inbox personalagent.Inbox `json:"inbox"`
}

// CoordinatorChat is the answer to "bind the embedded Chat to this Assistant's
// Coordinator": Bound says the Chat is on it now; otherwise State/Reason say
// why not (starting, waiting for its session, not in this window, …).
type CoordinatorChat struct {
	AssistantID string `json:"assistantId"`
	State       string `json:"state"`
	PaneID      string `json:"paneId"`
	Bound       bool   `json:"bound"`
	Reason      string `json:"reason"`
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
	view := AssistantsView{Assistants: []personalagent.OverviewRow{}}
	var panes map[string]bool
	var client *personalagent.Client
	var session, selected string
	s.callOnWindow(func() {
		view.Theme = s.chatBridgeTheme()
		view.NewForm, s.assistants.newForm = s.assistants.newForm, false
		if s.assistantsAvailable() {
			client = s.assistantsClientFor()
			session = herdr.ParseRef(s.activeInstance).Session
			selected = s.assistants.selected
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

// assistantsDetail reads one Assistant's page. A Run can be opened only while
// it ran in the window's Herdr session and its pane is still in the tree.
func assistantsDetail(ctx context.Context, client *personalagent.Client, id, session string, panes map[string]bool) (*AssistantsDetail, error) {
	list, err := client.Assistants(ctx)
	if err != nil {
		return nil, err
	}
	d := &AssistantsDetail{Goals: []personalagent.Goal{}, Tasks: []AssistantsTask{}, Approvals: []personalagent.Approval{}, Memory: []personalagent.MemoryItem{}}
	found := false
	for _, a := range list {
		if a.ID == id {
			d.Assistant, found = a, true
		}
	}
	if !found {
		return nil, nil
	}
	if goals, err := client.Goals(ctx, id); err != nil {
		return nil, err
	} else if goals != nil {
		d.Goals = goals
	}
	if approvals, err := client.Approvals(ctx, id); err != nil {
		return nil, err
	} else if approvals != nil {
		d.Approvals = approvals
	}
	if memory, err := client.VisibleMemory(ctx, id); err != nil {
		return nil, err
	} else if memory != nil {
		d.Memory = memory
	}
	tasks, err := client.Tasks(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Inbox = personalagent.Inbox{Items: []personalagent.InboxItem{}}
	if in, err := client.Inbox(ctx, id); err == nil && in.Items != nil {
		d.Inbox = in
	}
	if co, err := client.Coordinator(ctx, id); err == nil {
		d.Coordinator = co
		d.CoordinatorOpen = co.PaneID != "" && coordinatorInSession(co, session) && panes[co.PaneID]
	}
	for _, t := range tasks {
		item := AssistantsTask{Task: t, Runs: []AssistantsRun{}}
		if _, runs, err := client.TaskDetail(ctx, t.ID); err == nil {
			for _, r := range runs {
				same := r.HerdrSession == "" || herdr.ParseRef(r.HerdrSession).Session == session || r.HerdrSession == session
				item.Runs = append(item.Runs, AssistantsRun{Run: r, CanOpen: r.PaneID != "" && same && panes[r.PaneID]})
			}
		}
		d.Tasks = append(d.Tasks, item)
	}
	return d, nil
}

// coordinatorInSession says the Coordinator ran in the window's Herdr session
// (a Coordinator that ran elsewhere is never opened or bound here).
func coordinatorInSession(co personalagent.Coordinator, session string) bool {
	return co.HerdrSession == "" || herdr.ParseRef(co.HerdrSession).Session == session || co.HerdrSession == session
}

// StartCoordinator asks the daemon to bring the Assistant's Coordinator up as
// a real Herdr Agent (the plugin owns the lifecycle).
func (b *AssistantsBridge) StartCoordinator(ctx context.Context, requestID, id string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.StartCoordinator(ctx, requestID, id)
		return err
	})
}

// StopCoordinator marks the Coordinator stopped.
func (b *AssistantsBridge) StopCoordinator(ctx context.Context, requestID, id string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.StopCoordinator(ctx, requestID, id)
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
	out.State, out.PaneID = co.State, co.PaneID
	if co.State != personalagent.CoordinatorReady {
		out.Reason = coordinatorReason(co.State)
		return out, nil
	}
	b.shell.callOnWindow(func() { out = b.shell.bindAssistantCoordinator(out, co) })
	return out, nil
}

func coordinatorReason(state string) string {
	switch state {
	case personalagent.CoordinatorStarting:
		return "The Coordinator is starting."
	case personalagent.CoordinatorFailed:
		return "The Coordinator could not start."
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
	return out
}

// Select records the Assistant the page is on (the sidebar row follows).
func (b *AssistantsBridge) Select(id string) {
	b.shell.applyOnWindow(func() { b.shell.assistants.selected = id })
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

// bindChatToCard is openChat's binding without its navigation: a page that embeds the Chat (the Assistants
// Coordinator tab) keeps the route where it is. False says the agent has no chat source yet.
func (s *Shell) bindChatToCard(card agent.AgentCardModel) bool {
	s.chatPaneID = card.PaneID
	if card.PaneID != "" {
		s.selectPane(card.PaneID)
	}
	if s.bindLiveAgent(card.Key) {
		return true
	}
	if card.ConversationID != "" {
		s.bindChatConversation(card.ConversationID)
		return true
	}
	if s.liveSourcePending(card.Key) {
		s.rebindWorkspaceChat()
		return true
	}
	return false
}

// openPane shows one pane of this session.
func (s *Shell) openPane(id string) {
	s.hideQuickPanel()
	s.router.Push(routeWorkspace)
	s.selectPane(id)
}
