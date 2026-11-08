package personalagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Assistants 3.1 U1: a Group is a real conversation of the person with two or
// more Assistants. The Go core is its single authority: one ordered message
// log (`group_messages`, a monotonic seq), the members, and the deliveries that
// say which @-mentioned Assistant has been told about which message. Desktop
// and phone read the same log; neither keeps a conversation of its own.
//
// Two rules keep a Group honest:
//   - only the person's message can start an Agent, and only the Assistants it
//     @-mentions; an Assistant's own message never triggers another delivery
//     (no Agent-to-Agent loops), and a message without a mention calls nobody;
//   - author kinds are facts, not decoration: `user` is the person, `assistant`
//     is written only through a Group Agent that really runs for that member,
//     `system` is a receipt or a task card the core wrote itself.

// Message author kinds and message kinds.
const (
	AuthorUser      = "user"
	AuthorAssistant = "assistant"
	AuthorSystem    = "system"

	MsgText = "text"
	MsgFact = "fact"
	MsgTask = "task"
)

// Delivery states.
const (
	DeliveryQueued    = "queued"
	DeliveryDelivered = "delivered"
	DeliveryFailed    = "failed"
	DeliverySkipped   = "skipped"
	// DeliveryUncertain means the daemon stopped between claiming the delivery
	// and learning whether Herdr took it; it is never retried on its own.
	DeliveryUncertain  = "uncertain"
	DeliveryDelivering = "delivering"
)

const (
	maxGroupBody     = 8000
	maxGroupMembers  = 12
	maxOpenDelivery  = 20
	maxGroupTitleLen = 80
)

