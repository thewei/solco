package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thewei/solco/internal/vaultlink"
)

/**
 * [INPUT]: Options.Vault（Solco Vault App 手机门的回环客户端，桌面与常驻服务各自接线）、vaultlink 的类型与错误码、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 VaultSource、GET /api/v3/vault（scope view：待批问题与开关状态）、GET …/vault/items（scope view：条目名与字段名）、POST …/vault/approvals/{id}/answer（scope terminal-input：允许一次或拒绝）、POST …/vault/reveal（scope terminal-input：读一个值，响应 no-store）、POST …/vault/launch（scope terminal-input：只启动 Vault 应用，不解锁；404 vault_not_installed / 409 vault_already_running / 502 vault_launch_failed）、GET /vault 离线时的 `installed`、capabilities.vaultLaunch、bootstrap 的 capabilities.vault、events 帧 `vault`（NotifyVault）
 * [POS]: 手机的「Vault」页（2026-11-09 决策：手机 = 审批 + 浏览 + 显示值）：Solco Vault 是独立应用，开关（phone_access / phone_reveal）与所有状态的唯一所有者是它；Host 只转发它的回答。Vault 没开 = 200 online:false，开关没开 = 200 access:false（页面画说明而非错误）。手机永远看不到 Mac 路径、pid、cwd（Vault 不给）；笔记不出现；值只在 reveal 一个响应里，日志只记设备与操作名，从不记引用的值、命令或项目
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	vaultCallTimeout = 12 * time.Second
	vaultPollEvery   = 3 * time.Second
	maxVaultBody     = 4 << 10
)

// VaultSource is the Vault app's phone bridge as the Host uses it; *vaultlink.Client satisfies it. nil = no Vault
// view and bootstrap does not advertise it.
type VaultSource interface {
	Pending(ctx context.Context) (vaultlink.Pending, error)
	Answer(ctx context.Context, id int, allow bool) error
	Browse(ctx context.Context) ([]vaultlink.Item, error)
	Reveal(ctx context.Context, ref string) (string, error)
}

// VaultLauncher is the optional half of a VaultSource that can tell whether the app is installed and start it;
// *vaultlink.Client has it. A source without it keeps the phone's Vault page exactly as it was.
type VaultLauncher interface {
	Installed() vaultlink.Install
	Launch(ctx context.Context) error
}

// vaultOffered says the phone gets a Vault page: a source is wired and, when it can tell, Solco Vault is set up here.
func (s *Server) vaultOffered() bool {
	if s.opts.Vault == nil {
		return false
	}
	if a, ok := s.opts.Vault.(interface{ Available() bool }); ok {
		return a.Available()
	}
	return true
}

// vaultView is GET /vault.
type vaultView struct {
	Online    bool                 `json:"online"`
	Installed *bool                `json:"installed,omitempty"` // only while offline, only when the Host can tell
	Access    bool                 `json:"access"`
	State     string               `json:"state,omitempty"`
	CanReveal bool                 `json:"canReveal,omitempty"`
	Approvals []vaultlink.Question `json:"approvals"`
}

func (s *Server) handleVault(w http.ResponseWriter, r *http.Request, dev Device) {
	src := s.opts.Vault
	if src == nil {
		writeError(w, http.StatusNotFound, "vault_unavailable", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), vaultCallTimeout)
	defer cancel()
	p, err := src.Pending(ctx)
	view := vaultView{Approvals: []vaultlink.Question{}}
	var ve *vaultlink.Error
	switch {
	case err == nil:
		view.Online, view.Access, view.State, view.CanReveal = true, true, p.State, p.CanReveal
		if p.Approvals != nil {
			view.Approvals = p.Approvals
		}
	case errors.Is(err, vaultlink.ErrOffline):
		if l, ok := src.(VaultLauncher); ok {
			switch l.Installed() {
			case vaultlink.InstallYes:
				t := true
				view.Installed = &t
			case vaultlink.InstallNo:
				f := false
				view.Installed = &f
			}
		}
	case errors.As(err, &ve) && ve.Code == vaultlink.CodePhoneOff:
		view.Online = true
	default:
		slog.Warn("remote host vault read failed", "device", dev.ID, "error", err)
		writeError(w, http.StatusBadGateway, "vault_failed", "")
		return
	}
	slog.Info("remote host vault", "device", dev.ID, "online", view.Online, "access", view.Access, "waiting", len(view.Approvals))
	writeJSON(w, http.StatusOK, view)
}

// handleVaultLaunch starts the Vault app on the Mac (POST /vault/launch). It only opens the app: nothing is
// unlocked and nothing is sent to it.
func (s *Server) handleVaultLaunch(w http.ResponseWriter, r *http.Request, dev Device) {
	l, ok := s.opts.Vault.(VaultLauncher)
	if !ok {
		writeError(w, http.StatusNotFound, "vault_unavailable", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), vaultCallTimeout)
	defer cancel()
	err := l.Launch(ctx)
	switch {
	case err == nil:
		slog.Info("remote host vault write", "device", dev.ID, "op", "launch")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, vaultlink.ErrNotInstalled):
		writeError(w, http.StatusNotFound, "vault_not_installed", "")
	case errors.Is(err, vaultlink.ErrAlreadyRunning):
		writeError(w, http.StatusConflict, "vault_already_running", "")
	default:
		slog.Warn("remote host vault launch failed", "device", dev.ID, "error", err)
		writeError(w, http.StatusBadGateway, "vault_launch_failed", "")
	}
}

func (s *Server) handleVaultItems(w http.ResponseWriter, r *http.Request, dev Device) {
	src := s.opts.Vault
	if src == nil {
		writeError(w, http.StatusNotFound, "vault_unavailable", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), vaultCallTimeout)
	defer cancel()
	items, err := src.Browse(ctx)
	if err != nil {
		s.finishVault(w, dev, "browse", err)
		return
	}
	if items == nil {
		items = []vaultlink.Item{}
	}
	slog.Info("remote host vault browse", "device", dev.ID, "items", len(items))
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleVaultAnswer(w http.ResponseWriter, r *http.Request, dev Device) {
	src := s.opts.Vault
	if src == nil {
		writeError(w, http.StatusNotFound, "vault_unavailable", "")
		return
	}
	id, convErr := strconv.Atoi(r.PathValue("id"))
	var req struct {
		Allow *bool `json:"allow"`
	}
	if convErr != nil || id < 1 || json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVaultBody)).Decode(&req) != nil || req.Allow == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), vaultCallTimeout)
	defer cancel()
	op := "deny"
	if *req.Allow {
		op = "allow"
	}
	s.finishVault(w, dev, op, src.Answer(ctx, id, *req.Allow))
}

func (s *Server) handleVaultReveal(w http.ResponseWriter, r *http.Request, dev Device) {
	src := s.opts.Vault
	if src == nil {
		writeError(w, http.StatusNotFound, "vault_unavailable", "")
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVaultBody)).Decode(&req) != nil || !strings.Contains(req.Ref, "#") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), vaultCallTimeout)
	defer cancel()
	value, err := src.Reveal(ctx, req.Ref)
	if err != nil {
		s.finishVault(w, dev, "reveal", err)
		return
	}
	slog.Info("remote host vault write", "device", dev.ID, "op", "reveal")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"value": value})
}

func (s *Server) finishVault(w http.ResponseWriter, dev Device, op string, err error) {
	var ve *vaultlink.Error
	switch {
	case err == nil:
		slog.Info("remote host vault write", "device", dev.ID, "op", op)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, vaultlink.ErrOffline):
		writeError(w, http.StatusServiceUnavailable, "vault_offline", "")
	case errors.As(err, &ve):
		switch ve.Code {
		case vaultlink.CodePhoneOff:
			writeError(w, http.StatusConflict, "vault_phone_off", "")
		case vaultlink.CodeRevealOff:
			writeError(w, http.StatusForbidden, "vault_reveal_off", "")
		case vaultlink.CodeLocked:
			writeError(w, http.StatusConflict, "vault_locked", "")
		case vaultlink.CodeNotFound:
			writeError(w, http.StatusNotFound, "vault_not_found", "")
		case vaultlink.CodeNote:
			writeError(w, http.StatusUnprocessableEntity, "vault_note", "")
		case vaultlink.CodeGone:
			writeError(w, http.StatusGone, "vault_gone", "")
		case vaultlink.CodeInvalid:
			writeError(w, http.StatusBadRequest, "bad_request", "")
		default:
			slog.Warn("remote host vault refused", "device", dev.ID, "op", op, "code", ve.Code)
			writeError(w, http.StatusBadGateway, "vault_failed", "")
		}
	default:
		slog.Warn("remote host vault write failed", "device", dev.ID, "op", op, "error", err)
		writeError(w, http.StatusBadGateway, "vault_failed", "")
	}
}

// startVaultWatch polls the Vault bridge for as long as the Host serves and turns each new question into one
// content-light `vault` events frame (project and the reference names, never a value or a path); a Vault that is off
// or not running simply produces nothing.
func (s *Server) startVaultWatch() {
	src := s.opts.Vault
	if src == nil {
		return
	}
	s.mu.Lock()
	if s.vaultCancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.vaultCancel, s.vaultDone = cancel, done
	s.mu.Unlock()
	go func() {
		defer close(done)
		seen := map[int]bool{}
		t := time.NewTicker(vaultPollEvery)
		defer t.Stop()
		for {
			s.vaultPoll(ctx, src, seen)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (s *Server) stopVaultWatch() {
	s.mu.Lock()
	cancel, done := s.vaultCancel, s.vaultDone
	s.vaultCancel, s.vaultDone = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *Server) vaultPoll(ctx context.Context, src VaultSource, seen map[int]bool) {
	turn, cancel := context.WithTimeout(ctx, vaultCallTimeout)
	defer cancel()
	p, err := src.Pending(turn)
	if err != nil {
		for id := range seen {
			delete(seen, id)
		}
		return
	}
	now := map[int]bool{}
	for _, q := range p.Approvals {
		now[q.ID] = true
		if seen[q.ID] {
			continue
		}
		refs := make([]string, len(q.Refs))
		for i, r := range q.Refs {
			refs[i] = strings.TrimPrefix(r.Ref, "vault://")
		}
		s.NotifyVault(VaultNotice{Kind: "approval", ID: q.ID, Waiting: len(p.Approvals), Reveal: q.Reveal,
			Project: clip(q.Project, feedbackNameMax), Preview: clip(strings.Join(refs, ", "), feedbackPreviewMax)})
	}
	resolved := false
	for id := range seen {
		if !now[id] {
			resolved = true
			delete(seen, id)
		}
	}
	for id := range now {
		seen[id] = true
	}
	if resolved {
		s.NotifyVault(VaultNotice{Kind: "resolved", Waiting: len(p.Approvals)})
	}
}

// VaultNotice is one `vault` events frame: a question reached the Vault app (kind "approval") or some were settled
// ("resolved"). The phone re-reads GET /vault for the rest.
type VaultNotice struct {
	Kind    string // "approval" | "resolved"
	ID      int
	Waiting int
	Reveal  bool
	Project string
	Preview string
}

// NotifyVault tells every open events socket. A socket whose queue is full drops the frame rather than stall the Mac.
func (s *Server) NotifyVault(n VaultNotice) {
	frame := map[string]any{"t": "vault", "kind": n.Kind, "waiting": n.Waiting}
	if n.ID != 0 {
		frame["id"] = n.ID
	}
	if n.Reveal {
		frame["reveal"] = true
	}
	for k, v := range map[string]string{"project": n.Project, "preview": n.Preview} {
		if v != "" {
			frame[k] = v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for q := range s.notices {
		select {
		case q <- frame:
		default:
		}
	}
}
