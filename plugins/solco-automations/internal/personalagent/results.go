package personalagent

// Result is one settled task as a card above the chat composer: a finished,
// failed or review-waiting task of the last day, with where its newest run
// lives so a client can offer "Open pane". It is a projection of committed task
// state only — never generated text — so the card is the system's word, not an
// Agent's.
type Result struct {
	TaskID string `json:"taskId"`
	Title  string `json:"title"`
	// State is the task's own: done | failed | review_required.
	State        string `json:"state"`
	At           int64  `json:"at"`
	HerdrSession string `json:"herdrSession,omitempty"`
	PaneID       string `json:"paneId,omitempty"`
}

// ResultWindowMS is how long a settled task stays eligible for a card.
const ResultWindowMS = int64(24 * 3_600_000)

// Results lists an Assistant's tasks that settled within a day, newest first
// (limit defaults to 4, at most 20). The Assistant's own Coordinator and Group
// Agents are its conversation, not results. One bounded query: cheap enough for
// a client's few-second poll, unlike the full task list.
func (s *Store) Results(assistantID string, limit int) ([]Result, error) {
	if limit <= 0 || limit > 20 {
		limit = 4
	}
	rows, err := s.db.Query(`SELECT t.id,t.title,t.state,t.updated_at,
		COALESCE((SELECT r.herdr_session FROM runs r WHERE r.task_id=t.id AND r.pane_id<>'' ORDER BY r.created_at DESC, r.id DESC LIMIT 1),''),
		COALESCE((SELECT r.pane_id FROM runs r WHERE r.task_id=t.id AND r.pane_id<>'' ORDER BY r.created_at DESC, r.id DESC LIMIT 1),'')
		FROM tasks t WHERE t.assistant_id=? AND t.kind NOT IN (?,?) AND t.state IN (?,?,?) AND t.updated_at>?
		ORDER BY t.updated_at DESC, t.id LIMIT ?`,
		assistantID, KindCoordinator, KindGroupAgent, TaskDone, TaskFailed, TaskReviewRequired, s.nowMS()-ResultWindowMS, limit)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.TaskID, &r.Title, &r.State, &r.At, &r.HerdrSession, &r.PaneID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, wrapBusy(rows.Err())
}
