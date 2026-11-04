package personalagent

import "encoding/json"

// Types mirror the plugin CLI's JSON (golden files in
// plugins/solco-automations/internal/personalagent/testdata decode strictly
// into them). The plugin stays the authority: Solco presents, it never
// recomputes state.

// Assistant is one persistent identity.
type Assistant struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	RolePrompt         string   `json:"rolePrompt"`
	Scope              string   `json:"scope"`
	WorkspaceRefs      []string `json:"workspaceRefs"`
	CoordinatorSession string   `json:"coordinatorSession"`
	DefaultPolicy      string   `json:"defaultPolicy"`
	Status             string   `json:"status"`
	Revision           int      `json:"revision"`
	CreatedAt          int64    `json:"createdAt"`
	UpdatedAt          int64    `json:"updatedAt"`
}

// Commitment is one thing a Goal keeps doing.
type Commitment struct {
	ID              string `json:"id"`
	AssistantID     string `json:"assistantId"`
	GoalID          string `json:"goalId"`
	Kind            string `json:"kind"`
	SuccessCriteria string `json:"successCriteria"`
	ExitCondition   string `json:"exitCondition"`
	Spec            string `json:"spec"`
	State           string `json:"state"`
	NextCheckAt     int64  `json:"nextCheckAt"`
	LastCheckedAt   int64  `json:"lastCheckedAt"`
	LastError       string `json:"lastError"`
	Cursor          string `json:"cursor"`
	Revision        int    `json:"revision"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

// Goal is a long-lived objective.
type Goal struct {
	ID          string       `json:"id"`
	AssistantID string       `json:"assistantId"`
	Title       string       `json:"title"`
	Objective   string       `json:"objective"`
	State       string       `json:"state"`
	Evidence    string       `json:"evidence"`
	Commitments []Commitment `json:"commitments"`
	Revision    int          `json:"revision"`
	CreatedAt   int64        `json:"createdAt"`
	UpdatedAt   int64        `json:"updatedAt"`
}

// Task is one delegated unit of work.
type Task struct {
	ID           string `json:"id"`
	AssistantID  string `json:"assistantId"`
	GoalID       string `json:"goalId"`
	RuleID       string `json:"ruleId"`
	ParentTaskID string `json:"parentTaskId"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Brief        string `json:"brief"`
	RepoPath     string `json:"repoPath"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	State        string `json:"state"`
	NeedsYou     bool   `json:"needsYou"`
	ReviewState  string `json:"reviewState"`
	Attempt      int    `json:"attempt"`
	Revision     int    `json:"revision"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// Run is one execution of a task, with the real Herdr identity.
type Run struct {
	ID             string `json:"id"`
	TaskID         string `json:"taskId"`
	RuleID         string `json:"ruleId"`
	EventID        string `json:"eventId"`
	IdempotencyKey string `json:"idempotencyKey"`
	Status         string `json:"status"`
	Claimant       string `json:"claimant"`
	HerdrSession   string `json:"herdrSession"`
	WorkspaceID    string `json:"workspaceId"`
	PaneID         string `json:"paneId"`
	AgentSessionID string `json:"agentSessionId"`
	Label          string `json:"label"`
	Result         string `json:"result"`
	Error          string `json:"error"`
	CreatedAt      int64  `json:"createdAt"`
	StartedAt      int64  `json:"startedAt"`
	FinishedAt     int64  `json:"finishedAt"`
}

// MemoryItem is one remembered thing.
type MemoryItem struct {
	ID          string   `json:"id"`
	AssistantID string   `json:"assistantId"`
	Scope       string   `json:"scope"`
	Kind        string   `json:"kind"`
	Content     string   `json:"content"`
	SourceRef   string   `json:"sourceRef"`
	State       string   `json:"state"`
	SharedWith  []string `json:"sharedWith"`
	Revision    int      `json:"revision"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// Approval is a question to a person about one exact action.
type Approval struct {
	ID               string `json:"id"`
	AssistantID      string `json:"assistantId"`
	SubjectKind      string `json:"subjectKind"`
	SubjectID        string `json:"subjectId"`
	Action           string `json:"action"`
	ActionHash       string `json:"actionHash"`
	ExpectedRevision int    `json:"expectedRevision"`
	ExpiresAt        int64  `json:"expiresAt"`
	State            string `json:"state"`
	DecidedAt        int64  `json:"decidedAt"`
	CreatedAt        int64  `json:"createdAt"`
}

// OverviewRow is one Assistant's counters.
type OverviewRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Revision  int    `json:"revision"`
	Tasks     int    `json:"tasks"`
	Queued    int    `json:"queued"`
	Running   int    `json:"running"`
	NeedsYou  int    `json:"needsYou"`
	Done      int    `json:"done"`
	Goals     int    `json:"goals"`
	Approvals int    `json:"approvals"`
}

// Overview is the sidebar/phone summary.
type Overview struct {
	Schema     int             `json:"schema"`
	Assistants []OverviewRow   `json:"assistants"`
	NeedsYou   int             `json:"needsYou"`
	Groups     []OverviewGroup `json:"groups"`
}

// OverviewGroup is one Group's light row: members, log length, @-mentions
// still on their way.
type OverviewGroup struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Members   []GroupMember `json:"members"`
	LastSeq   int64         `json:"lastSeq"`
	Open      int           `json:"open"`
	UpdatedAt int64         `json:"updatedAt"`
}

// Error is a refusal with a stable code (request_conflict, stale_revision,
// not_found, assistant_disabled, busy, not_claimable, bad_state, invalid,
// uncertain, internal). Branch on Code, show Message.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// CreateTaskInput is the body of a task creation.
type CreateTaskInput struct {
	AssistantID string `json:"assistantId"`
	GoalID      string `json:"goalId,omitempty"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Brief       string `json:"brief"`
	RepoPath    string `json:"repoPath,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Draft       bool   `json:"draft,omitempty"`
	// DependsOn lists tasks of the same Assistant that must be done first.
	DependsOn    []string `json:"dependsOn,omitempty"`
	ParentTaskID string   `json:"parentTaskId,omitempty"`
}

// CommitmentInput drafts a commitment together with its Goal.
type CommitmentInput struct {
	Kind            string `json:"kind"`
	SuccessCriteria string `json:"successCriteria,omitempty"`
	ExitCondition   string `json:"exitCondition,omitempty"`
	Spec            string `json:"spec,omitempty"`
}

// CreateGoalInput is the body of a goal creation.
type CreateGoalInput struct {
	AssistantID string            `json:"assistantId"`
	Title       string            `json:"title"`
	Objective   string            `json:"objective,omitempty"`
	Commitments []CommitmentInput `json:"commitments,omitempty"`
}

// Coordinator is an Assistant's Coordinator Agent as the plugin reports it:
// the session fields are exactly what Herdr reported for its run.
type Coordinator struct {
	AssistantID    string `json:"assistantId"`
	State          string `json:"state"`
	TaskID         string `json:"taskId,omitempty"`
	HerdrSession   string `json:"herdrSession,omitempty"`
	WorkspaceID    string `json:"workspaceId,omitempty"`
	PaneID         string `json:"paneId,omitempty"`
	AgentSessionID string `json:"agentSessionId,omitempty"`
	Revision       int    `json:"revision"`
	CreatedAt      int64  `json:"createdAt,omitempty"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
}

// Coordinator states.
const (
	CoordinatorNone     = "none"
	CoordinatorStarting = "starting"
	CoordinatorReady    = "ready"
	CoordinatorStopped  = "stopped"
	CoordinatorFailed   = "failed"
)

// InboxItem is one thing that needs a person (or, for news, is worth a glance),
// from the plugin's single Inbox snapshot.
type InboxItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	AssistantID string `json:"assistantId"`
	TaskID      string `json:"taskId,omitempty"`
	ApprovalID  string `json:"approvalId,omitempty"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	Priority    int    `json:"priority"`
	At          int64  `json:"at"`
}

// Inbox is the snapshot: items by priority, the count that needs a person and
// the event watermark it was read at.
type Inbox struct {
	Items    []InboxItem `json:"items"`
	NeedsYou int         `json:"needsYou"`
	Seq      int64       `json:"seq"`
}

// Message author and kind values of a Group's log.
const (
	AuthorUser      = "user"
	AuthorAssistant = "assistant"
	AuthorSystem    = "system"

	MessageText = "text"
	MessageFact = "fact"
	MessageTask = "task"
)

// GroupMember is one Assistant in a Group.
type GroupMember struct {
	AssistantID string `json:"assistantId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
}

