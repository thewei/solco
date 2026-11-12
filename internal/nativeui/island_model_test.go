package nativeui

/**
 * [INPUT]: island_model.go
 * [OUTPUT]: 提示队列的顺序、同 ID 覆盖、上限、清空的验收
 * [POS]: 刘海岛纯模型测试，无窗口
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import "testing"

func alert(id string, k islandKind) islandAlert { return islandAlert{ID: id, Kind: k, Title: id} }

func TestIslandToastsOrderByRankThenArrival(t *testing.T) {
	var q islandToasts
	q.Push(alert("auto", islandAutomation))
	q.Push(alert("done1", islandReview))
	q.Push(alert("ask", islandFeedback))
	q.Push(alert("block", islandAttention))
	q.Push(alert("done2", islandReview))
	var got []string
	for {
		a, ok := q.Next()
		if !ok {
			break
		}
		got = append(got, a.ID)
		q.Finish()
	}
	want := []string{"ask", "block", "done1", "done2", "auto"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestIslandToastsSameIDReplacesWordsAndKeepsItsTurn(t *testing.T) {
	var q islandToasts
	q.Push(alert("a", islandReview))
	q.Push(alert("b", islandReview))
	a, _ := q.Next()
	if a.ID != "a" {
		t.Fatalf("on show = %q", a.ID)
	}
	q.Push(islandAlert{ID: "a", Kind: islandAttention, Title: "a again"})
	if s, _ := q.Showing(); s.Title != "a again" || s.Kind != islandAttention {
		t.Fatalf("the one on show kept its old words: %+v", s)
	}
	q.Push(islandAlert{ID: "b", Kind: islandReview, Title: "b again"})
	if q.Pending() != 1 {
		t.Fatalf("a repeated ID queued twice: pending = %d", q.Pending())
	}
	q.Finish()
	if b, _ := q.Next(); b.Title != "b again" {
		t.Fatalf("waiting alert kept its old words: %+v", b)
	}
}

func TestIslandToastsAreBoundedAndClear(t *testing.T) {
	var q islandToasts
	for i := 0; i < islandQueueMax+5; i++ {
		q.Push(islandAlert{ID: string(rune('a' + i)), Kind: islandReview})
	}
	if q.Pending() != islandQueueMax {
		t.Fatalf("pending = %d, want %d", q.Pending(), islandQueueMax)
	}
	// A more urgent alert past the bound drops the least urgent one, not itself.
	q.Push(alert("urgent", islandAttention))
	if a, _ := q.Next(); a.ID != "urgent" {
		t.Fatalf("first = %q, want the urgent one", a.ID)
	}
	q.Clear()
	if _, ok := q.Showing(); ok || q.Pending() != 0 {
		t.Fatal("Clear left something behind")
	}
}
