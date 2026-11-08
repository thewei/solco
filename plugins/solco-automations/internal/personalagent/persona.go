package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Assistant persona (3.1 U2): the person's "how you work" text, versioned, and
// the Assistant's own Agent/Model/Effort. The active text stays in
// assistants.role_prompt (the one authority every launch already reads);
// assistant_instructions is only its history, so there is no second source.

const (
	// MaxInstructionsBytes bounds one instructions text.
	MaxInstructionsBytes = 8 << 10

	SourceDefault = "default"
	SourceUser    = "user"
	SourceRestore = "restore"
)

// DefaultInstructions is the working style every new Assistant starts with:
// built from its name and the person's one line of mission, never a blank.
func DefaultInstructions(name, description string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, a personal assistant of the person you talk to.", strings.TrimSpace(name))
	if d := strings.TrimSpace(description); d != "" {
		fmt.Fprintf(&b, " Your job: %s.", strings.TrimRight(d, ".。 "))
	}
	b.WriteString(" Work style: answer simple questions directly and briefly; for real work, break the goal into tasks and keep them moving; report what actually happened, with evidence, and say so plainly when something is blocked. Ask before anything irreversible or outward-facing. Reply in the language the person writes in.")
	return b.String()
}

// InstructionRevision is one stored version.
type InstructionRevision struct {
	Revision  int    `json:"revision"`
	Source    string `json:"source"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

// Instructions is an Assistant's working style: the active text, its history and
// which revision each running surface launched with (a text edited since is
// "next session": nothing already running is rewritten).
type Instructions struct {
	AssistantID string                `json:"assistantId"`
	Revision    int                   `json:"revision"`
	Body        string                `json:"body"`
	History     []InstructionRevision `json:"history"`
	Applied     []AppliedRevision     `json:"applied"`
}

// AppliedRevision says which instructions revision a surface (the Coordinator or
// one Group's Agent) was launched with.
type AppliedRevision struct {
	Surface   string `json:"surface"`
	Revision  int    `json:"revision"`
	AppliedAt int64  `json:"appliedAt"`
}

func validInstructions(body string) error {
	if strings.TrimSpace(body) == "" || len(body) > MaxInstructionsBytes || !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return fmt.Errorf("%w: instructions must be 1 to %d bytes of text", ErrInvalid, MaxInstructionsBytes)
	}
	return nil
}

// ensureInstructionsTx materialises revision 1 for an Assistant that has none
// (one created before U2): its current role text, or the default.
func ensureInstructionsTx(tx *sql.Tx, now int64, a Assistant) (int, string, error) {
	var rev int
	err := tx.QueryRow(`SELECT COALESCE(MAX(revision),0) FROM assistant_instructions WHERE assistant_id=?`, a.ID).Scan(&rev)
	if err != nil {
		return 0, "", err
	}
	if rev > 0 {
		var body string
		if err := tx.QueryRow(`SELECT body FROM assistant_instructions WHERE assistant_id=? AND revision=?`, a.ID, rev).Scan(&body); err != nil {
			return 0, "", err
		}
		return rev, body, nil
	}
	body, source := strings.TrimSpace(a.RolePrompt), SourceUser
	if body == "" {
		body, source = DefaultInstructions(a.Name, a.Description), SourceDefault
		if _, err := tx.Exec(`UPDATE assistants SET role_prompt=? WHERE id=?`, body, a.ID); err != nil {
			return 0, "", err
		}
	}
	if _, err := tx.Exec(`INSERT INTO assistant_instructions(assistant_id,revision,body,source,created_at) VALUES(?,?,?,?,?)`, a.ID, 1, body, source, now); err != nil {
		return 0, "", err
	}
	return 1, body, nil
}

// GetInstructions reads the working style (materialising revision 1 once).
func (s *Store) GetInstructions(assistantID string) (Instructions, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Instructions{}, wrapBusy(err)
	}
	defer func() { _ = tx.Rollback() }()
	a, err := getAssistantTx(tx, assistantID)
	if err != nil {
		return Instructions{}, err
	}
	rev, body, err := ensureInstructionsTx(tx, s.nowMS(), a)
	if err != nil {
		return Instructions{}, wrapBusy(err)
	}
	out := Instructions{AssistantID: assistantID, Revision: rev, Body: body, History: []InstructionRevision{}, Applied: []AppliedRevision{}}
	rows, err := tx.Query(`SELECT revision,source,body,created_at FROM assistant_instructions WHERE assistant_id=? ORDER BY revision DESC`, assistantID)
	if err != nil {
		return Instructions{}, wrapBusy(err)
	}
	for rows.Next() {
		var r InstructionRevision
		if err := rows.Scan(&r.Revision, &r.Source, &r.Body, &r.CreatedAt); err != nil {
			_ = rows.Close()
			return Instructions{}, err
		}
		out.History = append(out.History, r)
	}
	_ = rows.Close()
	arows, err := tx.Query(`SELECT surface,revision,applied_at FROM persona_applied WHERE assistant_id=? ORDER BY surface`, assistantID)
	if err != nil {
		return Instructions{}, wrapBusy(err)
	}
	for arows.Next() {
		var r AppliedRevision
		if err := arows.Scan(&r.Surface, &r.Revision, &r.AppliedAt); err != nil {
			_ = arows.Close()
			return Instructions{}, err
		}
		out.Applied = append(out.Applied, r)
	}
	_ = arows.Close()
	if err := tx.Commit(); err != nil {
		return Instructions{}, wrapBusy(err)
	}
	return out, nil
}

// SetInstructions saves a new revision (the old text stays in history) and makes
// it the active role text. expectedRevision is the revision the person edited.
func (s *Store) SetInstructions(requestID, assistantID, body string, expectedRevision int) (Instructions, bool, error) {
	body = strings.TrimSpace(body)
	if err := validInstructions(body); err != nil {
		return Instructions{}, false, err
	}
	return s.writeInstructions(requestID, "assistant.instructions.set", assistantID, body, SourceUser, expectedRevision, 0)
}

// RestoreInstructions makes an earlier revision's text current again — as a new
// revision, so the history only ever grows.
func (s *Store) RestoreInstructions(requestID, assistantID string, revision, expectedRevision int) (Instructions, bool, error) {
	if revision < 1 {
		return Instructions{}, false, fmt.Errorf("%w: revision", ErrInvalid)
	}
	return s.writeInstructions(requestID, "assistant.instructions.restore", assistantID, "", SourceRestore, expectedRevision, revision)
}

func (s *Store) writeInstructions(requestID, kind, assistantID, body, source string, expected, from int) (Instructions, bool, error) {
	payload := map[string]any{"a": assistantID, "body": body, "rev": expected, "from": from}
	var replayed bool
	_, replayed, err := execute(s, requestID, kind, payload, func(tx *sql.Tx) (int, error) {
		a, err := getAssistantTx(tx, assistantID)
		if err != nil {
			return 0, err
		}
		now := s.nowMS()
		cur, _, err := ensureInstructionsTx(tx, now, a)
		if err != nil {
			return 0, err
		}
		if expected != cur {
			return 0, ErrStaleRevision
		}
		text := body
		if from > 0 {
			if err := tx.QueryRow(`SELECT body FROM assistant_instructions WHERE assistant_id=? AND revision=?`, assistantID, from).Scan(&text); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return 0, ErrNotFound
				}
				return 0, err
			}
		}
		next := cur + 1
		if _, err := tx.Exec(`INSERT INTO assistant_instructions(assistant_id,revision,body,source,created_at) VALUES(?,?,?,?,?)`, assistantID, next, text, source, now); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE assistants SET role_prompt=?, revision=revision+1, updated_at=? WHERE id=?`, text, now, assistantID); err != nil {
			return 0, err
		}
		return next, nil
	})
	if err != nil {
		return Instructions{}, false, err
	}
	out, err := s.GetInstructions(assistantID)
	return out, replayed, err
}

