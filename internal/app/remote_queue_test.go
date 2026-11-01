package app

import (
	"errors"
	"testing"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/remotehost"
)

func TestRemoteFollowUpsShareTheDesktopQueue(t *testing.T) {
	q := NewRemoteFollowUps(NewLaunchService(nil))
	const id = "live:omp:term_a"
	if err := q.Enqueue("default", "w1:p1", id, "fp", "next"); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue("default", "w1:p1", id, "fp", "again"); err != nil {
		t.Fatalf("second enqueue = %v, want appended", err)
	}
	items, ok := q.Items(id)
	if !ok || len(items) != 2 || items[0].Text != "next" || items[0].State != "queued" || items[0].ID == "" || items[1].Text != "again" || items[1].ID == "" || items[0].ID == items[1].ID {
		t.Fatalf("items = %+v ok %v", items, ok)
	}
	if err := q.CancelItem(id, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if items, _ = q.Items(id); len(items) != 1 || items[0].Text != "again" {
		t.Fatalf("items after CancelItem = %+v", items)
	}
	if err := q.CancelItem(id, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := q.Items(id); ok {
		t.Fatal("a fully cancelled queue is empty, not live")
	}
	if err := q.CancelItem(id, "remote-queue-none"); !errors.Is(err, remotehost.ErrFollowUpNone) {
		t.Fatalf("CancelItem of nothing = %v", err)
	}
	if err := q.Cancel(id); !errors.Is(err, remotehost.ErrFollowUpNone) {
		t.Fatalf("Cancel of nothing = %v", err)
	}
}

func TestRemoteFollowUpsRefuseBehindAnUncertainDelivery(t *testing.T) {
	q := NewRemoteFollowUps(NewLaunchService(nil))
	const id = "live:omp:term_a"
	if err := q.Enqueue("default", "w1:p1", id, "fp", "next"); err != nil {
		t.Fatal(err)
	}
	items, _ := q.Items(id)
	// A crash mid-delivery leaves the tombstone; the conversation freezes.
	if _, err := q.service.Queue().ClaimDelivery(conversation.ConversationID(id), items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.service.Queue().CompleteDelivery(conversation.ConversationID(id), items[0].ID, false, "check the terminal"); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue("default", "w1:p1", id, "fp", "more"); !errors.Is(err, remotehost.ErrFollowUpFrozen) {
		t.Fatalf("enqueue behind the tombstone = %v, want ErrFollowUpFrozen", err)
	}
	// The tombstone itself stays visible to the phone until acknowledged.
	if items, _ = q.Items(id); len(items) != 1 || items[0].State != string(conversation.DeliveryUncertain) || items[0].Detail == "" {
		t.Fatalf("tombstone = %+v", items)
	}
	if err := q.CancelItem(id, items[0].ID); err != nil {
		t.Fatal(err)
	}
}