// Group is a conversation of the person with two or more Assistants.
type Group struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Status    string        `json:"status"`
	Members   []GroupMember `json:"members"`
	LastSeq   int64         `json:"lastSeq"`
	Revision  int           `json:"revision"`
	CreatedAt int64         `json:"createdAt"`
	UpdatedAt int64         `json:"updatedAt"`
}

// GroupMessage is one row of a Group's log. A `fact` is a receipt the core
// wrote itself (Fact says which); a `task` row points at a live task.
type GroupMessage struct {
	Seq        int64           `json:"seq"`
	ID         string          `json:"id"`
	GroupID    string          `json:"groupId"`
	AuthorKind string          `json:"authorKind"`
	AuthorID   string          `json:"authorId,omitempty"`
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Mentions   []string        `json:"mentions"`
	ReplyTo    string          `json:"replyTo,omitempty"`
	TaskID     string          `json:"taskId,omitempty"`
	Fact       json.RawMessage `json:"fact,omitempty"`
	CreatedAt  int64           `json:"createdAt"`
}

// GroupDelivery says whether a mentioned Assistant has been told of a message.
type GroupDelivery struct {
	ID          string `json:"id"`
	MessageID   string `json:"messageId"`
	AssistantID string `json:"assistantId"`
	State       string `json:"state"`
	Attempts    int    `json:"attempts"`
}

