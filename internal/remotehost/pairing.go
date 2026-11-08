package remotehost

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: 依赖 crypto/rand、crypto/subtle 标准库
 * [OUTPUT]: 对外提供 Pairing（Start 生成一次性配对码、Consume 校验并销毁）与 PairingTTL
 * [POS]: 配对的一次性秘密：6 分钟、单次有效、同一时刻只有一个活动码；配对码不是可复用凭据，也绝不写日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// PairingTTL is how long a pairing code stays valid.
const PairingTTL = 6 * time.Minute

var pairingEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Pairing holds the single active one-time pairing code.
type Pairing struct {
	mu      sync.Mutex
	now     func() time.Time
	code    string
	expires time.Time
}

// NewPairing builds a Pairing; a nil clock means time.Now.
func NewPairing(now func() time.Time) *Pairing {
	if now == nil {
		now = time.Now
	}
	return &Pairing{now: now}
}

// Start replaces any active code with a fresh 128-bit one and returns it with
// its expiry.
func (p *Pairing) Start() (string, time.Time, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code = pairingEncoding.EncodeToString(raw)
	p.expires = p.now().Add(PairingTTL)
	return p.code, p.expires, nil
}

// Consume reports whether code is the active, unexpired code and destroys it:
// a code works once. A wrong guess leaves the active code untouched.
func (p *Pairing) Consume(code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.code == "" || !p.now().Before(p.expires) {
		p.code = ""
		return false
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(p.code)) != 1 {
		return false
	}
	p.code = ""
	return true
}

// Active reports whether a code is waiting and unexpired: the pairing window is open.
func (p *Pairing) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code != "" && p.now().Before(p.expires)
}

// Cancel drops the active code (the Settings sheet closed).
func (p *Pairing) Cancel() {
	p.mu.Lock()
	p.code = ""
	p.mu.Unlock()
}
