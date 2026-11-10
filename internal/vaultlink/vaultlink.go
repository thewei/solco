// Package vaultlink is Solco's one-way link to the Solco Vault app: a client of
// the app's phone bridge socket, used by the Remote Host so the phone can answer
// a pending secret approval, browse item names and — if the owner allowed it —
// read one value.
package vaultlink

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

/**
 * [INPUT]: Solco Vault 的 home 目录（SOLCO_VAULT_HOME 或 <用户配置目录>/Solco Vault）里的 bridge.sock
 * [OUTPUT]: Client（Pending/Answer/Browse/Reveal）、Home、各类型、ErrOffline 与 *Error（带 Vault 的错误码）
 * [POS]: Solco → Solco Vault 的单向链接（Solco Vault 自己的仓库定义协议：`internal/bridge`，两边各钉同一份 JSON）。Vault 没开 = ErrOffline，是一等状态；开关（phone_access / phone_reveal）由 Vault App 的主人决定，这里只转达它的回答。值只出现在 Reveal 的返回里，从不进日志；每次调用一连接一行请求一行响应，30 s 期限
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Vault's codes (internal/bridge in the solco-vault repository).
const (
	CodePhoneOff  = "phone_off"
	CodeRevealOff = "reveal_off"
	CodeLocked    = "locked"
	CodeNotFound  = "not_found"
	CodeGone      = "gone"
	CodeInvalid   = "invalid"
	CodeNote      = "note"
)

// ErrOffline means the Solco Vault app is not running (no bridge socket answers).
var ErrOffline = errors.New("solco vault offline")

// Error is a refusal from the Vault app.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return "solco vault: " + e.Code }

// Question is one pending approval: names only.
type Question struct {
	ID      int       `json:"id"`
	Project string    `json:"project"`
	Command string    `json:"command,omitempty"`
	Reveal  bool      `json:"reveal,omitempty"`
	Refs    []Ref     `json:"refs"`
	Asked   time.Time `json:"asked"`
}

// Ref is one reference of a Question and its standing policy (new | ask | allowed).
type Ref struct {
	Ref      string `json:"ref"`
	Standing string `json:"standing"`
}

// Item is an item without values.
type Item struct {
	Path   string   `json:"path"`
	Kind   string   `json:"kind"`
	Fields []string `json:"fields,omitempty"`
}

// Pending is what `pending` returns.
type Pending struct {
	State     string
	CanReveal bool
	Approvals []Question
}

type request struct {
	Op    string `json:"op"`
	ID    int    `json:"id,omitempty"`
	Allow bool   `json:"allow,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

type response struct {
	OK        bool       `json:"ok"`
	Err       string     `json:"err,omitempty"`
	Code      string     `json:"code,omitempty"`
	State     string     `json:"state,omitempty"`
	CanReveal bool       `json:"can_reveal,omitempty"`
	Approvals []Question `json:"approvals,omitempty"`
	Items     []Item     `json:"items,omitempty"`
	Value     string     `json:"value,omitempty"`
}

// Home is where Solco Vault keeps its socket (its own SOLCO_VAULT_HOME rule).
func Home() string {
	if h := os.Getenv("SOLCO_VAULT_HOME"); h != "" {
		return h
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "Solco Vault")
}

// Client dials the bridge socket of one Vault home.
type Client struct {
	socket string
	probe  Prober // nil = the system's (probe.go)
	cache  installCache
}

// New returns a client for home; "" uses Home().
func New(home string) *Client {
	if home == "" {
		home = Home()
	}
	return &Client{socket: filepath.Join(home, "bridge.sock")}
}

func (c *Client) call(ctx context.Context, req request) (response, error) {
	var resp response
	if c.socket == "" || filepath.Base(c.socket) != "bridge.sock" {
		return resp, ErrOffline
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return resp, ErrOffline
	}
	defer conn.Close()
	dl := time.Now().Add(30 * time.Second)
	if t, ok := ctx.Deadline(); ok && t.Before(dl) {
		dl = t
	}
	conn.SetDeadline(dl)
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, ErrOffline
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return resp, ErrOffline
	}
	if !resp.OK {
		return resp, &Error{Code: resp.Code, Message: resp.Err}
	}
	return resp, nil
}

// Available says the Host offers the phone a Vault page at all: the app is installed here (even if it never ran), or
// — when installation cannot be told — it has been set up (its home directory exists). Installed-and-absent hides
// the page; whether the app is running right now is a separate, live state.
func (c *Client) Available() bool {
	if c.socket == "" {
		return false
	}
	switch c.Installed() {
	case InstallYes:
		return true
	case InstallNo:
		return false
	}
	st, err := os.Stat(filepath.Dir(c.socket))
	return err == nil && st.IsDir()
}

// Pending reads the questions waiting for the owner. A switched-off bridge is an *Error with CodePhoneOff.
func (c *Client) Pending(ctx context.Context) (Pending, error) {
	r, err := c.call(ctx, request{Op: "pending"})
	if err != nil {
		return Pending{}, err
	}
	return Pending{State: r.State, CanReveal: r.CanReveal, Approvals: r.Approvals}, nil
}

// Answer allows once (true) or denies (false) one question.
func (c *Client) Answer(ctx context.Context, id int, allow bool) error {
	_, err := c.call(ctx, request{Op: "answer", ID: id, Allow: allow})
	return err
}

// Browse lists item names and field names.
func (c *Client) Browse(ctx context.Context) ([]Item, error) {
	r, err := c.call(ctx, request{Op: "browse"})
	if err != nil {
		return nil, err
	}
	return r.Items, nil
}

// Reveal reads one value (vault://folder/item#field).
func (c *Client) Reveal(ctx context.Context, ref string) (string, error) {
	r, err := c.call(ctx, request{Op: "reveal", Ref: ref})
	if err != nil {
		return "", err
	}
	return r.Value, nil
}