// GroupTaskCard is a task's live state for a card in a Group.
type GroupTaskCard struct {
	TaskID      string `json:"taskId"`
	AssistantID string `json:"assistantId"`
	Title       string `json:"title"`
	State       string `json:"state"`
	NeedsYou    bool   `json:"needsYou"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// GroupAgentState is the Agent an Assistant runs for a Group (none until the
// first @-mention starts it).
type GroupAgentState struct {
	AssistantID string `json:"assistantId"`
	State       string `json:"state"`
	PaneID      string `json:"paneId,omitempty"`
}

// GroupFeed is what a client renders.
type GroupFeed struct {
	Group      Group             `json:"group"`
	Messages   []GroupMessage    `json:"messages"`
	Deliveries []GroupDelivery   `json:"deliveries"`
	Tasks      []GroupTaskCard   `json:"tasks"`
	Agents     []GroupAgentState `json:"agents"`
	Seq        int64             `json:"seq"`
}

// PostResult is the stored message and the deliveries it queued.
type PostResult struct {
	Message    GroupMessage    `json:"message"`
	Deliveries []GroupDelivery `json:"deliveries"`
}

// Persona (Assistants 3.1 U2): the Assistant's working style, its own
// Agent/Model/Effort and its MCP grants. The wire shapes are pinned by the
// plugin's persona-*.json goldens.

// InstructionRevision is one stored version of the working style.
type InstructionRevision struct {
	Revision  int    `json:"revision"`
	Source    string `json:"source"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

// AppliedRevision is the revision a running surface was launched with.
type AppliedRevision struct {
	Surface   string `json:"surface"`
	Revision  int    `json:"revision"`
	AppliedAt int64  `json:"appliedAt"`
}

// Instructions is the Assistant's active working style and its history.
type Instructions struct {
	AssistantID string                `json:"assistantId"`
	Revision    int                   `json:"revision"`
	Body        string                `json:"body"`
	History     []InstructionRevision `json:"history"`
	Applied     []AppliedRevision     `json:"applied"`
}

// RuntimeProfile is the Agent an Assistant starts by default.
type RuntimeProfile struct {
	AssistantID     string `json:"assistantId"`
	Agent           string `json:"agent"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`
	EffortSupported bool   `json:"effortSupported"`
	Revision        int    `json:"revision"`
	UpdatedAt       int64  `json:"updatedAt"`
}

// RunningAgent is an Agent the Assistant really has right now.
type RunningAgent struct {
	Surface string `json:"surface"`
	TaskID  string `json:"taskId"`
	Agent   string `json:"agent"`
	Model   string `json:"model"`
	State   string `json:"state"`
	PaneID  string `json:"paneId,omitempty"`
}

// Runtime is the profile plus what really runs.
type Runtime struct {
	Profile RuntimeProfile `json:"profile"`
	Running []RunningAgent `json:"running"`
}

// MCPServer is a configured server: names, transport and program/host only.
type MCPServer struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Summary   string   `json:"summary"`
	Sources   []string `json:"sources"`
	Agents    []string `json:"agents"`
}

