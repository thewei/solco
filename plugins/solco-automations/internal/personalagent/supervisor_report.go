// [INPUT]: Committed Task states, Assistant proactivity report cadence and quiet hours
// [OUTPUT]: Bounded, de-duplicated Solco progress facts for genuinely unfinished multi-task work
// [POS]: Supervisor's background progress digest; never fabricates Coordinator dialogue or task completion
// [PROTOCOL]: Update this header on changes, then check CLAUDE.md
package personalagent

import (
	"fmt"
	"strconv"
)

// periodicReports emits a status snapshot only when the person configured a
// cadence. It never wakes a model, writes to a provider transcript, or claims
// success from anything other than committed task states. No unfinished work
// means no heartbeat-like "still here" spam.
func (sv *Supervisor) periodicReports() (int, error) {
	s := sv.Store
	rows, err := s.db.Query("SELECT p.assistant_id FROM proactivity p JOIN assistants a ON a.id=p.assistant_id "+
		"WHERE p.report_every_min>0 AND a.status=? ORDER BY p.assistant_id", StatusActive)
	if err != nil {
		return 0, wrapBusy(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, wrapBusy(err)
	}
	rows.Close()
	added := 0
	for _, id := range ids {
		pol, err := s.GetProactivity(id)
		if err != nil {
			return added, err
		}
		if pol.ReportEveryMin <= 0 || pol.QuietNow || pol.Level == LevelRespond {
			continue
		}
		var total, queued, running, needs, finished int
		var oldest int64
		err = s.db.QueryRow("SELECT COUNT(*),"+
			" COALESCE(SUM(CASE WHEN state IN ('queued','waiting','claimed') THEN 1 ELSE 0 END),0),"+
			" COALESCE(SUM(CASE WHEN state IN ('starting','running') THEN 1 ELSE 0 END),0),"+
			" COALESCE(SUM(CASE WHEN state IN ('draft','blocked','uncertain','review_required') OR needs_you=1 THEN 1 ELSE 0 END),0),"+
			" COALESCE(SUM(CASE WHEN state='done' THEN 1 ELSE 0 END),0),"+
			" COALESCE(MIN(CASE WHEN state IN ('queued','waiting','claimed','starting','running','draft','blocked','uncertain','review_required') THEN created_at END),0)"+
			" FROM tasks WHERE assistant_id=? AND kind NOT IN (?,?)",
			id, KindCoordinator, KindGroupAgent).Scan(&total, &queued, &running, &needs, &finished, &oldest)
		if err != nil {
			return added, wrapBusy(err)
		}
		if queued+running+needs == 0 || oldest <= 0 {
			continue
		}
		interval := int64(pol.ReportEveryMin) * 60000
		now := s.nowMS()
		if now-oldest < interval {
			continue
		}
		var last int64
		if err := s.db.QueryRow("SELECT COALESCE(MAX(created_at),0) FROM goal_facts WHERE assistant_id=? AND kind=?",
			id, FactPulse).Scan(&last); err != nil {
			return added, wrapBusy(err)
		}
		if last > 0 && now-last < interval {
			continue
		}
		message := fmt.Sprintf("Work check-in: %d queued, %d running, %d waiting for review or help; %d finished of %d worker tasks",
			queued, running, needs, finished, total)
		key := fmt.Sprintf("pulse:%s:%s", id, strconv.FormatInt(now/interval, 10))
		if ok, err := s.newFact(Fact{AssistantID: id, Kind: FactPulse, Text: message}, key); err != nil {
			return added, err
		} else if ok {
			added++
		}
	}
	return added, nil
}
