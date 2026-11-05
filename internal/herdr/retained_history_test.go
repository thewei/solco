package herdr

/**
 * [INPUT]: 依赖伪 Herdr Unix-socket Server 与 Protocol 22 的 pane.copy_motion / pane.get / pane.selection.read 响应
 * [OUTPUT]: 钉住保留历史整段读取、内容修订门、错误降级及不改变共享 Pane 视口
 * [POS]: retained_history.go 的 Schema-shaped 只读契约回归
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestReadPaneRetainedTextUsesAbsoluteSelectionWithoutScrolling(t *testing.T) {
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		switch method {
		case "pane.copy_motion":
			if params["motion"] != "line_end" {
				return nil, errorsNew("expected line_end")
			}
			cursor, _ := params["cursor"].(map[string]any)
			row := int(cursor["row"].(float64))
			switch row {
			case 0:
				if _, exists := params["content_revision"]; exists {
					return nil, errorsNew("first copy motion should not set revision")
				}
				return json.RawMessage(`{"type":"pane_copy_motion","pane_id":"w1:p1","cursor":{"row":0,"col":4},"content_revision":42}`), nil
			case 10:
				if params["content_revision"] != float64(42) {
					return nil, errorsNew("endpoint must be revision-gated")
				}
				return json.RawMessage(`{"type":"pane_copy_motion","pane_id":"w1:p1","cursor":{"row":10,"col":7},"content_revision":42}`), nil
			}
			return nil, fmt.Errorf("unexpected terminal row %d", row)
		case "pane.get":
			return json.RawMessage(`{"type":"pane_info","pane":{"pane_id":"w1:p1","scroll":{"max_offset_from_bottom":8,"offset_from_bottom":0,"viewport_rows":3}}}`), nil
		case "pane.selection.read":
			start := params["anchor"].(map[string]any)
			end := params["cursor"].(map[string]any)
			if start["row"] != float64(0) || start["col"] != float64(0) ||
				end["row"] != float64(10) || end["col"] != float64(7) ||
				params["content_revision"] != float64(42) {
				return nil, errorsNew("absolute selection or revision mismatch")
			}
			return json.RawMessage(`{"type":"pane_selection","pane_id":"w1:p1","text":"oldest\n中间\nnewest"}`), nil
		default:
			return nil, fmt.Errorf("unexpected RPC method %q", method)
		}
	})
	history, err := NewManager().ReadPaneRetainedText(context.Background(), "default", "w1:p1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Text != "oldest\n中间\nnewest" || history.RetainedRows != 11 || history.ContentRevision != 42 {
		t.Fatalf("wrong history result: %+v", history)
	}
	var names []string
	for _, r := range server.recorded() {
		if r.Method == "ping" {
			continue
		}
		names = append(names, r.Method)
		if r.Method == "pane.scroll" || r.Method == "terminal.attach" {
			t.Fatalf("read-only history must not manipulate the runtime: %v", names)
		}
	}
	if got := strings.Join(names, ","); got != "pane.copy_motion,pane.get,pane.copy_motion,pane.selection.read" {
		t.Fatalf("unexpected RPC order: %s", got)
	}
}

func TestReadPaneRetainedTextRejectsChangedContentBeforeExtraction(t *testing.T) {
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		switch method {
		case "pane.copy_motion":
			cursor := params["cursor"].(map[string]any)
			if cursor["row"] == float64(0) {
				return json.RawMessage(`{"type":"pane_copy_motion","pane_id":"w1:p1","cursor":{"row":0,"col":1},"content_revision":10}`), nil
			}
			return nil, errorsNew("stale_content")
		case "pane.get":
			return json.RawMessage(`{"type":"pane_info","pane":{"pane_id":"w1:p1","scroll":{"max_offset_from_bottom":8,"offset_from_bottom":0,"viewport_rows":3}}}`), nil
		default:
			return nil, errorsNew("unexpected extraction after stale revision")
		}
	})
	_, err := NewManager().ReadPaneRetainedText(context.Background(), "default", "w1:p1")
	if err == nil || !strings.Contains(err.Error(), "stale_content") {
		t.Fatalf("expected revision failure, got %v", err)
	}
	for _, req := range server.recorded() {
		if req.Method == "pane.selection.read" {
			t.Fatal("selection must not execute on stale content")
		}
	}
}

func TestReadPaneRetainedTextValidatesRequest(t *testing.T) {
	manager := NewManager()
	if _, err := manager.ReadPaneRetainedText(context.Background(), "", ""); err == nil {
		t.Fatal("empty pane must fail")
	}
	if _, err := manager.ReadPaneRetainedText(nil, "", "w1:p1"); err == nil {
		t.Fatal("nil context must fail")
	}
}

// Opt-in live protocol regression. Never prints or persists terminal contents.
// Run with SOLCO_HERDR_TEST_PANE=<pane-id> in a managed Herdr session.
func TestReadPaneRetainedTextLive(t *testing.T) {
	paneID := os.Getenv("SOLCO_HERDR_TEST_PANE")
	if paneID == "" {
		t.Skip("requires a consenting live test pane")
	}
	session := os.Getenv("SOLCO_HERDR_TEST_SESSION")
	if session == "" {
		session = "default"
	}
	m := NewManager()
	paneOffset := func() (uint64, error) {
		view, err := m.Projection(session)
		if err != nil {
			return 0, err
		}
		for _, pane := range view.Panes {
			if pane.ID == paneID && pane.Scroll != nil {
				return pane.Scroll.OffsetFromBottom, nil
			}
		}
		return 0, fmt.Errorf("pane scroll not available: %s", paneID)
	}
	before, err := paneOffset()
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.ReadPaneRetainedText(context.Background(), session, paneID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := paneOffset()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("read moved shared viewport: %d -> %d", before, after)
	}
	if got.RetainedRows == 0 {
		t.Fatal("no screen rows reported")
	}
	t.Logf("retained_rows=%d bytes=%d viewport_unchanged=true", got.RetainedRows, len(got.Text))
}
