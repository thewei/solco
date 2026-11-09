package vaultlink

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// The same literals the solco-vault repository's internal/bridge speaks: a change on either side shows up in both
// repositories' tests.
const (
	pendingJSON = `{"ok":true,"state":"unlocked","can_reveal":true,"approvals":[{"id":7,"project":"api","command":"make deploy","refs":[{"ref":"vault://agent/prod/pg#password","standing":"new"}],"asked":"2026-11-09T10:00:00Z"}]}`
	offJSON     = `{"ok":false,"err":"phone access is switched off","code":"phone_off"}`
)

func serve(t *testing.T, reply func(req map[string]any) string) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "vl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "bridge.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req map[string]any
			json.NewDecoder(c).Decode(&req)
			c.Write([]byte(reply(req) + "\n"))
			c.Close()
		}
	}()
	return New(dir)
}

func TestPendingAndAnswer(t *testing.T) {
	var last map[string]any
	c := serve(t, func(req map[string]any) string {
		last = req
		if req["op"] == "pending" {
			return pendingJSON
		}
		return `{"ok":true}`
	})
	p, err := c.Pending(context.Background())
	if err != nil || p.State != "unlocked" || !p.CanReveal || len(p.Approvals) != 1 || p.Approvals[0].Command != "make deploy" || p.Approvals[0].Refs[0].Standing != "new" {
		t.Fatalf("pending: %+v %v", p, err)
	}
	if err := c.Answer(context.Background(), 7, true); err != nil {
		t.Fatal(err)
	}
	if last["op"] != "answer" || last["id"] != float64(7) || last["allow"] != true {
		t.Fatalf("request: %v", last)
	}
}

func TestRefusalsKeepTheirCode(t *testing.T) {
	c := serve(t, func(map[string]any) string { return offJSON })
	_, err := c.Pending(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Code != CodePhoneOff {
		t.Fatalf("err: %v", err)
	}
}

func TestOfflineWhenNothingListens(t *testing.T) {
	c := New(t.TempDir())
	if _, err := c.Pending(context.Background()); !errors.Is(err, ErrOffline) {
		t.Fatalf("err: %v", err)
	}
	if _, err := c.Reveal(context.Background(), "vault://a/b#c"); !errors.Is(err, ErrOffline) {
		t.Fatalf("reveal: %v", err)
	}
}

func TestBrowseAndReveal(t *testing.T) {
	c := serve(t, func(req map[string]any) string {
		if req["op"] == "browse" {
			return `{"ok":true,"state":"unlocked","items":[{"path":"agent/prod/pg","kind":"login","fields":["username","password"]}]}`
		}
		return `{"ok":true,"value":"v-123"}`
	})
	items, err := c.Browse(context.Background())
	if err != nil || len(items) != 1 || items[0].Fields[1] != "password" {
		t.Fatalf("browse: %+v %v", items, err)
	}
	if v, err := c.Reveal(context.Background(), "vault://agent/prod/pg#password"); err != nil || v != "v-123" {
		t.Fatalf("reveal: %q %v", v, err)
	}
}