// markInstructionsApplied records the revision a surface launched with.
func (s *Store) markInstructionsApplied(assistantID, surface string) {
	var rev int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(revision),0) FROM assistant_instructions WHERE assistant_id=?`, assistantID).Scan(&rev); err != nil || rev == 0 {
		return
	}
	_, _ = s.db.Exec(`INSERT INTO persona_applied(assistant_id,surface,revision,applied_at) VALUES(?,?,?,?)
		ON CONFLICT(assistant_id,surface) DO UPDATE SET revision=excluded.revision, applied_at=excluded.applied_at`, assistantID, surface, rev, s.nowMS())
}

// --- runtime profile -------------------------------------------------------

// effortKinds are the Agents whose launch command takes a thinking effort.
var effortKinds = map[string]string{"claude": "--effort"}

// RuntimeProfile is the Agent an Assistant's own conversation and work run on by
// default. Empty fields mean the product default.
type RuntimeProfile struct {
	AssistantID string `json:"assistantId"`
	Agent       string `json:"agent"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	// EffortSupported says whether this Agent's launch takes the effort at all.
	EffortSupported bool `json:"effortSupported"`
	// IdleSleepMin is how many quiet minutes put this Assistant's Coordinator to
	// sleep (0 = never); the default when the person never chose is the daemon's
	// (DefaultIdleSleep, or SOLCO_ASSISTANTS_IDLE_SLEEP_MIN).
	IdleSleepMin int   `json:"idleSleepMin"`
	Revision     int   `json:"revision"`
	UpdatedAt    int64 `json:"updatedAt"`
}

