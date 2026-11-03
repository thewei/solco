package personalagent

// InboxWatcher is the notifier's judgement: which inbox items are new since
// its last look. It reads the same snapshot the page and the phone read, keeps
// only the set of item ids it has already seen, and stays silent on its first
// look (what was waiting before the daemon started is not news).
type InboxWatcher struct {
	Store *Store
	seen  map[string]bool
}

// Tick returns the non-news items that appeared since the previous Tick.
func (w *InboxWatcher) Tick() ([]InboxItem, error) {
	in, err := w.Store.Inbox("", InboxOptions{})
	if err != nil {
		return nil, err
	}
	now := make(map[string]bool, len(in.Items))
	var fresh []InboxItem
	for _, it := range in.Items {
		if it.Kind == InboxNews {
			continue
		}
		now[it.ID+"|"+it.Kind] = true
		if w.seen != nil && !w.seen[it.ID+"|"+it.Kind] {
			fresh = append(fresh, it)
		}
	}
	w.seen = now
	return fresh, nil
}