// GroupMember is one Assistant in a Group.
type GroupMember struct {
	AssistantID string `json:"assistantId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
}

// Group is a conversation of the person with several Assistants.
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

// GroupMessage is one row of a Group's log.
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

// GroupDelivery says whether a mentioned Assistant has been told about a message.
type GroupDelivery struct {
	ID          string `json:"id"`
	MessageID   string `json:"messageId"`
	AssistantID string `json:"assistantId"`
	State       string `json:"state"`
	Attempts    int    `json:"attempts"`
}

// GroupTaskCard is the live state of a task a Group message points at: read
// from the task table on every feed, never copied into the message.
type GroupTaskCard struct {
	TaskID      string `json:"taskId"`
	AssistantID string `json:"assistantId"`
	Title       string `json:"title"`
	State       string `json:"state"`
	NeedsYou    bool   `json:"needsYou"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// GroupAgentState is the real Agent an Assistant runs for one Group.
type GroupAgentState struct {
	AssistantID string `json:"assistantId"`
	State       string `json:"state"`
	PaneID      string `json:"paneId,omitempty"`
}

// GroupFeed is what a client renders: the log since a seq plus the live facts
// the cards need.
type GroupFeed struct {
	Group      Group             `json:"group"`
	Messages   []GroupMessage    `json:"messages"`
	Deliveries []GroupDelivery   `json:"deliveries"`
	Tasks      []GroupTaskCard   `json:"tasks"`
	Agents     []GroupAgentState `json:"agents"`
	Seq        int64             `json:"seq"`
}

const groupCols = `id,title,status,revision,created_at,updated_at`

func loadMembers(q queryer, groupID string) ([]GroupMember, error) {
	rows, err := q.Query(`SELECT m.assistant_id, a.name, a.status FROM group_members m
		JOIN assistants a ON a.id=m.assistant_id WHERE m.group_id=? ORDER BY m.added_at, m.assistant_id`, groupID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []GroupMember{}
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.AssistantID, &m.Name, &m.Status); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapBusy(rows.Err())
}

func getGroupTx(q queryer, id string) (Group, error) {
	var g Group
	err := q.QueryRow(`SELECT `+groupCols+`,COALESCE((SELECT MAX(seq) FROM group_messages WHERE group_id=chat_groups.id),0) FROM chat_groups WHERE id=?`, id).
		Scan(&g.ID, &g.Title, &g.Status, &g.Revision, &g.CreatedAt, &g.UpdatedAt, &g.LastSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, wrapBusy(err)
	}
	g.Members, err = loadMembers(q, id)
	return g, err
}

func memberNames(ms []GroupMember) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// CreateGroup records a Group of the person with at least two existing,
// active Assistants. A blank title becomes the member names.
func (s *Store) CreateGroup(requestID, title string, members []string) (Group, bool, error) {
	title = strings.TrimSpace(title)
	if len([]rune(title)) > maxGroupTitleLen {
		return Group{}, false, fmt.Errorf("%w: the title is too long", ErrInvalid)
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		ids = append(ids, m)
	}
	if len(ids) < 2 {
		return Group{}, false, fmt.Errorf("%w: a group needs at least two assistants", ErrInvalid)
	}
	if len(ids) > maxGroupMembers {
		return Group{}, false, fmt.Errorf("%w: a group holds at most %d assistants", ErrInvalid, maxGroupMembers)
	}
	payload := map[string]any{"title": title, "members": ids}
	return execute(s, requestID, "group.create", payload, func(tx *sql.Tx) (Group, error) {
		names := []string{}
		for _, id := range ids {
			a, err := getAssistantTx(tx, id)
			if err != nil {
				return Group{}, err
			}
			if a.Status != StatusActive {
				return Group{}, ErrAssistantDisabled
			}
			names = append(names, a.Name)
		}
		if title == "" {
			title = strings.Join(names, ", ")
			if r := []rune(title); len(r) > maxGroupTitleLen {
				title = string(r[:maxGroupTitleLen-1]) + "…"
			}
		}
		now := s.nowMS()
		gid := newID("g")
		if _, err := tx.Exec(`INSERT INTO chat_groups(`+groupCols+`) VALUES(?,?,?,?,?,?)`, gid, title, "active", 1, now, now); err != nil {
			return Group{}, err
		}
		for i, id := range ids {
			if _, err := tx.Exec(`INSERT INTO group_members(group_id,assistant_id,added_at) VALUES(?,?,?)`, gid, id, now+int64(i)); err != nil {
				return Group{}, err
			}
		}
		return getGroupTx(tx, gid)
	})
}

// GetGroup reads one Group.
func (s *Store) GetGroup(id string) (Group, error) { return getGroupTx(s.db, id) }

// ListGroups returns the active Groups, most recently active first (archived
// ones are ListArchivedGroups).
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id FROM chat_groups WHERE status='active' ORDER BY
		COALESCE((SELECT MAX(created_at) FROM group_messages WHERE group_id=chat_groups.id), created_at) DESC, id`)
	if err != nil {
		return nil, wrapBusy(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := []Group{}
	for _, id := range ids {
		g, err := getGroupTx(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// RenameGroup changes the title; revision guards a concurrent edit.
func (s *Store) RenameGroup(requestID, id, title string, expectedRevision int) (Group, bool, error) {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > maxGroupTitleLen {
		return Group{}, false, ErrInvalid
	}
	payload := map[string]any{"id": id, "title": title, "rev": expectedRevision}
	return execute(s, requestID, "group.rename", payload, func(tx *sql.Tx) (Group, error) {
		res, err := tx.Exec(`UPDATE chat_groups SET title=?, revision=revision+1, updated_at=? WHERE id=? AND revision=?`, title, s.nowMS(), id, expectedRevision)
		if err != nil {
			return Group{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := getGroupTx(tx, id); err != nil {
				return Group{}, err
			}
			return Group{}, ErrStaleRevision
		}
		return getGroupTx(tx, id)
	})
}

// AddGroupMember adds an active Assistant to a Group.
func (s *Store) AddGroupMember(requestID, groupID, assistantID string) (Group, bool, error) {
	payload := map[string]string{"group": groupID, "assistant": assistantID, "op": "add"}
	return execute(s, requestID, "group.member", payload, func(tx *sql.Tx) (Group, error) {
		g, err := getGroupTx(tx, groupID)
		if err != nil {
			return Group{}, err
		}
		a, err := getAssistantTx(tx, assistantID)
		if err != nil {
			return Group{}, err
		}
		if a.Status != StatusActive {
			return Group{}, ErrAssistantDisabled
		}
		for _, m := range g.Members {
			if m.AssistantID == assistantID {
				return g, nil
			}
		}
		if len(g.Members) >= maxGroupMembers {
			return Group{}, fmt.Errorf("%w: a group holds at most %d assistants", ErrInvalid, maxGroupMembers)
		}
		now := s.nowMS()
		if _, err := tx.Exec(`INSERT INTO group_members(group_id,assistant_id,added_at) VALUES(?,?,?)`, groupID, assistantID, now); err != nil {
			return Group{}, err
		}
		if _, err := tx.Exec(`UPDATE chat_groups SET revision=revision+1, updated_at=? WHERE id=?`, now, groupID); err != nil {
			return Group{}, err
		}
		if err := insertFact(tx, s, groupID, "member_added", assistantID, now); err != nil {
			return Group{}, err
		}
		return getGroupTx(tx, groupID)
	})
}

// RemoveGroupMember takes an Assistant out; a Group keeps at least two.
// Messages and tasks the member already produced stay in the log.
func (s *Store) RemoveGroupMember(requestID, groupID, assistantID string) (Group, bool, error) {
	payload := map[string]string{"group": groupID, "assistant": assistantID, "op": "remove"}
	return execute(s, requestID, "group.member", payload, func(tx *sql.Tx) (Group, error) {
		g, err := getGroupTx(tx, groupID)
		if err != nil {
			return Group{}, err
		}
		found := false
		for _, m := range g.Members {
			found = found || m.AssistantID == assistantID
		}
		if !found {
			return Group{}, ErrNotFound
		}
		if len(g.Members) <= 2 {
			return Group{}, fmt.Errorf("%w: a group keeps at least two assistants", ErrBadState)
		}
		now := s.nowMS()
		if _, err := tx.Exec(`DELETE FROM group_members WHERE group_id=? AND assistant_id=?`, groupID, assistantID); err != nil {
			return Group{}, err
		}
		// What was still waiting for the departed member is no longer deliverable.
		if _, err := tx.Exec(`UPDATE group_deliveries SET state=?, updated_at=? WHERE group_id=? AND assistant_id=? AND state IN (?,?)`,
			DeliverySkipped, now, groupID, assistantID, DeliveryQueued, DeliveryDelivering); err != nil {
			return Group{}, err
		}
		if _, err := tx.Exec(`UPDATE chat_groups SET revision=revision+1, updated_at=? WHERE id=?`, now, groupID); err != nil {
			return Group{}, err
		}
		if err := insertFact(tx, s, groupID, "member_removed", assistantID, now); err != nil {
			return Group{}, err
		}
		return getGroupTx(tx, groupID)
	})
}

// PostMessageInput is one message the person (or, through a Group Agent, a
// member) puts into a Group.
type PostMessageInput struct {
	GroupID    string
	AuthorKind string
	AuthorID   string
	Body       string
	Mentions   []string // member Assistant ids; each name must appear as @Name in the body
	ReplyTo    string
}

// PostResult is the stored message and the deliveries it queued.
type PostResult struct {
	Message    GroupMessage    `json:"message"`
	Deliveries []GroupDelivery `json:"deliveries"`
}

func insertMessage(tx *sql.Tx, now int64, m GroupMessage) (GroupMessage, error) {
	mentions, _ := json.Marshal(append([]string{}, m.Mentions...))
	fact := ""
	if len(m.Fact) > 0 {
		fact = string(m.Fact)
	}
	res, err := tx.Exec(`INSERT INTO group_messages(id,group_id,author_kind,author_id,kind,body,mentions,reply_to,task_id,fact,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.GroupID, m.AuthorKind, m.AuthorID, m.Kind, m.Body, string(mentions), m.ReplyTo, m.TaskID, fact, now)
	if err != nil {
		return m, err
	}
	m.Seq, _ = res.LastInsertId()
	m.CreatedAt = now
	if m.Mentions == nil {
		m.Mentions = []string{}
	}
	return m, nil
}

