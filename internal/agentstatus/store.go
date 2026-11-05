package agentstatus

import (
	"sync"
	"time"
)

/**
 * [INPUT]: 依赖 Parse/Report/Set 与 sync/time；Solco 直属 hook、Herdr spool 与终端 OSC 事件共用此模型
 * [OUTPUT]: Store；原生/通用报文、Solco/Herdr Hook 来源仲裁、生命周期控制、Terminal/Agent 投影读取及 Pane 清理接口
 * [POS]: 每个 Pane 的唯一状态仲裁器：生效中 OSC 7501 > Solco 自有 Hook > Herdr Hook；不用静默超时剥夺协议权威
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Control names terminal lifecycle events, not presentation events. In
// particular, detaching a visible Terminal widget is NOT process exit.
type Control uint8

const (
	ControlPrompt      Control = iota + 1 // OSC 133;A
	ControlReset                          // RIS (ESC c)
	ControlProcessExit                    // confirmed program process exit
)

type statusSource uint8

const (
	sourceNone statusSource = iota
	sourceProgram
	sourceSolco
	sourceHerdr
)

// paneState is a status *projection*, not a second process/session runtime.
// Herdr retains process ownership and Solco's hook retains session identity.
type paneState struct {
	records Set
	solco   string
	herdr   string
	source  statusSource
}

// Store serializes OSC reports and provider hook reports from distinct
// goroutines; its read path returns exactly the status currently applied.
type Store struct {
	mu    sync.Mutex
	panes map[string]*paneState
}

func NewStore() *Store {
	return &Store{panes: make(map[string]*paneState)}
}

func (s *Store) pane(paneID string) *paneState {
	p, ok := s.panes[paneID]
	if !ok {
		p = &paneState{}
		s.panes[paneID] = p
	}
	return p
}

// ApplyProgramReport first fully parses a report, then atomically applies
// it to its id record. A child blocked record cannot be masked by a root
// report: the return value is the *effective* Set.Display value.
func (s *Store) ApplyProgramReport(paneID string, body []byte, now time.Time) (string, bool) {
	return s.applyProgram(paneID, body, now, true)
}

// ApplyPassiveProgramReport tracks generic terminal programs without letting
// them commandeer an already identified coding Agent's status. The records
// still obey all OSC lifetimes, clear/subtree and reset rules.
func (s *Store) ApplyPassiveProgramReport(paneID string, body []byte, now time.Time) (string, bool) {
	return s.applyProgram(paneID, body, now, false)
}

func (s *Store) applyProgram(paneID string, body []byte, now time.Time, agentOwned bool) (string, bool) {
	rep, ok := Parse(body)
	if !ok {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pane(paneID)
	p.records.Apply(rep, now)
	if !agentOwned {
		if p.source == sourceProgram {
			p.restoreFallback()
		}
		return p.display(), true
	}
	if _, exists := p.records.Display(); exists {
		p.source = sourceProgram
	} else {
		p.restoreFallback()
	}
	return p.display(), true
}

// ApplySolcoHookStatus is the direct in-app Unix socket path. Solco's hook
// is always preferred to the Herdr spool. While a native OSC 7501 record
// is working/blocked it stays the source even during long silent periods:
// the protocol explicitly has NO heartbeat requirement.
func (s *Store) ApplySolcoHookStatus(paneID, status string, _ time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pane(paneID)
	p.solco = status
	if status == "released" {
		// The session_end event is the provider's own exit signal; terminal
		// completion records remain even when transient records are dropped.
		p.records.DropTransient()
		if _, ok := p.records.Display(); ok && p.source == sourceProgram {
			return p.display(), true
		}
		p.source = sourceSolco
		return status, true
	}
	if p.source == sourceProgram && p.records.Transient() {
		return p.display(), false
	}
	p.source = sourceSolco
	return status, true
}

// ApplyVerifiedAgentEnd retires only transient native OSC run records after
// the *same provider's* complete agent_end was identity-fenced by the caller.
// It is a stronger lifecycle fact than a prior OSC working frame, but
// completed done/error records remain visible. It must never be invoked for
// a single assistant message_end or a generic Herdr/spool observation.
func (s *Store) ApplyVerifiedAgentEnd(paneID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pane(paneID)
	p.solco = "idle"
	p.records.DropTransient()
	if p.hasProgramStatus() {
		p.source = sourceProgram
	} else {
		p.source = sourceSolco
	}
	return p.display()
}

// ApplyHerdrHookStatus is ONLY the fallback when no native report or active
// Solco direct-hook source is authoritative. It never injects a Solco event
// or overwrites a native record. It remains available for hook-less agents.
func (s *Store) ApplyHerdrHookStatus(paneID, status string, _ time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pane(paneID)
	p.herdr = status
	if (p.source == sourceProgram && p.hasProgramStatus()) ||
		(p.solco != "" && p.solco != "released") {
		return p.display(), false
	}
	p.source = sourceHerdr
	return status, true
}

// ApplyHookStatus is kept as a compatibility name for existing Solco direct
// hook clients; callers that read Herdr's spool MUST use ApplyHerdrHookStatus.
func (s *Store) ApplyHookStatus(paneID, status string, now time.Time) (string, bool) {
	return s.ApplySolcoHookStatus(paneID, status, now)
}

// ApplyControl enforces the protocol's event-based lifetime. No periodic
// timer or window hide/reattach is permitted to remove stored records.
func (s *Store) ApplyControl(paneID string, event Control) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, exists := s.panes[paneID]
	if !exists {
		return "", false
	}
	switch event {
	case ControlPrompt, ControlProcessExit:
		p.records.DropTransient()
	case ControlReset:
		p.records.Reset()
	default:
		return p.display(), false
	}
	if p.source == sourceProgram && !p.hasProgramStatus() {
		p.restoreFallback()
	}
	return p.display(), true
}

// DisplayTerminal is the terminal's raw record projection, independent of
// which status source currently leads an Agent card.
func (s *Store) DisplayTerminal(paneID string) (Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.panes[paneID]
	if !ok {
		return Report{}, false
	}
	return p.records.Display()
}

// HasTransient lets an authoritative Herdr process-info observation
// retire native in-flight records without disturbing done/error survivors.
func (s *Store) HasTransient(paneID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.panes[paneID]
	return ok && p.records.Transient()
}

func (s *Store) Display(paneID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.panes[paneID]
	if !ok {
		return ""
	}
	return p.display()
}

// Reset releases all status projections when the active Herdr instance
// changes; a Pane id is only unique inside its instance.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.panes = make(map[string]*paneState)
}

// PaneClosed is for an authoritative Herdr Pane/terminal deletion only.
func (s *Store) PaneClosed(paneID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.panes, paneID)
}

func (p *paneState) hasProgramStatus() bool {
	_, ok := p.records.Display()
	return ok
}

func (p *paneState) restoreFallback() {
	switch {
	case p.solco != "" && p.solco != "released":
		p.source = sourceSolco
	case p.herdr != "":
		p.source = sourceHerdr
	case p.solco != "":
		p.source = sourceSolco
	default:
		p.source = sourceNone
	}
}

func (p *paneState) display() string {
	switch p.source {
	case sourceProgram:
		if rep, ok := p.records.Display(); ok {
			return rep.StatusWord()
		}
	case sourceSolco:
		return p.solco
	case sourceHerdr:
		return p.herdr
	}
	return ""
}
