package personalagent

// ResultWatcher is the notifier's judgement for finished work: which tasks
// completed since its last look and that nothing else already tells the person
// about. A failure, a review and a finished task that asks to be acknowledged
// reach the InboxWatcher's toast; a plain completion would otherwise go by in
// silence. It keeps only the set it has already seen, and stays silent on its
// first look (what finished before the daemon started is not news).
type ResultWatcher struct {
	Store *Store
	seen  map[string]bool
}

// FinishedTask is one completion worth a quiet toast.
type FinishedTask struct {
	AssistantID string
	Name        string
	Title       string
}

// Tick returns the tasks that finished since the previous Tick.
func (w *ResultWatcher) Tick() ([]FinishedTask, error) {
	list, err := w.Store.ListAssistants()
	if err != nil {
		return nil, err
	}
	now := map[string]bool{}
	var fresh []FinishedTask
	for _, a := range list {
		if a.System { // scheduled runs have their own step notifications
			continue
		}
		results, err := w.Store.Results(a.ID, 20)
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			if r.State != TaskDone {
				continue
			}
			key := r.TaskID + "@" + a.ID
			now[key] = true
			if w.seen == nil || w.seen[key] {
				continue
			}
			// The inbox asks about it (acknowledge): one toast, not two.
			if t, err := w.Store.GetTask(r.TaskID); err == nil && t.NeedsYou {
				continue
			}
			fresh = append(fresh, FinishedTask{AssistantID: a.ID, Name: a.Name, Title: r.Title})
		}
	}
	w.seen = now
	return fresh, nil
}
