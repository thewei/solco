package agentstatus

import (
	"testing"
	"time"
)

// hook feeds a hook status and returns what the caller should apply.
func hook(s *Store, pane, status string, at time.Time) (string, bool) {
	return s.ApplyHookStatus(pane, status, at)
}

func TestStoreProtocolWinsWhileTransient(t *testing.T) {
	s := NewStore()
	// Native working report, then the same agent's own hook fires
	// working seconds later: the hook is dropped, the protocol stream
	// stays authoritative.
	if st, ok := s.ApplyProgramReport("p1", []byte("state=working:app=claude-code"), base); !ok || st != "working" {
		t.Fatalf("report: %q ok=%v", st, ok)
	}
	if st, applied := hook(s, "p1", "working", base.Add(time.Second)); applied {
		t.Fatalf("hook passed mid-flight: %q", st)
	}
	// A blocked transition arrives through the protocol; the hook's
	// blocked is still dropped.
	s.ApplyProgramReport("p1", []byte("state=blocked:kind=permission"), base.Add(2*time.Second))
	if st, applied := hook(s, "p1", "blocked", base.Add(3*time.Second)); applied {
		t.Fatalf("hook passed blocked: %q", st)
	}
	if st := s.Display("p1"); st != "blocked" {
		t.Fatalf("display = %q", st)
	}
}

func TestStoreHookTakesOverAfterSurvivor(t *testing.T) {
	s := NewStore()
	// A native session ends with done; the record survives.
	s.ApplyProgramReport("p1", []byte("state=done:msg=RG9uZQ=="), base)
	// A hook-only program (an older agent) starts in the same pane:
	// nothing transient stands, so the hook takes the pane back.
	if st, applied := hook(s, "p1", "working", base.Add(5*time.Second)); !applied || st != "working" {
		t.Fatalf("hook takeover: %q applied=%v", st, applied)
	}
	// From here the protocol stream re-takes the pane the moment it
	// speaks, and hooks are suppressed again mid-flight.
	s.ApplyProgramReport("p1", []byte("state=working"), base.Add(6*time.Second))
	if _, applied := hook(s, "p1", "blocked", base.Add(7*time.Second)); applied {
		t.Fatal("hook suppressed expectation failed")
	}
}

func TestStoreReleasedDropsTransient(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p1", []byte("state=working"), base)
	// released mirrors process exit: it always passes and clears the
	// transient record.
	if st, applied := hook(s, "p1", "released", base.Add(time.Second)); !applied || st != "released" {
		t.Fatalf("released: %q applied=%v", st, applied)
	}
	// With the working record gone, the next hook event applies.
	if _, applied := hook(s, "p1", "idle", base.Add(2*time.Second)); !applied {
		t.Fatal("hook after released dropped")
	}
}

func TestStoreNoHeartbeatRequiredForNativeTransient(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p1", []byte("state=working"), base)
	// Even after a long-running job is silent, the protocol requires
	// no heartbeat. Only a real lifecycle transition ends its authority.
	for _, age := range []time.Duration{time.Minute, 3 * time.Minute, time.Hour} {
		if st, applied := hook(s, "p1", "idle", base.Add(age)); applied || st != "working" {
			t.Fatalf("false timeout after %s: %q applied=%v", age, st, applied)
		}
	}
}

func TestStoreInvalidBodyNoop(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p1", []byte("state=working"), base)
	if _, ok := s.ApplyProgramReport("p1", []byte("state=thinking"), base); ok {
		t.Fatal("invalid body accepted")
	}
	// The pane still shows the last valid state.
	if st := s.Display("p1"); st != "working" {
		t.Fatalf("display = %q", st)
	}
}

func TestVerifiedAgentEndRetiresOnlyInFlightOSCAndKeepsCompletedRecords(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p1", []byte("state=working:id=active:app=omp"), base)
	s.ApplyProgramReport("p1", []byte("state=done:id=finished:app=omp"), base)
	s.ApplyProgramReport("p1", []byte("state=blocked:id=child:app=omp"), base)
	if got := s.Display("p1"); got != "blocked" {
		t.Fatalf("setup status=%q", got)
	}
	// A simple Herdr/spool idle or Solco hook idle must not defeat OSC
	// until the provider itself emits an identity-verified agent_end.
	if _, applied := s.ApplySolcoHookStatus("p1", "idle", base); applied {
		t.Fatal("unverified hook overrode OSC status")
	}
	if got := s.ApplyVerifiedAgentEnd("p1"); got != "done" {
		t.Fatalf("run completion did not keep finished native record: %q", got)
	}
	if s.HasTransient("p1") {
		t.Fatal("finished run retained in-flight OSC")
	}
	// The next run legitimately reenters working.
	s.ApplyProgramReport("p1", []byte("state=working:id=active:app=omp"), base.Add(time.Second))
	if got := s.Display("p1"); got != "working" {
		t.Fatalf("new run status=%q", got)
	}
}

func TestStorePaneClosed(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p1", []byte("state=working"), base)
	s.PaneClosed("p1")
	// A fresh pane starts from whichever source speaks first; hooks are
	// no longer suppressed by the dead pane's records.
	if _, applied := hook(s, "p1", "idle", base.Add(time.Second)); !applied {
		t.Fatal("closed pane still suppresses hooks")
	}
	if st := s.Display("p1"); st != "idle" {
		t.Fatalf("display = %q", st)
	}
}

func TestOSCPriorityMultiRecordsAndHookOrigins(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p", []byte("state=working:app=claude-code"), base)
	s.ApplyProgramReport("p", []byte("state=blocked:id=child"), base.Add(time.Second))
	if got, ok := s.ApplyProgramReport("p", []byte("state=idle"), base.Add(2*time.Second)); !ok || got != "blocked" {
		t.Fatalf("root idle must preserve blocked child, got %q ok=%v", got, ok)
	}
	if got, applied := s.ApplySolcoHookStatus("p", "idle", base.Add(4*time.Minute)); applied || got != "blocked" {
		t.Fatalf("no heartbeat requirement, got %q applied=%v", got, applied)
	}
	if got, applied := s.ApplyHerdrHookStatus("p", "working", base.Add(5*time.Minute)); applied || got != "blocked" {
		t.Fatalf("Herdr must not override native record: %q %v", got, applied)
	}
	s.ApplyProgramReport("p", []byte("state=clear:id=child"), base.Add(6*time.Minute))
	if got, applied := s.ApplySolcoHookStatus("p", "working", base.Add(7*time.Minute)); !applied || got != "working" {
		t.Fatalf("Solco hook takes over idle native: %q %v", got, applied)
	}
	if got, applied := s.ApplyHerdrHookStatus("p", "blocked", base.Add(8*time.Minute)); applied || got != "working" {
		t.Fatalf("Herdr fallback overrode Solco: %q %v", got, applied)
	}
	if got := s.Display("p"); got != "working" {
		t.Fatalf("read path disagrees with source arbitration: %q", got)
	}
}

func TestOSC7501ControlAndSurvivors(t *testing.T) {
	s := NewStore()
	s.ApplyProgramReport("p", []byte("state=working:id=active"), base)
	s.ApplyProgramReport("p", []byte("state=done:id=finished"), base)
	s.ApplyControl("p", ControlPrompt)
	if got := s.Display("p"); got != "done" {
		t.Fatalf("prompt dropped completed record: %q", got)
	}
	s.ApplyProgramReport("p", []byte("state=blocked"), base)
	s.ApplyControl("p", ControlReset)
	if got := s.Display("p"); got != "" {
		t.Fatalf("full reset retained records: %q", got)
	}
}