// idleSleepChoices are the minutes a person can pick (0 = never).
var idleSleepChoices = map[int]bool{0: true, 15: true, 30: true, 60: true}

// RunningAgent is a real Agent an Assistant has right now — what actually runs,
// which may differ from the profile (a profile change never restarts one).
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

func withSupport(p RuntimeProfile) RuntimeProfile {
	agent := p.Agent
	if agent == "" {
		agent = DefaultCoordinatorAgent
	}
	_, p.EffortSupported = effortKinds[agent]
	return p
}

func (s *Store) profileOf(q interface {
	QueryRow(string, ...any) *sql.Row
}, assistantID string) RuntimeProfile {
	p := RuntimeProfile{AssistantID: assistantID, Revision: 0}
	var idle sql.NullInt64
	err := q.QueryRow(`SELECT agent_kind,model,effort,revision,updated_at,idle_sleep_min FROM assistant_runtime WHERE assistant_id=?`, assistantID).
		Scan(&p.Agent, &p.Model, &p.Effort, &p.Revision, &p.UpdatedAt, &idle)
	if err != nil {
		p = RuntimeProfile{AssistantID: assistantID}
		idle = sql.NullInt64{}
	}
	p.IdleSleepMin = int(IdleSleepAfter() / time.Minute)
	if idle.Valid {
		p.IdleSleepMin = int(idle.Int64)
	}
	return withSupport(p)
}