// insertFact writes a system receipt. The message id is derived from the
// subject, so the same event is recorded once however often the pump retries.
func insertFact(tx *sql.Tx, s *Store, groupID, event, assistantID string, now int64) error {
	return insertFactKeyed(tx, groupID, event, assistantID, "", newID("m"), now)
}

// groupFact writes one receipt outside a transaction (the Agent sync path);
// reason is short, human text the run reported and carries no conversation.
func (s *Store) groupFact(groupID, event, assistantID, reason, id string, now int64) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertFactReason(tx, groupID, event, assistantID, "", reason, id, now); err != nil {
		return
	}
	_ = tx.Commit()
}

func insertFactKeyed(tx *sql.Tx, groupID, event, assistantID, deliveryID, id string, now int64) error {
	return insertFactReason(tx, groupID, event, assistantID, deliveryID, "", id, now)
}

func insertFactReason(tx *sql.Tx, groupID, event, assistantID, deliveryID, reason, id string, now int64) error {
	if len(reason) > 300 {
		reason = reason[:300]
	}
	var name string
	_ = tx.QueryRow(`SELECT name FROM assistants WHERE id=?`, assistantID).Scan(&name) // a receipt keeps the name it was written under
	raw, _ := json.Marshal(map[string]string{"event": event, "assistantId": assistantID, "name": name, "deliveryId": deliveryID, "reason": reason})
	mentions, _ := json.Marshal([]string{})
	_, err := tx.Exec(`INSERT OR IGNORE INTO group_messages(id,group_id,author_kind,author_id,kind,body,mentions,reply_to,task_id,fact,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, groupID, AuthorSystem, "", MsgFact, "", string(mentions), "", "", string(raw), now)
	return err
}

// PostGroupMessage appends one message. A user message with explicit mentions
// targets those members; a message without mentions broadcasts to the group.
func (s *Store) PostGroupMessage(requestID string, in PostMessageInput) (PostResult, bool, error) {
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > maxGroupBody {
		return PostResult{}, false, fmt.Errorf("%w: a message needs 1–%d bytes of text", ErrInvalid, maxGroupBody)
	}
	switch in.AuthorKind {
	case AuthorUser:
		in.AuthorID = ""
	case AuthorAssistant:
		if in.AuthorID == "" {
			return PostResult{}, false, fmt.Errorf("%w: an assistant message needs its author", ErrInvalid)
		}
	default:
		return PostResult{}, false, fmt.Errorf("%w: author kind %q", ErrInvalid, in.AuthorKind)
	}
	payload := map[string]any{"in": in}
	return execute(s, requestID, "group.post", payload, func(tx *sql.Tx) (PostResult, error) {
		g, err := getGroupTx(tx, in.GroupID)
		if err != nil {
			return PostResult{}, err
		}
		if g.Status != "active" {
			return PostResult{}, ErrBadState
		}
		byID := map[string]GroupMember{}
		for _, m := range g.Members {
			byID[m.AssistantID] = m
		}
		if in.AuthorKind == AuthorAssistant {
			if _, ok := byID[in.AuthorID]; !ok {
				return PostResult{}, fmt.Errorf("%w: %s is not a member of this group", ErrInvalid, in.AuthorID)
			}
			// Only a Group Agent that really runs for this member may speak as it.
			ga, err := getGroupAgentTx(tx, in.GroupID, in.AuthorID)
			if err != nil {
				return PostResult{}, err
			}
			if !ga.Live() {
				return PostResult{}, fmt.Errorf("%w: no running agent of %s in this group", ErrBadState, byID[in.AuthorID].Name)
			}
		}
		mentions, err := checkMentions(in.Body, in.Mentions, g.Members)
		if err != nil {
			return PostResult{}, err
		}
		if in.ReplyTo != "" {
			var gid string
			if err := tx.QueryRow(`SELECT group_id FROM group_messages WHERE id=?`, in.ReplyTo).Scan(&gid); err != nil || gid != in.GroupID {
				return PostResult{}, fmt.Errorf("%w: the replied-to message is not in this group", ErrInvalid)
			}
		}
		now := s.nowMS()
		msg, err := insertMessage(tx, now, GroupMessage{
			ID: newID("m"), GroupID: in.GroupID, AuthorKind: in.AuthorKind, AuthorID: in.AuthorID, Kind: MsgText,
			Body: in.Body, Mentions: mentions, ReplyTo: in.ReplyTo,
		})
		if err != nil {
			return PostResult{}, err
		}
		out := PostResult{Message: msg, Deliveries: []GroupDelivery{}}
		if in.AuthorKind == AuthorUser {
			targets := mentions
			if len(targets) == 0 {
				for _, member := range g.Members {
					targets = append(targets, member.AssistantID)
				}
			}
			for _, id := range targets {
				m := byID[id]
				if m.Status != StatusActive {
					if err := insertFactKeyed(tx, in.GroupID, "assistant_disabled", id, "", newID("m"), now); err != nil {
						return PostResult{}, err
					}
					continue
				}
				var open int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM group_deliveries WHERE assistant_id=? AND state IN (?,?)`, id, DeliveryQueued, DeliveryDelivering).Scan(&open); err != nil {
					return PostResult{}, err
				}
				if open >= maxOpenDelivery {
					return PostResult{}, fmt.Errorf("%w: %s already has %d messages waiting", ErrBadState, m.Name, open)
				}
				d := GroupDelivery{ID: newID("d"), MessageID: msg.ID, AssistantID: id, State: DeliveryQueued}
				if _, err := tx.Exec(`INSERT INTO group_deliveries(id,group_id,message_id,assistant_id,state,attempts,next_at,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
					d.ID, in.GroupID, msg.ID, id, d.State, 0, 0, "", now, now); err != nil {
					return PostResult{}, err
				}
				out.Deliveries = append(out.Deliveries, d)
			}
		}
		if _, err := tx.Exec(`UPDATE chat_groups SET updated_at=? WHERE id=?`, now, in.GroupID); err != nil {
			return PostResult{}, err
		}
		return out, nil
	})
}

func scanGroupMessage(r rowScanner) (GroupMessage, error) {
	var m GroupMessage
	var mentions, fact string
	if err := r.Scan(&m.Seq, &m.ID, &m.GroupID, &m.AuthorKind, &m.AuthorID, &m.Kind, &m.Body, &mentions, &m.ReplyTo, &m.TaskID, &fact, &m.CreatedAt); err != nil {
		return m, err
	}
	_ = json.Unmarshal([]byte(mentions), &m.Mentions)
	if m.Mentions == nil {
		m.Mentions = []string{}
	}
	if fact != "" {
		m.Fact = json.RawMessage(fact)
	}
	return m, nil
}

const groupMsgCols = `seq,id,group_id,author_kind,author_id,kind,body,mentions,reply_to,task_id,fact,created_at`

// groupMsgColsM is groupMsgCols qualified for a join aliased `m`.
const groupMsgColsM = `m.seq,m.id,m.group_id,m.author_kind,m.author_id,m.kind,m.body,m.mentions,m.reply_to,m.task_id,m.fact,m.created_at`

// GroupMessagesAfter reads the log after a seq, oldest first, at most limit.
func groupMessagesAfter(q queryer, groupID string, after int64, limit int) ([]GroupMessage, error) {
	rows, err := q.Query(`SELECT `+groupMsgCols+` FROM group_messages WHERE group_id=? AND seq>? ORDER BY seq LIMIT ?`, groupID, after, limit)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []GroupMessage{}
	for rows.Next() {
		m, err := scanGroupMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapBusy(rows.Err())
}

// groupMessagesNewest is the last `limit` messages of one Group, oldest first.
func groupMessagesNewest(q queryer, groupID string, limit int) ([]GroupMessage, error) {
	rows, err := q.Query(`SELECT * FROM (SELECT `+groupMsgCols+` FROM group_messages WHERE group_id=? ORDER BY seq DESC LIMIT ?) ORDER BY seq`, groupID, limit)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []GroupMessage{}
	for rows.Next() {
		m, err := scanGroupMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapBusy(rows.Err())
}

// GroupFeedSince is the one read clients use. since=0 returns the newest
// `limit` messages (so a long Group opens at its end); a positive since returns
// what came after it.
func (s *Store) GroupFeedSince(groupID string, since int64, limit int) (GroupFeed, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	g, err := getGroupTx(s.db, groupID)
	if err != nil {
		return GroupFeed{}, err
	}
	feed := GroupFeed{Group: g, Seq: g.LastSeq, Deliveries: []GroupDelivery{}, Tasks: []GroupTaskCard{}, Agents: []GroupAgentState{}}
	if since <= 0 {
		// Message seq is global across Groups, so "the newest N of this Group"
		// is its own query, never LastSeq-N.
		feed.Messages, err = groupMessagesNewest(s.db, groupID, limit)
	} else {
		feed.Messages, err = groupMessagesAfter(s.db, groupID, since, limit)
	}
	if err != nil {
		return GroupFeed{}, err
	}
	// Deliveries and task cards for the messages that are in this page — only
	// theirs, whatever the Group's history holds beyond it.
	var rows *sql.Rows
	if len(feed.Messages) == 0 {
		rows, err = s.db.Query(`SELECT d.id,d.message_id,d.assistant_id,d.state,d.attempts FROM group_deliveries d WHERE 0`)
	} else {
		lo, hi := feed.Messages[0].Seq, feed.Messages[len(feed.Messages)-1].Seq
		rows, err = s.db.Query(`SELECT d.id,d.message_id,d.assistant_id,d.state,d.attempts FROM group_deliveries d
			JOIN group_messages m ON m.id=d.message_id WHERE d.group_id=? AND m.seq>=? AND m.seq<=? ORDER BY m.seq, d.assistant_id`, groupID, lo, hi)
	}
	if err != nil {
		return GroupFeed{}, wrapBusy(err)
	}
	for rows.Next() {
		var d GroupDelivery
		if err := rows.Scan(&d.ID, &d.MessageID, &d.AssistantID, &d.State, &d.Attempts); err != nil {
			_ = rows.Close()
			return GroupFeed{}, err
		}
		feed.Deliveries = append(feed.Deliveries, d)
	}
	_ = rows.Close()
	for _, m := range feed.Messages {
		if m.TaskID == "" {
			continue
		}
		t, err := getTaskTx(s.db, m.TaskID)
		if err != nil {
			continue
		}
		feed.Tasks = append(feed.Tasks, GroupTaskCard{TaskID: t.ID, AssistantID: t.AssistantID, Title: t.Title, State: t.State, NeedsYou: t.NeedsYou, UpdatedAt: t.UpdatedAt})
	}
	for _, m := range g.Members {
		ga, err := getGroupAgentTx(s.db, groupID, m.AssistantID)
		if err != nil {
			return GroupFeed{}, err
		}
		feed.Agents = append(feed.Agents, GroupAgentState{AssistantID: m.AssistantID, State: ga.State, PaneID: ga.PaneID})
	}
	return feed, nil
}

// linkGroupTask records, in the same transaction that created the task, a task
// card in the Group the task was filed from. The filing Assistant must be a
// member with a running Group Agent: a card is never decoration.
func linkGroupTask(tx *sql.Tx, s *Store, groupID string, t Task, now int64) error {
	g, err := getGroupTx(tx, groupID)
	if err != nil {
		return err
	}
	member := false
	for _, m := range g.Members {
		member = member || m.AssistantID == t.AssistantID
	}
	if !member {
		return fmt.Errorf("%w: %s is not a member of group %s", ErrInvalid, t.AssistantID, groupID)
	}
	ga, err := getGroupAgentTx(tx, groupID, t.AssistantID)
	if err != nil {
		return err
	}
	if !ga.Live() {
		return fmt.Errorf("%w: no running agent of this assistant in the group", ErrBadState)
	}
	_, err = insertMessage(tx, now, GroupMessage{
		ID: newID("m"), GroupID: groupID, AuthorKind: AuthorAssistant, AuthorID: t.AssistantID, Kind: MsgTask,
		Body: t.Title, TaskID: t.ID,
	})
	return err
}

// checkMentions is the authority on who a message may wake. The client says
// which member ids it means; the body must back each one with an `@Name` that
// really stands in the text:
//
//   - the name must not be the head of a longer member name or word (`@Bobby`
//     never reads as `@Bo`, and `@Bob` followed by letters is no mention);
//   - members that share a name are told apart only by what the person picked,
//     so a message may wake no more of them than it has `@Name` occurrences
//     (one `@Ada` with two Adas' ids is refused, never delivered twice).
func checkMentions(body string, ids []string, members []GroupMember) ([]string, error) {
	byID := map[string]GroupMember{}
	for _, m := range members {
		byID[m.AssistantID] = m
	}
	occurrences := mentionOccurrences(body, members)
	out := []string{}
	seen := map[string]bool{}
	picked := map[string]int{}
	for _, id := range ids {
		m, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: %s is not a member of this group", ErrInvalid, id)
		}
		if seen[id] {
			continue
		}
		picked[m.Name]++
		if picked[m.Name] > occurrences[m.Name] {
			if occurrences[m.Name] == 0 {
				return nil, fmt.Errorf("%w: the message does not say @%s", ErrInvalid, m.Name)
			}
			return nil, fmt.Errorf("%w: more members called %s were picked than the message names", ErrInvalid, m.Name)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// mentionOccurrences counts, per member name, the `@Name` tokens in body. Each
// `@` belongs to the longest member name that follows it, and an ASCII-word
// name must end where the word ends (CJK names carry no spaces, so they only
// need the longest-match rule).
func mentionOccurrences(body string, members []GroupMember) map[string]int {
	out := map[string]int{}
	for i := 0; i < len(body); i++ {
		if body[i] != '@' {
			continue
		}
		if i > 0 {
			if r, _ := utf8.DecodeLastRuneInString(body[:i]); (isMentionWordRune(r) && r < utf8.RuneSelf) || r == '@' {
				continue
			}
		}
		rest := body[i+1:]
		best := ""
		for _, m := range members {
			if m.Name == "" || !strings.HasPrefix(rest, m.Name) || len(m.Name) <= len(best) {
				continue
			}
			if last, _ := utf8.DecodeLastRuneInString(m.Name); isMentionWordRune(last) && last < utf8.RuneSelf {
				if next, size := utf8.DecodeRuneInString(rest[len(m.Name):]); size > 0 && isMentionWordRune(next) && next < utf8.RuneSelf {
					continue
				}
			}
			best = m.Name
		}
		if best != "" {
			out[best]++
			i += len(best)
		}
	}
	return out
}

func isMentionWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
