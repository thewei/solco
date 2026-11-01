package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/coder/websocket"
)

/**
 * [INPUT]: 依赖 Backend.Subscribe（Herdr events.subscribe）、server.go 的 currentTree 与 trackConn
 * [OUTPUT]: 对外提供 GET /api/v3/events 的 WebSocket 处理器：hello{revision} 之后只推 tree.changed{revision}
 * [POS]: 树变化通知通道：Herdr 事件与 Server.Poke（桌面标题快照变化，无 Herdr 事件）只当「重读触发器」（和桌面同一纪律），去抖 150 ms 重算树，内容真变才推；窗格集合变化时重订阅（Herdr 的 agent_status 订阅按窗格），订阅失败指数退避；不转发 Herdr 全局焦点（手机导航本地）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	eventDebounce    = 150 * time.Millisecond
	wsWriteTimeout   = 10 * time.Second
	subscribeBackMin = time.Second
	subscribeBackMax = 10 * time.Second
)

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, dev Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{subprotocolBearer}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.trackConn(dev.ID, func() { _ = conn.Close(websocket.StatusPolicyViolation, "revoked"); cancel() })()

	// The phone sends nothing on this socket; reading detects a close.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	tree, err := s.currentTree(instance)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "backend_unavailable")
		return
	}
	last := tree.Revision
	if writeFrame(ctx, conn, map[string]any{"t": "hello", "revision": last}) != nil {
		return
	}
	notify := func(rev uint64) error {
		if rev == last {
			return nil
		}
		last = rev
		return writeFrame(ctx, conn, map[string]any{"t": "tree.changed", "revision": rev})
	}
	s.followTree(ctx, instance, notify)
}

// followTree keeps one Herdr event subscription alive and calls notify with
// the new revision whenever the tree content changes. It returns when ctx ends
// or notify fails.
func (s *Server) followTree(ctx context.Context, instance string, notify func(uint64) error) {
	backoff := subscribeBackMin
	for ctx.Err() == nil {
		tree, err := s.currentTree(instance)
		if err == nil {
			err = notify(tree.Revision)
		}
		var src EventSource
		if err == nil {
			src, err = s.opts.Backend.Subscribe(instance, tree.paneIDs())
		}
		if err != nil {
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, subscribeBackMax)
			continue
		}
		backoff = subscribeBackMin
		stop := s.pumpEvents(ctx, instance, src, tree.paneIDs(), notify)
		src.Close()
		if stop {
			return
		}
	}
}

// pumpEvents reads one subscription until it ends or the pane set changes.
// It reports true when the caller should stop altogether.
func (s *Server) pumpEvents(ctx context.Context, instance string, src EventSource, panes []string, notify func(uint64) error) (stop bool) {
	var fire <-chan time.Time
	poke := s.pokeChannel()
	for {
		select {
		case <-ctx.Done():
			return true
		case <-poke:
			poke = s.pokeChannel()
			if fire == nil {
				fire = time.After(eventDebounce)
			}
		case <-src.Done():
			return false
		case _, ok := <-src.Events():
			if !ok {
				return false
			}
			if fire == nil {
				fire = time.After(eventDebounce)
			}
		case <-fire:
			fire = nil
			tree, err := s.currentTree(instance)
			if err != nil {
				continue
			}
			if notify(tree.Revision) != nil {
				return true
			}
			if !slices.Equal(tree.paneIDs(), panes) {
				return false
			}
		}
	}
}

func writeFrame(ctx context.Context, conn *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, raw)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