// MCPServerState keeps the four states apart: configured, granted, bound, verified.
type MCPServerState struct {
	MCPServer
	Configured bool     `json:"configured"`
	Granted    bool     `json:"granted"`
	Bound      []string `json:"bound"`
	Verified   string   `json:"verified"`
	VerifiedAt int64    `json:"verifiedAt"`
}

// MCPBinding is what one surface's last launch was handed.
type MCPBinding struct {
	Surface     string   `json:"surface"`
	Agent       string   `json:"agent"`
	Servers     []string `json:"servers"`
	Strict      bool     `json:"strict"`
	Unsupported string   `json:"unsupported,omitempty"`
	BoundAt     int64    `json:"boundAt"`
}

// MCPView is an Assistant's MCP picture.
type MCPView struct {
	AssistantID string           `json:"assistantId"`
	Agent       string           `json:"agent"`
	Supported   bool             `json:"supported"`
	Unsupported string           `json:"unsupported,omitempty"`
	Servers     []MCPServerState `json:"servers"`
	Bindings    []MCPBinding     `json:"bindings"`
}

// MilestoneTask is the work a plan milestone delegates.
type MilestoneTask struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Brief    string `json:"brief"`
	RepoPath string `json:"repoPath,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Milestone is one verifiable step of a Goal's plan.
type Milestone struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	DependsOn []string       `json:"dependsOn,omitempty"`
	Task      *MilestoneTask `json:"task,omitempty"`
	TaskID    string         `json:"taskId,omitempty"`
	Attempt   int            `json:"attempt,omitempty"`
}

// MilestoneView is a milestone with its derived state: pending, working,
// verifying, verified, blocked or needs_you.
type MilestoneView struct {
	Milestone
	State    string `json:"state"`
	Evidence string `json:"evidence,omitempty"`
}

// PlanRevision is one entry of a plan's trail.
type PlanRevision struct {
	Revision   int    `json:"revision"`
	Reason     string `json:"reason"`
	Milestones int    `json:"milestones"`
	CreatedAt  int64  `json:"createdAt"`
}

// GoalPlan is a Goal's plan with every state derived from committed tasks.
type GoalPlan struct {
	GoalID          string          `json:"goalId"`
	AssistantID     string          `json:"assistantId"`
	SuccessCriteria string          `json:"successCriteria"`
	Milestones      []MilestoneView `json:"milestones"`
	NextAction      string          `json:"nextAction"`
	PlanRevision    int             `json:"planRevision"`
	Replans         int             `json:"replans"`
	MaxTasks        int             `json:"maxTasks"`
	StaleAfterMS    int64           `json:"staleAfterMs"`
	Verified        int             `json:"verified"`
	Total           int             `json:"total"`
	State           string          `json:"state"`
	Revisions       []PlanRevision  `json:"revisions"`
	UpdatedAt       int64           `json:"updatedAt"`
}

// GoalPlanInput is what a person or Coordinator submits as a plan.
type GoalPlanInput struct {
	SuccessCriteria string      `json:"successCriteria,omitempty"`
	Milestones      []Milestone `json:"milestones"`
	NextAction      string      `json:"nextAction,omitempty"`
}

// Fact is one entry of the ledger: a sentence the core wrote about committed
// state (accepted, planned, delegated, verified, blocked, ready, done, stale,
// held), never an Agent's words.
type Fact struct {
	Seq         int64  `json:"seq"`
	ID          string `json:"id"`
	AssistantID string `json:"assistantId"`
	GoalID      string `json:"goalId,omitempty"`
	TaskID      string `json:"taskId,omitempty"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	Evidence    string `json:"evidence,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// Routine is a scheduled automation that belongs to an Assistant, with the
// definition it points at and how it is doing. The definition itself lives in
// the Automations plugin's own file; prompt and rationale are clipped.
type Routine struct {
	Name          string `json:"name"`
	AssistantID   string `json:"assistantId"`
	RequestedBy   string `json:"requestedBy"`
	Source        string `json:"source"`
	CorrelationID string `json:"correlationId,omitempty"`
	// State: draft | active | stale | cancelled.
	State         string `json:"state"`
	Revision      int    `json:"revision"`
	Approved      bool   `json:"approved"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	Cron          string `json:"cron"`
	Repo          string `json:"repo"`
	Agent         string `json:"agent"`
	Model         string `json:"model,omitempty"`
	Headless      bool   `json:"headless"`
	Paused        bool   `json:"paused"`
	Missing       bool   `json:"missing,omitempty"`
	Rationale     string `json:"rationale,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	Next          int64  `json:"next,omitempty"`
	LastState     string `json:"lastState,omitempty"`
	LastAt        int64  `json:"lastAt,omitempty"`
	ApprovalID    string `json:"approvalId,omitempty"`
	ApprovalState string `json:"approvalState,omitempty"`
}

// MemoryVersion is one revision of a memory item; Author is who made it
// (user, agent, files, system) — a label, not a proof.
type MemoryVersion struct {
	MemoryID string `json:"memoryId"`
	Revision int    `json:"revision"`
	Author   string `json:"author"`
	State    string `json:"state"`
	Content  string `json:"content"`
	At       int64  `json:"at"`
}

// MemoryReview is one Assistant's live memory (candidates and approved) with
// what looks wrong: duplicate wording, unanswered candidates, edit conflicts.
type MemoryReview struct {
	AssistantID     string       `json:"assistantId"`
	Approved        int          `json:"approved"`
	Candidates      int          `json:"candidates"`
	Max             int          `json:"max"`
	Duplicates      [][]string   `json:"duplicates"`
	StaleCandidates []string     `json:"staleCandidates"`
	Conflicts       []string     `json:"conflicts"`
	Items           []MemoryItem `json:"items"`
}

// Proactivity is the rules for what the system may start on its own for one
// Assistant. Revision 0 = never set (the defaults apply).
type Proactivity struct {
	AssistantID    string `json:"assistantId"`
	Level          string `json:"level"`
	QuietStart     string `json:"quietStart"`
	QuietEnd       string `json:"quietEnd"`
	TZ             string `json:"tz"`
	ReportEveryMin int    `json:"reportEveryMin"`
	DailyBudget    int    `json:"dailyBudget"`
	DispatchToday  int    `json:"dispatchToday"`
	QuietNow       bool   `json:"quietNow"`
	Revision       int    `json:"revision"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// ProactivityInput is what a person may change.
type ProactivityInput struct {
	Level          string `json:"level"`
	QuietStart     string `json:"quietStart"`
	QuietEnd       string `json:"quietEnd"`
	TZ             string `json:"tz"`
	ReportEveryMin int    `json:"reportEveryMin"`
	DailyBudget    int    `json:"dailyBudget"`
}
