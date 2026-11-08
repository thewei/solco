package herdr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalTaskResultReadsTheExitMarker(t *testing.T) {
	dir := t.TempDir()
	task := &TerminalTask{dir: dir, marker: filepath.Join(dir, "exit")}
	if _, done := task.Result(); done {
		t.Fatal("no marker yet: the task must read as still running")
	}
	for body, want := range map[string]int{"0": 0, "2\n": 2, "130": 130, "garbage": 1} {
		if err := os.WriteFile(task.marker, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		code, done := task.Result()
		if !done || code != want {
			t.Errorf("marker %q → (%d, %v), want (%d, true)", body, code, done, want)
		}
	}
	task.Cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Cleanup must remove the task directory, stat err = %v", err)
	}
	task.Cleanup() // idempotent
}

// The script's own logic — title, command, marker — is run for real with a
// stub command; only the final "press any key" pause needs a tty, so it is
// cut off before running.
func TestTerminalScriptRecordsTheExitStatus(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	for _, tc := range []struct {
		command string
		want    int
	}{{"true", 0}, {"exit 7", 7}} {
		dir := t.TempDir()
		marker := filepath.Join(dir, "exit")
		script := terminalScript("Adding o'brien to Herdr…", tc.command, marker)
		if i := strings.Index(script, "printf 'Press any key"); i >= 0 {
			script = script[:i]
		}
		// `exit 7` must not end the script before the marker is written, so it
		// runs in a subshell like a real command would.
		script = strings.Replace(script, "\n"+tc.command+"\n", "\n("+tc.command+")\n", 1)
		path := filepath.Join(dir, "task.zsh")
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("zsh", path)
		cmd.Env = append(os.Environ(), "TERM=dumb")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
		task := &TerminalTask{dir: dir, marker: marker}
		if code, done := task.Result(); !done || code != tc.want {
			t.Errorf("%q → (%d, %v), want (%d, true)", tc.command, code, done, tc.want)
		}
	}
}

func TestMachineAddCommand(t *testing.T) {
	cases := []struct {
		label, session, want string
	}{
		{"", "", `'/opt/herdr' machine add 'deploy@workbox'`},
		{"Build box", "", `'/opt/herdr' machine add --label 'Build box' 'deploy@workbox'`},
		{"", "agents", `'/opt/herdr' machine add --remote-session 'agents' 'deploy@workbox'`},
		{" Build box ", " agents ", `'/opt/herdr' machine add --label 'Build box' --remote-session 'agents' 'deploy@workbox'`},
		{"o'brien", "", `'/opt/herdr' machine add --label 'o'\''brien' 'deploy@workbox'`},
	}
	for _, tc := range cases {
		if got := machineAddCommand("/opt/herdr", " deploy@workbox ", tc.label, tc.session); got != tc.want {
			t.Errorf("machineAddCommand(label=%q, session=%q)\n got  %s\n want %s", tc.label, tc.session, got, tc.want)
		}
	}
}

func TestMachineSetupCommand(t *testing.T) {
	if got, want := machineSetupCommand("/opt/herdr", Machine{Target: "workbox", Session: "default"}), `'/opt/herdr' --remote 'workbox'`; got != want {
		t.Errorf("default session = %s, want %s", got, want)
	}
	if got, want := machineSetupCommand("/opt/herdr", Machine{Target: "workbox"}), `'/opt/herdr' --remote 'workbox'`; got != want {
		t.Errorf("no session = %s, want %s", got, want)
	}
	if got, want := machineSetupCommand("/opt/herdr", Machine{Target: "workbox", Session: "agents"}), `'/opt/herdr' --remote 'workbox' --session 'agents'`; got != want {
		t.Errorf("named session = %s, want %s", got, want)
	}
}

func TestStartMachineTasksRejectBadTargetsBeforeOpeningAnything(t *testing.T) {
	m := NewManager()
	if _, err := m.StartMachineAdd("-oProxyCommand=x", "", ""); err == nil {
		t.Fatal("StartMachineAdd accepted an option as a target")
	}
	if _, err := m.StartMachineSignIn("two words"); err == nil {
		t.Fatal("StartMachineSignIn accepted a target with spaces")
	}
	if _, err := m.StartMachineSetup(Machine{Label: "x", Target: ""}); err == nil {
		t.Fatal("StartMachineSetup accepted an empty target")
	}
}