// GetRuntime reads the profile and the Agents that really run for the Assistant.
func (s *Store) GetRuntime(assistantID string) (Runtime, error) {
	if _, err := s.GetAssistant(assistantID); err != nil {
		return Runtime{}, err
	}
	out := Runtime{Profile: s.profileOf(s.db, assistantID), Running: []RunningAgent{}}
	rows, err := s.db.Query(`SELECT t.id,t.kind,t.agent,t.model,t.state,COALESCE(r.pane_id,''),COALESCE(g.group_id,'')
		FROM tasks t
		LEFT JOIN runs r ON r.id=(SELECT id FROM runs WHERE task_id=t.id ORDER BY rowid DESC LIMIT 1)
		LEFT JOIN group_agents g ON g.task_id=t.id
		WHERE t.assistant_id=? AND t.state IN ('claimed','starting','running','blocked') ORDER BY t.created_at`, assistantID)
	if err != nil {
		return Runtime{}, wrapBusy(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a RunningAgent
		var kind, group string
		if err := rows.Scan(&a.TaskID, &kind, &a.Agent, &a.Model, &a.State, &a.PaneID, &group); err != nil {
			return Runtime{}, err
		}
		switch kind {
		case KindCoordinator:
			a.Surface = "coordinator"
		case KindGroupAgent:
			a.Surface = "group:" + group
		default:
			a.Surface = "task"
		}
		out.Running = append(out.Running, a)
	}
	return out, rows.Err()
}

// SetRuntime saves the profile. A running Agent is never restarted by it: the
// change applies to the next start. An optional last argument sets the idle-sleep
// minutes (0, 15, 30 or 60); left out, the Assistant's earlier choice stays.
func (s *Store) SetRuntime(requestID, assistantID, agent, model, effort string, expectedRevision int, idleSleepMin ...int) (Runtime, bool, error) {
	agent, model, effort = strings.TrimSpace(agent), strings.TrimSpace(model), strings.TrimSpace(effort)
	if agent != "" && !coordinatorAgentRe.MatchString(agent) {
		return Runtime{}, false, fmt.Errorf("%w: agent %q is not a Herdr agent kind", ErrInvalid, agent)
	}
	if len(model) > 120 || strings.ContainsAny(model, "\n\r\x00") {
		return Runtime{}, false, fmt.Errorf("%w: invalid model", ErrInvalid)
	}
	if len(effort) > 24 || strings.ContainsAny(effort, " \n\r\x00") {
		return Runtime{}, false, fmt.Errorf("%w: invalid effort", ErrInvalid)
	}
	idle := sql.NullInt64{}
	if len(idleSleepMin) > 0 {
		if !idleSleepChoices[idleSleepMin[0]] {
			return Runtime{}, false, fmt.Errorf("%w: idle sleep must be 0, 15, 30 or 60 minutes", ErrInvalid)
		}
		idle = sql.NullInt64{Int64: int64(idleSleepMin[0]), Valid: true}
	}
	payload := map[string]any{"a": assistantID, "agent": agent, "model": model, "effort": effort, "rev": expectedRevision}
	if idle.Valid {
		payload["idle"] = idle.Int64
	}
	_, replayed, err := execute(s, requestID, "assistant.runtime.set", payload, func(tx *sql.Tx) (int, error) {
		if _, err := getAssistantTx(tx, assistantID); err != nil {
			return 0, err
		}
		cur := s.profileOf(tx, assistantID)
		if expectedRevision != cur.Revision {
			return 0, ErrStaleRevision
		}
		now := s.nowMS()
		if _, err := tx.Exec(`INSERT INTO assistant_runtime(assistant_id,agent_kind,model,effort,revision,updated_at,idle_sleep_min) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(assistant_id) DO UPDATE SET agent_kind=excluded.agent_kind, model=excluded.model, effort=excluded.effort,
			idle_sleep_min=COALESCE(excluded.idle_sleep_min, assistant_runtime.idle_sleep_min),
			revision=assistant_runtime.revision+1, updated_at=excluded.updated_at`,
			assistantID, agent, model, effort, 1, now, idle); err != nil {
			return 0, err
		}
		return cur.Revision + 1, nil
	})
	if err != nil {
		return Runtime{}, false, err
	}
	out, err := s.GetRuntime(assistantID)
	return out, replayed, err
}

// effortArgs is the launch argument carrying the profile's effort, or none when
// the Agent has no such flag.
func effortArgs(agent, effort string) []string {
	if effort == "" {
		return nil
	}
	if agent == "" {
		agent = DefaultCoordinatorAgent
	}
	if flag, ok := effortKinds[agent]; ok {
		return []string{flag, effort}
	}
	return nil
}

// runtimeDefaults fills an unnamed Agent from the Assistant's profile (and the
// model only when the Agent was not named, so a named Agent never inherits
// another Agent's model). An explicit choice always wins.
func (s *Store) runtimeDefaults(assistantID, agent, model string) (string, string) {
	if agent == "" {
		p := s.profileOf(s.db, assistantID)
		agent = p.Agent
		if model == "" {
			model = p.Model
		}
	}
	if agent == "" {
		agent = DefaultCoordinatorAgent
	}
	return agent, model
}
