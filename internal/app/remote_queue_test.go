package app

import (
	"errors"
	"testing"

	"github.com/thewei/solco/internal/remotehost"
)

func TestRemoteFollowUpsShareTheDesktopQueueSlot(t *testing.T) {
	q := NewRemoteFollowUps(NewLaunchService(nil))
	if err := q.Enqueue("default", "w1:p1", "live:omp:term_a", "fp", "next"); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue("default", "w1:p1", "live:omp:term_a", "fp", "again"); !errors.Is(err, remotehost.ErrFollowUpFull) {
		t.Fatalf("second enqueue = %v, want ErrFollowUpFull", err)
	}
	if item, ok := q.Get("live:omp:term_a"); !ok || item.Text != "next" || item.State != "queued" {
		t.Fatalf("get = %+v %v", item, ok)
	}
	if err := q.Cancel("live:omp:term_a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := q.Get("live:omp:term_a"); ok {
		t.Fatal("a cancelled follow-up is settled, not live")
	}
	if err := q.Cancel("live:omp:other"); !errors.Is(err, remotehost.ErrFollowUpNone) {
		t.Fatalf("cancel of nothing = %v", err)
	}
}
