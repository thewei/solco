package feedback

/**
 * [INPUT]: 无（纯数据）
 * [OUTPUT]: 对外提供 Question/Answer/Attachment/Ask/Thread 与它们的状态常量、CreateInput、ThreadSummary、Event
 * [POS]: Feedback 收件箱的数据模型（2026-11-09 决策）：Ask = Agent 通过 MCP 提出的一组问题，Thread = 同一项目同一 Agent 的连续提问；
 *        JSON 形状同时是 broker HTTP、桌面 WebView 桥与手机 Host v3 的线格式（camelCase），改动要三处一起改
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// AskState is where an Ask stands. Answered after expired is a late answer (Ask.Late).
type AskState string

const (
	StatePending  AskState = "pending"
	StateAnswered AskState = "answered"
	StateExpired  AskState = "expired" // timed out or the Agent went away; may still be answered late
	StateEnded    AskState = "ended"   // the user ended it; final
)

// Question is one question of an Ask.
type Question struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Type        string   `json:"type"` // single | multiple | text
	Options     []string `json:"options,omitempty"`
	AllowCustom bool     `json:"allowCustom"`
	Required    bool     `json:"required"`
	Placeholder string   `json:"placeholder,omitempty"`
}

// Answer is the user's answer to one Question.
type Answer struct {
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// Attachment is an image the user attached to an answer. Bytes live in memory until delivered; history keeps
// only the count.
type Attachment struct {
	MimeType   string `json:"mimeType"`
	DataBase64 string `json:"dataBase64"`
	Name       string `json:"name,omitempty"`
}

// Ask is one set of questions an Agent put to the user.
type Ask struct {
	ID              string            `json:"id"`
	ThreadID        string            `json:"threadId"`
	CreatedAt       int64             `json:"createdAt"` // unix ms
	ResolvedAt      int64             `json:"resolvedAt,omitempty"`
	Description     string            `json:"description,omitempty"`
	Questions       []Question        `json:"questions"`
	TimeoutMs       int64             `json:"timeoutMs"`
	State           AskState          `json:"state"`
	Answers         map[string]Answer `json:"answers,omitempty"`
	AttachmentCount int               `json:"attachmentCount,omitempty"`
	Message         string            `json:"message,omitempty"`
	Late            bool              `json:"late,omitempty"`
	LateDelivered   bool              `json:"lateDelivered,omitempty"`
	LastHeartbeat   int64             `json:"lastHeartbeat"`
	Disconnected    bool              `json:"disconnected,omitempty"` // computed: pending and the Agent's shim stopped polling
}

// Thread groups the Asks of one Agent in one project.
type Thread struct {
	ID            string `json:"id"`
	ProjectKey    string `json:"projectKey"`
	ProjectRoot   string `json:"projectRoot,omitempty"`
	Project       string `json:"project"` // display name: last path element of ProjectKey
	Branch        string `json:"branch,omitempty"`
	Worktree      bool   `json:"worktree,omitempty"`
	Client        string `json:"client,omitempty"`
	ClientVersion string `json:"clientVersion,omitempty"`
	CallerInfo    string `json:"callerInfo,omitempty"`
	Title         string `json:"title"`
	ExplicitID    string `json:"explicitId,omitempty"`
	TaskID        string `json:"taskId,omitempty"`
	// Task is the user's original request the Agent works on (Markdown), given with the thread's first ask.
	Task      string `json:"task,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	LastAt    int64  `json:"lastAt"`
	Waiting   int    `json:"waiting"` // computed: pending Asks
	Asks      []*Ask `json:"asks,omitempty"`
}

// ThreadSummary is a Thread row without its Asks.
type ThreadSummary struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Branch    string `json:"branch,omitempty"`
	Worktree  bool   `json:"worktree,omitempty"`
	Client    string `json:"client,omitempty"`
	Title     string `json:"title"`
	LastAt    int64  `json:"lastAt"`
	Waiting   int    `json:"waiting"`
	AskCount  int    `json:"askCount"`
	Preview   string `json:"preview,omitempty"` // first line of the newest Ask's first question
	LastState string `json:"lastState,omitempty"`
}

// CreateInput is what an Agent's MCP call carries.
type CreateInput struct {
	ID            string
	CallerInfo    string
	Client        string
	ClientVersion string
	Description   string
	Task          string
	ProjectRoot   string
	TaskID        string
	ThreadID      string
	Questions     []Question
	TimeoutMs     int64
}

// Event is what the SSE stream carries. It names ids and a one-line preview; a UI fetches the rest.
type Event struct {
	Type     string   `json:"type"` // ask_created | ask_resolved
	ThreadID string   `json:"threadId"`
	AskID    string   `json:"askId"`
	State    AskState `json:"state,omitempty"`
	Project  string   `json:"project,omitempty"`
	Client   string   `json:"client,omitempty"`
	Title    string   `json:"title,omitempty"`
	Preview  string   `json:"preview,omitempty"`
}
