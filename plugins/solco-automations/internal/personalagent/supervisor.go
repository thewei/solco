package personalagent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Fact kinds: the ledger's vocabulary. A Fact is a sentence the store itself
// wrote about committed state (ids, titles, counts) — never an Agent's words
// and never a run's output.
const (
	FactAccepted  = "accepted"  // the Goal was approved and is now being pursued
	FactPlanned   = "planned"   // a plan exists
	FactReplanned = "replanned" // the plan changed
	FactDelegated = "delegated" // a milestone was handed to a task
	FactVerifying = "verifying" // a task finished and awaits review
	FactVerified  = "verified"  // a milestone's task is done
	FactBlocked   = "blocked"   // a task failed, blocked or needs a person
	FactReady     = "ready"     // everything automatic is verified; a person must confirm the rest
	FactDone      = "done"      // the Goal completed, with its evidence
	FactStale     = "stale"     // nothing moved for a while
	FactHeld      = "held"      // a budget stopped the supervisor
)

// Fact is one ledger entry.
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

const factCols = `rowid,id,assistant_id,goal_id,task_id,kind,text,evidence,created_at`

// addFactTx writes a fact once per dedupe key; it reports whether it was new.
func addFactTx(tx *sql.Tx, f Fact, dedupe string, now int64) (bool, error) {
	if f.ID == "" {
		f.ID = newID("f")
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO goal_facts(id,assistant_id,goal_id,task_id,kind,text,evidence,dedupe_key,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		f.ID, f.AssistantID, f.GoalID, f.TaskID, f.Kind, clip(f.Text, 300), clip(f.Evidence, 1200), dedupe, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Facts lists the ledger newest first, for an Assistant, a Goal, or both.
func (s *Store) Facts(assistantID, goalID string, limit int) ([]Fact, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q, args := `SELECT `+factCols+` FROM goal_facts WHERE 1=1`, []any{}
	if assistantID != "" {
		q += ` AND assistant_id=?`
		args = append(args, assistantID)
	}
	if goalID != "" {
		q += ` AND goal_id=?`
		args = append(args, goalID)
	}
	rows, err := s.db.Query(q+` ORDER BY rowid DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Fact{}
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.Seq, &f.ID, &f.AssistantID, &f.GoalID, &f.TaskID, &f.Kind, &f.Text, &f.Evidence, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, wrapBusy(rows.Err())
}

// Supervisor is the deterministic Goal driver (§5.3): each Tick it derives
// facts from the event log, delegates milestones whose dependencies are
// verified (within a task budget and a fan-out limit), verifies milestones
// from task state, completes a Goal only when every milestone is machine-
// verified, and says so when a Goal goes quiet. It never runs a model and
// every step is idempotent, so a restart or a repeated Tick changes nothing.
type Supervisor struct {
	Store *Store
	// MaxFanOut bounds the open tasks one Goal may have at once (default 3).
	MaxFanOut int
}

// SupervisorReport counts what one Tick did.
type SupervisorReport struct {
	Facts, Dispatched, Completed int
}

const supervisorMark = "supervisor_event_mark"

func (sv *Supervisor) fanOut() int {
	if sv.MaxFanOut > 0 {
		return sv.MaxFanOut
	}
	return DefaultFanOut
}

func (s *Store) newFact(f Fact, dedupe string) (bool, error) {
	var added bool
	_, _, err := execute(s, "", "fact", nil, func(tx *sql.Tx) (struct{}, error) {
		var err error
		added, err = addFactTx(tx, f, dedupe, s.nowMS())
		return struct{}{}, err
	})
	return added, err
}

// Tick makes one pass. Errors on one Goal do not stop the others.
func (sv *Supervisor) Tick() (SupervisorReport, error) {
	var rep SupervisorReport
	n, err := sv.eventFacts()
	rep.Facts += n
	if err != nil {
		return rep, err
	}
	rows, err := sv.Store.db.Query(`SELECT p.goal_id FROM goal_plans p JOIN goals g ON g.id=p.goal_id WHERE g.state=? ORDER BY g.created_at, g.id`, GoalActive)
	if err != nil {
		return rep, wrapBusy(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return rep, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var first error
	for _, id := range ids {
		if err := sv.step(id, &rep); err != nil && first == nil {
			first = fmt.Errorf("goal %s: %w", id, err)
		}
	}
	return rep, first
}

var eventFactKind = map[string]string{
	"task.blocked":   FactBlocked,
	"task.uncertain": FactBlocked,
	"task.failed":    FactBlocked,
	"review.ready":   FactVerifying,
	"review.failed":  FactBlocked,
}

// eventFacts turns the task/review events of Goal-bound tasks into facts,
// advancing a durable watermark so a restart resumes where it stopped.
func (sv *Supervisor) eventFacts() (int, error) {
	s := sv.Store
	var mark int64
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, supervisorMark).Scan(&raw); err == nil {
		mark, _ = strconv.ParseInt(raw, 10, 64)
	}
	rows, err := s.db.Query(`SELECT e.rowid,e.id,e.assistant_id,e.kind,e.task_id,e.detail,t.goal_id,t.rule_id FROM events e JOIN tasks t ON t.id=e.task_id
		WHERE e.rowid>? AND e.task_id<>'' ORDER BY e.rowid LIMIT 500`, mark)
	if err != nil {
		return 0, wrapBusy(err)
	}
	type ev struct {
		seq                                           int64
		id, assistant, kind, task, detail, goal, rule string
	}
	var evs []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.seq, &e.id, &e.assistant, &e.kind, &e.task, &e.detail, &e.goal, &e.rule); err != nil {
			rows.Close()
			return 0, err
		}
		evs = append(evs, e)
	}
	rows.Close()
	if len(evs) == 0 {
		return 0, nil
	}
	added := 0
	_, _, err = execute(s, "", "supervisor.events", nil, func(tx *sql.Tx) (struct{}, error) {
		for _, e := range evs {
			if name, isRule := LegacyRuleName(e.rule); isRule && e.goal == "" {
				// A scheduled run of an enabled Routine: say what happened to its owner.
				if owner, owned := routineOwnerTx(tx, name); owned && owner == e.assistant {
					if fk, text, ok := routineEventFact(e.kind, name); ok {
						isNew, err := addFactTx(tx, Fact{AssistantID: e.assistant, TaskID: e.task, Kind: fk, Text: text}, "ev:"+e.id, s.nowMS())
						if err != nil {
							return struct{}{}, err
						}
						if isNew {
							added++
						}
					}
				}
				continue
			}
			kind, ok := eventFactKind[e.kind]
			if !ok || e.goal == "" {
				continue
			}
			var active string
			if err := tx.QueryRow(`SELECT state FROM goals WHERE id=?`, e.goal).Scan(&active); err != nil || active != GoalActive {
				continue
			}
			isNew, err := addFactTx(tx, Fact{AssistantID: e.assistant, GoalID: e.goal, TaskID: e.task, Kind: kind, Text: e.detail}, "ev:"+e.id, s.nowMS())
			if err != nil {
				return struct{}{}, err
			}
			if isNew {
				added++
			}
		}
		_, err := tx.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, supervisorMark, strconv.FormatInt(evs[len(evs)-1].seq, 10))
		return struct{}{}, err
	})
	return added, err
}

func (sv *Supervisor) step(goalID string, rep *SupervisorReport) error {
	s := sv.Store
	plan, err := s.GetGoalPlan(goalID)
	if err != nil {
		return err
	}
	g, err := s.GetGoal(goalID)
	if err != nil {
		return err
	}
	// 1. Verified milestones leave a fact (once per milestone and task).
	for _, m := range plan.Milestones {
		if m.State == MilestoneVerified && m.TaskID != "" {
			if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: goalID, TaskID: m.TaskID, Kind: FactVerified,
				Text: fmt.Sprintf("Verified: %s", m.Title), Evidence: m.Evidence}, "verified:"+goalID+":"+m.ID+":"+m.TaskID); err != nil {
				return err
			} else if added {
				rep.Facts++
			}
		}
	}
	// 2. Completion is earned: every milestone delegated and verified from task state.
	// "Its task finished" proves the work ran, not that the Goal's success
	// criteria hold: only a merged coding task is machine-verified, so a plan
	// with any other milestone is handed to the person for the final check.
	if plan.Total > 0 && plan.Verified == plan.Total && allDelegated(plan) && !planMachineVerified(plan) {
		if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: goalID, Kind: FactReady,
			Text:     fmt.Sprintf("Ready for you: every milestone of %q finished its work; confirm it meets the success criteria", g.Title),
			Evidence: completionEvidence(plan)}, fmt.Sprintf("ready:%s:%d:all", goalID, plan.PlanRevision)); err != nil {
			return err
		} else if added {
			rep.Facts++
		}
		return nil
	}
	if plan.Total > 0 && plan.Verified == plan.Total && allDelegated(plan) {
		ev := completionEvidence(plan)
		done, _, err := s.CompleteGoal(fmt.Sprintf("sup-done-%s-%d", goalID, g.Revision), goalID, g.Revision, ev)
		if err != nil {
			return err
		}
		if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: goalID, Kind: FactDone,
			Text: fmt.Sprintf("Done: %s — %d of %d milestone(s) verified", g.Title, plan.Verified, plan.Total), Evidence: done.Evidence}, "done:"+goalID); err != nil {
			return err
		} else if added {
			rep.Facts++
		}
		rep.Completed++
		return nil
	}
	// 3. Only a person can confirm the manual milestones.
	if plan.State == "waiting_user" && autoVerified(plan) {
		if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: goalID, Kind: FactReady,
			Text:     fmt.Sprintf("Ready for you: every automatic milestone of %q is verified; %d need your check", g.Title, countState(plan, MilestoneNeedsYou)),
			Evidence: completionEvidence(plan)}, fmt.Sprintf("ready:%s:%d", goalID, plan.PlanRevision)); err != nil {
			return err
		} else if added {
			rep.Facts++
		}
	}
	// 4. Delegate what is ready, inside the budgets.
	if err := sv.dispatch(g, plan, rep); err != nil {
		return err
	}
	// 5. Say so when nothing has moved.
	return sv.stale(g, plan, rep)
}

func allDelegated(p GoalPlan) bool {
	for _, m := range p.Milestones {
		if m.Task == nil {
			return false
		}
	}
	return true
}

func autoVerified(p GoalPlan) bool {
	for _, m := range p.Milestones {
		if m.Task != nil && m.State != MilestoneVerified {
			return false
		}
	}
	return true
}

func countState(p GoalPlan, st string) int {
	n := 0
	for _, m := range p.Milestones {
		if m.State == st {
			n++
		}
	}
	return n
}

func completionEvidence(p GoalPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan rev %d, %d of %d milestone(s) verified from task state.", p.PlanRevision, p.Verified, p.Total)
	for _, m := range p.Milestones {
		if m.Evidence != "" {
			fmt.Fprintf(&b, "\n- %s: %s", m.Title, m.Evidence)
		} else {
			fmt.Fprintf(&b, "\n- %s: %s", m.Title, m.State)
		}
	}
	return b.String()
}

func (sv *Supervisor) openTasks(goalID string) (open, total int, err error) {
	rows, err := sv.Store.db.Query(`SELECT state FROM tasks WHERE goal_id=? AND kind NOT IN (?,?)`, goalID, KindCoordinator, KindGroupAgent)
	if err != nil {
		return 0, 0, wrapBusy(err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return 0, 0, err
		}
		total++
		if taskOpen(st) {
			open++
		}
	}
	return open, total, wrapBusy(rows.Err())
}

func (sv *Supervisor) dispatch(g Goal, plan GoalPlan, rep *SupervisorReport) error {
	s := sv.Store
	verified := map[string]bool{}
	for _, m := range plan.Milestones {
		verified[m.ID] = m.State == MilestoneVerified
	}
	open, total, err := sv.openTasks(g.ID)
	if err != nil {
		return err
	}
	pol, err := s.GetProactivity(g.AssistantID)
	if err != nil {
		return err
	}
	for _, m := range plan.Milestones {
		if m.State != MilestonePending || m.Task == nil || m.TaskID != "" {
			continue
		}
		ready := true
		for _, d := range m.DependsOn {
			ready = ready && verified[d]
		}
		if !ready {
			continue
		}
		// The Assistant's own rules come first: they decide whether the system
		// may start anything unasked right now.
		switch {
		case pol.Level == LevelRespond:
			return nil
		case pol.Level == LevelSuggest:
			if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: g.ID, Kind: FactHeld,
				Text: fmt.Sprintf("Next up for %q: %s. Nothing starts on its own while this Assistant is set to Suggest; raise it to Follow up to let it go", g.Title, m.Title)},
				fmt.Sprintf("policy:%s:%d:%s", g.ID, plan.PlanRevision, m.ID)); err != nil {
				return err
			} else if added {
				rep.Facts++
			}
			continue
		case pol.QuietNow:
			return nil // quiet hours: resumes by itself afterwards, nothing to say
		case pol.DispatchToday >= pol.DailyBudget:
			if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: g.ID, Kind: FactHeld,
				Text: fmt.Sprintf("Held: today's budget of %d task(s) started on their own is used up; %q continues tomorrow or when you raise it", pol.DailyBudget, g.Title)},
				fmt.Sprintf("held:%s:day:%s", g.ID, pol.day)); err != nil {
				return err
			} else if added {
				rep.Facts++
			}
			return nil
		}
		if total >= plan.MaxTasks {
			if added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: g.ID, Kind: FactHeld,
				Text: fmt.Sprintf("Held: %q reached its budget of %d task(s); a person must raise it or revise the plan", g.Title, plan.MaxTasks)},
				fmt.Sprintf("held:%s:tasks:%d", g.ID, plan.PlanRevision)); err != nil {
				return err
			} else if added {
				rep.Facts++
			}
			return nil
		}
		if open >= sv.fanOut() {
			return nil // wait for a slot; nothing to report
		}
		t := m.Task
		task, _, err := s.CreateTask(fmt.Sprintf("sup-%s-%s-%d", g.ID, m.ID, m.Attempt), CreateTaskInput{
			AssistantID: g.AssistantID, GoalID: g.ID, Kind: t.Kind, Title: t.Title, Brief: t.Brief,
			RepoPath: t.RepoPath, Agent: t.Agent, Model: t.Model,
			// A coding task waits for a person to publish it, like every other
			// coding task (the Coordinator's own rule); research and manual run.
			Draft: t.Kind == KindCoding,
		})
		if err != nil {
			return err
		}
		if err := s.bindMilestone(g.ID, plan.PlanRevision, m.ID, task); err != nil {
			return err
		}
		if err := s.countDispatch(g.AssistantID, 1); err != nil {
			return err
		}
		pol.DispatchToday++
		open++
		total++
		rep.Dispatched++
		rep.Facts++
	}
	return nil
}

// bindMilestone records the task a milestone was delegated to and its fact in
// one transaction. A plan that changed meanwhile is left for the next Tick
// (the task's receipt makes the retry find the same task).
func (s *Store) bindMilestone(goalID string, planRev int, msID string, task Task) error {
	_, _, err := execute(s, "", "supervisor.bind", nil, func(tx *sql.Tx) (struct{}, error) {
		p, err := getPlanRowTx(tx, goalID)
		if err != nil {
			return struct{}{}, err
		}
		if p.rev != planRev {
			return struct{}{}, nil
		}
		for i := range p.milestones {
			if p.milestones[i].ID == msID && p.milestones[i].TaskID == "" {
				p.milestones[i].TaskID = task.ID
			}
		}
		b, _ := json.Marshal(p.milestones)
		if _, err := tx.Exec(`UPDATE goal_plans SET milestones=? WHERE goal_id=?`, string(b), goalID); err != nil {
			return struct{}{}, err
		}
		text := fmt.Sprintf("Delegated: %s", task.Title)
		if task.State == TaskDraft {
			text += " (waits for your approval)"
		}
		_, err = addFactTx(tx, Fact{AssistantID: task.AssistantID, GoalID: goalID, TaskID: task.ID, Kind: FactDelegated,
			Text: text}, "delegated:"+task.ID, s.nowMS())
		return struct{}{}, err
	})
	return err
}

// stale reminds, with backoff, that a Goal with unfinished work has had no
// progress: after staleAfter, then 2×, then 4×, then never again until
// something moves (a new progress fact restarts the clock).
func (sv *Supervisor) stale(g Goal, plan GoalPlan, rep *SupervisorReport) error {
	s := sv.Store
	var last int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(created_at),0) FROM goal_facts WHERE goal_id=? AND kind NOT IN (?,?)`, g.ID, FactStale, FactHeld).Scan(&last); err != nil {
		return wrapBusy(err)
	}
	if last == 0 || plan.StaleAfterMS <= 0 {
		return nil
	}
	pol, err := s.GetProactivity(g.AssistantID)
	if err != nil {
		return err
	}
	if pol.Level == LevelRespond || pol.QuietNow {
		return nil // a quiet reminder is only late: it is said once the window ends
	}
	if pol.ReportEveryMin > 0 {
		var lastSaid int64
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(created_at),0) FROM goal_facts WHERE assistant_id=? AND kind=?`, g.AssistantID, FactStale).Scan(&lastSaid); err != nil {
			return wrapBusy(err)
		}
		if lastSaid > 0 && s.nowMS()-lastSaid < int64(pol.ReportEveryMin)*60000 {
			return nil
		}
	}
	elapsed := s.nowMS() - last
	for k := 0; k < MaxStaleReminders; k++ {
		if elapsed < plan.StaleAfterMS<<k {
			break
		}
		added, err := s.newFact(Fact{AssistantID: g.AssistantID, GoalID: g.ID, Kind: FactStale,
			Text: fmt.Sprintf("No progress on %q for %d min — %d of %d milestone(s) verified, plan is %s", g.Title, elapsed/60000, plan.Verified, plan.Total, plan.State)},
			fmt.Sprintf("stale:%s:%d:%d", g.ID, last, k))
		if err != nil {
			return err
		}
		if added {
			rep.Facts++
			if pol.ReportEveryMin > 0 {
				break // with a cadence, one reminder per pass
			}
		}
	}
	return nil
}
