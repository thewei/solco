package personalagent

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
	Schema     int           `json:"schema"`
	Assistants []OverviewRow `json:"assistants"`
	NeedsYou   int           `json:"needsYou"`
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
