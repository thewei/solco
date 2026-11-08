package personalagent

// CoordinatorReport describes an opt-in periodic narrative prompt sent to an
// existing OMP Coordinator through the Herdr pane. "submitted" means Herdr
// accepted the prompt; it does not prove an Agent reply was produced.
// "uncertain" forbids automatic retries until the person checks the original
// conversation and explicitly resolves that exact throughSeq.
type CoordinatorReport struct {
	AssistantID         string `json:"assistantId"`
	State               string `json:"state"`
	FromSeq             int64  `json:"fromSeq"`
	ThroughSeq          int64  `json:"throughSeq"`
	SubmittedSeq        int64  `json:"submittedSeq"`
	Attempts            int    `json:"attempts"`
	NextAt              int64  `json:"nextAt"`
	LastSubmittedAt     int64  `json:"lastSubmittedAt"`
	LastError           string `json:"lastError,omitempty"`
	HerdrSession        string `json:"herdrSession,omitempty"`
	WorkspaceID         string `json:"workspaceId,omitempty"`
	PaneID              string `json:"paneId,omitempty"`
	CoordinatorRevision int    `json:"coordinatorRevision"`
	UpdatedAt           int64  `json:"updatedAt"`
	// Herdr accepting a report != a final OMP answer. Only a unique marker
	// in OMP's original JSONL with a finished assistant record is verified.
	ReplyState      string `json:"replyState,omitempty"`
	ReplySessionID  string `json:"replySessionId,omitempty"`
	ReplyMessageID  string `json:"replyMessageId,omitempty"`
	ReplyVerifiedAt int64  `json:"replyVerifiedAt,omitempty"`
}
