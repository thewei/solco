package remotehost

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: 依赖 crypto/*、encoding/json 标准库与 tls.go 的 writeFileAtomic
 * [OUTPUT]: 对外提供 Device、DeviceStore（Add/Authenticate/Revoke/List/OnRevoke）
 * [POS]: 已配对手机的唯一记录：只存 token 的 SHA-256、名称与时间；明文 token 只在 Add 返回一次，撤销后立即失效并通知调用方关闭该设备的连接
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	devicesFile     = "devices.json"
	maxDeviceName   = 64
	lastSeenMinStep = time.Minute
)

// Device is the durable record of one paired phone. It never holds the token.
type Device struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TokenHash  string    `json:"tokenHash"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	// TailcatKey is the phone's Tailcat node public key, bound once (at pairing from the tunnel handshake, or by an
	// authenticated enroll). It is what the tunnel admits and what a token must arrive with on that route.
	TailcatKey string `json:"tailcatKey,omitempty"`
}

type devicesDisk struct {
	Devices []Device `json:"devices"`
}

// DeviceStore persists paired devices under dir.
type DeviceStore struct {
	mu       sync.Mutex
	path     string
	now      func() time.Time
	devices  []Device
	onRevoke func(id string)
	// onRevokeKey runs after a revoke with the removed device's tunnel key (when it had one).
	onRevokeKey func(key string)
	// stamp is the device file's modification time as last read or written by this store, so a second process
	// sharing the directory (the desktop app and the resident service take turns) is noticed by Refresh.
	stamp time.Time
}

// OpenDeviceStore loads (or starts empty) the device list in dir. A nil clock
// means time.Now. A corrupt file is an error: silently dropping it would
// un-pair every phone without telling the user.
func OpenDeviceStore(dir string, now func() time.Time) (*DeviceStore, error) {
	if now == nil {
		now = time.Now
	}
	s := &DeviceStore{path: filepath.Join(dir, devicesFile), now: now}
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	var disk devicesDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return nil, fmt.Errorf("read %s: %w", devicesFile, err)
	}
	s.devices = disk.Devices
	s.stamp = fileStamp(s.path)
	return s, nil
}

func fileStamp(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// Refresh re-reads the file when another process rewrote it (the other owner of the shared directory revoked or
// added a phone) and runs the revoke callbacks for every device that vanished, so this process closes its sockets
// and tunnel peers too. A file that cannot be parsed is ignored: the in-memory list stays authoritative.
func (s *DeviceStore) Refresh() {
	s.mu.Lock()
	stamp := fileStamp(s.path)
	if stamp.Equal(s.stamp) {
		s.mu.Unlock()
		return
	}
	raw, err := os.ReadFile(s.path)
	var disk devicesDisk
	if err != nil || json.Unmarshal(raw, &disk) != nil {
		s.mu.Unlock()
		return
	}
	keep, keys := map[string]bool{}, map[string]bool{}
	for _, d := range disk.Devices {
		keep[d.ID] = true
		if d.TailcatKey != "" {
			keys[d.TailcatKey] = true
		}
	}
	var gone []Device
	for _, d := range s.devices {
		if !keep[d.ID] {
			gone = append(gone, d)
		}
	}
	s.devices, s.stamp = disk.Devices, stamp
	cb, keyCb := s.onRevoke, s.onRevokeKey
	s.mu.Unlock()
	for _, d := range gone {
		if cb != nil {
			cb(d.ID)
		}
		// A key a surviving card still holds (the phone paired again) keeps its tunnel peer.
		if keyCb != nil && d.TailcatKey != "" && !keys[d.TailcatKey] {
			keyCb(d.TailcatKey)
		}
	}
}

// OnRevoke registers the callback run (outside the store lock) after a device
// is revoked, so the server can close that device's sockets at once.
func (s *DeviceStore) OnRevoke(fn func(id string)) {
	s.mu.Lock()
	s.onRevoke = fn
	s.mu.Unlock()
}

// OnRevokeKey registers the callback run after a revoked device that carried a tunnel key.
func (s *DeviceStore) OnRevokeKey(fn func(key string)) {
	s.mu.Lock()
	s.onRevokeKey = fn
	s.mu.Unlock()
}

// Add records a new device and returns it with its plaintext token — the only
// time the token exists outside the phone.
func (s *DeviceStore) Add(name string) (Device, string, error) { return s.AddWithKey(name, "") }

// AddWithKey is Add with the phone's tunnel node key bound from the start.
func (s *DeviceStore) AddWithKey(name, tailcatKey string) (Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Phone"
	}
	if r := []rune(name); len(r) > maxDeviceName {
		name = string(r[:maxDeviceName])
	}
	tokenRaw := make([]byte, 32)
	idRaw := make([]byte, 8)
	if _, err := rand.Read(tokenRaw); err != nil {
		return Device{}, "", err
	}
	if _, err := rand.Read(idRaw); err != nil {
		return Device{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenRaw)
	now := s.now()
	dev := Device{ID: "dev_" + hex.EncodeToString(idRaw), Name: name, TokenHash: hashToken(token), CreatedAt: now, LastSeenAt: now, TailcatKey: tailcatKey}

	s.mu.Lock()
	// One phone install has one node key, so pairing again with a key an earlier pairing already carries
	// replaces that card instead of stacking a second row: the old token stops authenticating at once.
	var replaced []string
	next := make([]Device, 0, len(s.devices)+1)
	for _, d := range s.devices {
		if tailcatKey != "" && d.TailcatKey == tailcatKey {
			replaced = append(replaced, d.ID)
			continue
		}
		next = append(next, d)
	}
	next = append(next, dev)
	if err := s.saveLocked(next); err != nil {
		s.mu.Unlock()
		return Device{}, "", err
	}
	s.devices = next
	cb := s.onRevoke
	s.mu.Unlock()
	// Only the sockets of the replaced cards close; the tunnel peer stays (the new card owns the same key).
	if cb != nil {
		for _, id := range replaced {
			cb(id)
		}
	}
	return dev, token, nil
}

// Authenticate returns the device a token belongs to. Every stored hash is
// compared in constant time so the response time does not reveal a match.
// LastSeenAt is persisted at most once a minute.
func (s *DeviceStore) Authenticate(token string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	s.Refresh()
	want := []byte(hashToken(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	found := -1
	for i, d := range s.devices {
		if subtle.ConstantTimeCompare([]byte(d.TokenHash), want) == 1 {
			found = i
		}
	}
	if found < 0 {
		return Device{}, false
	}
	if now := s.now(); now.Sub(s.devices[found].LastSeenAt) >= lastSeenMinStep {
		next := append([]Device(nil), s.devices...)
		next[found].LastSeenAt = now
		if s.saveLocked(next) == nil {
			s.devices = next
		}
	}
	return s.devices[found], true
}

// Revoke removes a device; its token stops authenticating immediately.
func (s *DeviceStore) Revoke(id string) error {
	s.mu.Lock()
	idx := -1
	for i, d := range s.devices {
		if d.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("unknown device %q", id)
	}
	key := s.devices[idx].TailcatKey
	next := append(append([]Device(nil), s.devices[:idx]...), s.devices[idx+1:]...)
	if err := s.saveLocked(next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.devices = next
	cb, keyCb := s.onRevoke, s.onRevokeKey
	s.mu.Unlock()
	if cb != nil {
		cb(id)
	}
	if keyCb != nil && key != "" {
		keyCb(key)
	}
	return nil
}

// HasTailcatKey reports whether a paired device is bound to this tunnel node key.
func (s *DeviceStore) HasTailcatKey(key string) bool {
	if key == "" {
		return false
	}
	s.Refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if d.TailcatKey == key {
			return true
		}
	}
	return false
}

// ErrKeyBound says the device already carries a different tunnel key; re-pairing is the only way to change it.
var ErrKeyBound = errors.New("device is bound to another tunnel key")

// BindTailcatKey binds key to a device that has none (idempotent for the same key).
func (s *DeviceStore) BindTailcatKey(id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.devices {
		if d.ID != id {
			continue
		}
		if d.TailcatKey == key {
			return nil
		}
		if d.TailcatKey != "" {
			return ErrKeyBound
		}
		next := append([]Device(nil), s.devices...)
		next[i].TailcatKey = key
		if err := s.saveLocked(next); err != nil {
			return err
		}
		s.devices = next
		return nil
	}
	return fmt.Errorf("unknown device %q", id)
}

// List returns a copy of the paired devices (token hashes included in the
// copy only so callers can persist; UI code shows ID/Name/times).
func (s *DeviceStore) List() []Device {
	s.Refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Device(nil), s.devices...)
}

func (s *DeviceStore) saveLocked(devices []Device) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(devicesDisk{Devices: devices}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, raw, 0o600); err != nil {
		return err
	}
	s.stamp = fileStamp(s.path)
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
