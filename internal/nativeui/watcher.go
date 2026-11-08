package nativeui

import (
	"context"
	"log/slog"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

func (s *Shell) startWatcher(projection herdr.Projection) {
	if s.watchCancel != nil {
		s.watchCancel()
	}
	if s.activeInstance == "" || s.win == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.watchCancel = cancel
	instance := s.activeInstance
	generation := s.generation.Load()
	go s.watchProjection(ctx, instance, generation, projection)
}

const (
	eventQuietWindow = 35 * time.Millisecond
	eventMaxLatency  = 140 * time.Millisecond
	remoteRetryCap   = 30 * time.Second
)

func (s *Shell) watchProjection(ctx context.Context, instance string, generation uint64, current herdr.Projection) {
	// A device in use may drop: the first failed subscription marks it lost
	// (the window dims and offers recovery), retries back off so a host that
	// wants a sign-in is not dialed twice a second, and the first success
	// reads a fresh projection through the restored link.
	remote := herdr.ParseRef(instance).IsRemote()
	reportedSubscribeError := false
	failures := 0
	for ctx.Err() == nil {
		stream, err := s.runtime.SubscribeEvents(instance, projectionPaneIDs(current))
		if err != nil {
			if !reportedSubscribeError {
				slog.Warn("Herdr event subscription unavailable", "instance", instance, "error", err)
				reportedSubscribeError = true
				if remote {
					s.noteDeviceLost(instance, generation, err)
				}
			}
			delay := 600 * time.Millisecond
			if remote {
				delay = remoteRetryDelay(failures)
			}
			failures++
			if !sleepContext(ctx, delay) {
				return
			}
			continue
		}
		failures = 0
		if reportedSubscribeError {
			slog.Info("Herdr event subscription recovered", "instance", instance)
			reportedSubscribeError = false
			if remote {
				if next, err := s.runtime.Projection(instance); err == nil {
					current = next
					s.noteDeviceRecovered(instance, generation, next)
				}
			}
		}
		restart := false
		for !restart {
			select {
			case <-ctx.Done():
				stream.Close()
				return
			case first, ok := <-stream.Events():
				if !ok {
					restart = true
					continue
				}

				membership, streamOpen, batchOK := collectRuntimeEventBurst(
					ctx,
					stream.Events(),
					first,
					eventQuietWindow,
					eventMaxLatency,
				)
				if !batchOK {
					stream.Close()
					return
				}
				next, err := s.runtime.Projection(instance)
				if err == nil {
					current = next
					if s.win != nil {
						s.win.Update(func() {
							if generation != s.generation.Load() || instance != s.activeInstance {
								return
							}
							s.applyProjection(next, false)
						})
					}
				}
				if membership || !streamOpen {
					restart = true
				}
			}
		}
		stream.Close()
		if !sleepContext(ctx, 100*time.Millisecond) {
			return
		}
	}
}

func collectRuntimeEventBurst(
	ctx context.Context,
	events <-chan herdr.RuntimeEvent,
	first herdr.RuntimeEvent,
	quietWindow time.Duration,
	maxLatency time.Duration,
) (membership bool, streamOpen bool, ok bool) {
	membership = paneMembershipEvent(first)
	streamOpen = true
	quiet := time.NewTimer(quietWindow)
	maxDelay := time.NewTimer(maxLatency)
	defer quiet.Stop()
	defer maxDelay.Stop()

	for {
		select {
		case <-ctx.Done():
			return membership, streamOpen, false
		case event, open := <-events:
			if !open {
				return membership, false, true
			}
			membership = membership || paneMembershipEvent(event)
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(quietWindow)
		case <-quiet.C:
			return membership, streamOpen, true
		case <-maxDelay.C:
			return membership, streamOpen, true
		}
	}
}

func paneMembershipEvent(event herdr.RuntimeEvent) bool {
	return event.Event == "pane.created" || event.Event == "pane.closed"
}

func projectionPaneIDs(projection herdr.Projection) []string {
	ids := make([]string, 0, len(projection.Panes))
	for _, pane := range projection.Panes {
		ids = append(ids, pane.ID)
	}
	return ids
}

// remoteRetryDelay is the wait before a lost device's next try: 600 ms doubling
// per consecutive failure up to 30 s.
func remoteRetryDelay(failures int) time.Duration {
	delay := 600 * time.Millisecond << min(failures, 6)
	return min(delay, remoteRetryCap)
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
