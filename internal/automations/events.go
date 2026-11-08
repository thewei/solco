package automations

/**
 * [INPUT]: 依赖 client.go 的 Automation / LastRun（插件 list --json 的行）
 * [OUTPUT]: 对外提供 EventKind（Draft/Started/Done/Failed）、Event、Watcher.Observe / Reset——把相邻两次 list 之间可通知的状态变化算出来
 * [POS]: 自动化通知的唯一判定处：桌面系统通知与手机推送都吃它的输出，状态仍归插件，这里不存任何东西，只做相邻两次观察的差；无框架代码
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// EventKind is one step of an automation's life that is worth a notification.
type EventKind string

const (
	// EventDraft: an Agent filed a draft and a person has to approve it.
	EventDraft EventKind = "draft"
	// EventStarted: a run began.
	EventStarted EventKind = "started"
	// EventDone: a run finished.
	EventDone EventKind = "done"
	// EventFailed: a run failed.
	EventFailed EventKind = "failed"
)

// Event is what changed for one automation between two reads. It carries no
// prompt, rationale or error text: notifications name the automation and the
// step, and the drawer holds the details.
type Event struct {
	Name  string
	RunID string
	Kind  EventKind
}

type observed struct {
	runID  string
	status string
}

// Watcher turns successive list reads into Events. The first read only
// records the state (an app launch must not announce every old run), the
// same first-observation rule agent notifications follow. It is not
// goroutine-safe: the owner calls it from one thread.
type Watcher struct {
	primed bool
	last   map[string]observed
}

// Reset forgets everything, so the next read is a first observation again
// (the plugin gate closed, or the window moved to another Herdr instance).
func (w *Watcher) Reset() {
	w.primed = false
	w.last = nil
}

// Observe records the list and returns what changed since the previous read:
// a new draft, and each automation whose newest run is a different run or has
// a different status. A run that began and ended between two reads is
// reported by its final status only.
func (w *Watcher) Observe(list []Automation) []Event {
	next := make(map[string]observed, len(list))
	var events []Event
	for _, a := range list {
		cur := observed{}
		if a.LastRun != nil {
			cur = observed{runID: a.LastRun.RunID, status: a.LastRun.Status}
		}
		next[a.Name] = cur
		if !w.primed {
			continue
		}
		prev, known := w.last[a.Name]
		if !known {
			if a.Draft {
				events = append(events, Event{Name: a.Name, Kind: EventDraft})
			}
			continue
		}
		if a.Draft || cur == prev || a.LastRun == nil {
			continue
		}
		switch cur.status {
		case "running":
			events = append(events, Event{Name: a.Name, RunID: cur.runID, Kind: EventStarted})
		case "done":
			events = append(events, Event{Name: a.Name, RunID: cur.runID, Kind: EventDone})
		case "failed":
			events = append(events, Event{Name: a.Name, RunID: cur.runID, Kind: EventFailed})
		}
	}
	w.last, w.primed = next, true
	return events
}
