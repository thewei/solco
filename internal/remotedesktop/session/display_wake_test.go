package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// sleepingCap is a Mac whose display is asleep: it lists no display until it is woken (and a moment after).
type sleepingCap struct {
	*screenCap
	wm      sync.Mutex
	woken   bool
	listing int // Displays calls since the wake
	after   int // empty answers still given after the wake
}

func (c *sleepingCap) Wake(ctx context.Context) error {
	c.wm.Lock()
	c.woken = true
	c.wm.Unlock()
	return c.screenCap.Wake(ctx)
}

func (c *sleepingCap) Displays(ctx context.Context) ([]protocol.DisplayInfo, error) {
	c.wm.Lock()
	defer c.wm.Unlock()
	if !c.woken {
		return nil, nil
	}
	c.listing++
	if c.listing <= c.after {
		return nil, nil
	}
	return c.fakeCap.Displays(ctx)
}

func prepareOn(t *testing.T, mode protocol.Mode, c *sleepingCap) error {
	t.Helper()
	oldWait, oldPoll := displayWakeWait, displayWakePoll
	displayWakeWait, displayWakePoll = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { displayWakeWait, displayWakePoll = oldWait, oldPoll })
	s := New(Config{
		ID: "s1", Device: protocol.DeviceInfo{ID: "d1", Name: "Phone"}, Mode: mode, Tier: protocol.TierStandard, Idle: true,
		Capturer: c, OnEnd: func(protocol.ErrorCode) {},
	})
	t.Cleanup(func() { s.Close(protocol.CodeSessionEnded) })
	_, err := s.Prepare(context.Background())
	return err
}

func TestAControlSessionWakesASleepingDisplayBeforeItGivesUp(t *testing.T) {
	c := &sleepingCap{screenCap: &screenCap{fakeCap: newFakeCap()}, after: 3}
	if err := prepareOn(t, protocol.ModeFullControl, c); err != nil {
		t.Fatalf("a Control session on a sleeping Mac failed: %v", err)
	}
	if c.screenCap.wakes != 1 {
		t.Fatalf("wakes = %d, want 1", c.screenCap.wakes)
	}
}

func TestAViewOnlySessionDoesNotWakeTheMac(t *testing.T) {
	c := &sleepingCap{screenCap: &screenCap{fakeCap: newFakeCap()}}
	err := prepareOn(t, protocol.ModeViewOnly, c)
	if protocol.CodeOf(err) != protocol.CodeDisplayNotFound {
		t.Fatalf("err = %v, want display_not_found", err)
	}
	if c.screenCap.wakes != 0 {
		t.Fatalf("a view-only session woke the Mac %d times", c.screenCap.wakes)
	}
}

func TestAMacThatStaysDarkStillEndsWithDisplayNotFound(t *testing.T) {
	c := &sleepingCap{screenCap: &screenCap{fakeCap: newFakeCap()}, after: 1 << 30}
	err := prepareOn(t, protocol.ModeFullControl, c)
	if protocol.CodeOf(err) != protocol.CodeDisplayNotFound {
		t.Fatalf("err = %v, want display_not_found", err)
	}
}
