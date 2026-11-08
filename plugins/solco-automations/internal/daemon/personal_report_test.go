package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The daemon's production seam must never launch/submit into a blocked,
// working, mismatched or unreadable Herdr OMP pane.
func fakeReportHerdr(t *testing.T, response string, failRead bool) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	args := filepath.Join(dir, "args")
	shell := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HERDR_TEST_ARGS\"\n" +
		"case \"$*\" in\n" +
		"  *'pane read'*)\n" +
		"    if [ \"$HERDR_TEST_READ_FAIL\" = 1 ]; then exit 1; fi\n" +
		"    printf 'OMP normal composer\\n';;\n" +
		"  *) printf '%s\\n' \"$HERDR_TEST_RESPONSE\";;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(shell), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("HERDR_TEST_ARGS", args)
	t.Setenv("HERDR_TEST_RESPONSE", response)
	if failRead {
		t.Setenv("HERDR_TEST_READ_FAIL", "1")
	} else {
		t.Setenv("HERDR_TEST_READ_FAIL", "0")
	}
	return args
}
func TestCoordinatorReportRequiresExactIdleOMPInHerdr(t *testing.T) {
	for _, tc := range []struct {
		name, kind, status, pane string
		ready                    bool
		allow                    bool
	}{
		{"eligible", "omp", "idle", "w1:p1", true, true},
		{"working user turn", "omp", "working", "w1:p1", true, false},
		{"blocked dialog", "omp", "blocked", "w1:p1", true, false},
		{"interactive not ready", "omp", "idle", "w1:p1", false, false},
		{"different agent", "claude-code", "idle", "w1:p1", true, false},
		{"different pane", "omp", "idle", "other", true, false},
		{"unknown status", "omp", "unknown", "w1:p1", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready := "false"
			if tc.ready {
				ready = "true"
			}
			response := `{"result":{"type":"agent_info","agent":{"pane_id":"` + tc.pane + `","agent":"` + tc.kind + `","agent_status":"` + tc.status + `","interactive_ready":` + ready + `}}}`
			fakeReportHerdr(t, response, false)
			if got := coordinatorReportReady("default", "w1:p1"); got != tc.allow {
				t.Fatalf("readiness = %t, want %t", got, tc.allow)
			}
		})
	}
	t.Run("Herdr unreachable", func(t *testing.T) {
		fakeReportHerdr(t, `{"result":{}}`, false)
		if coordinatorReportReady("default", "w1:p1") {
			t.Fatal("missing Herdr identity allowed automatic report")
		}
	})
}

func TestCoordinatorReportFailsClosedOnUncertainPaneRead(t *testing.T) {
	fakeReportHerdr(t, `{"result":{"type":"agent_info"}}`, true)
	if _, blocked := coordinatorReportDialog("default", "w1:p1"); !blocked {
		t.Fatal("unreadable trust/composer screen allowed report submission")
	}
}
func TestCoordinatorReportReceiptDoesNotWaitForWorkingState(t *testing.T) {
	args := fakeReportHerdr(t, `{"result":{"type":"agent_prompted","agent":{"pane_id":"w1:p1"}}}`, false)
	if err := submitCoordinatorReport("default", "w1:p1", "Solco progress"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{"--session", "default", "agent", "prompt", "w1:p1", "Solco progress"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected --wait or wrong target: %q", argv)
	}
}
