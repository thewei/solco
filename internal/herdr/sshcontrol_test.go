package herdr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPathIsShortStableAndPerTarget(t *testing.T) {
	home := shortHome(t)
	t.Setenv("HOME", home)

	a := ControlPath("deploy@workbox")
	if a != ControlPath(" deploy@workbox ") {
		t.Fatal("ControlPath must ignore surrounding whitespace")
	}
	if a == ControlPath("deploy@other") {
		t.Fatal("different targets must not share a control socket")
	}
	if dir := filepath.Dir(a); dir != filepath.Join(home, ".solco", "ssh-ctl") {
		t.Fatalf("control dir = %q", dir)
	}
	// A target of any length must not lengthen the path: unix socket paths
	// are limited to ~104 bytes on macOS and ssh appends a 17-byte suffix.
	if long := ControlPath(strings.Repeat("x", 300)); len(long) != len(a) {
		t.Fatalf("path length depends on the target: %d vs %d", len(long), len(a))
	}
}

func TestReuseArgsNeverCreateAMaster(t *testing.T) {
	t.Setenv("HOME", shortHome(t))
	args := strings.Join(reuseArgs("deploy@workbox"), " ")
	if !strings.Contains(args, "ControlMaster=no") {
		t.Fatalf("a non-interactive call must never create a master: %s", args)
	}
	if !strings.Contains(args, "ControlPath="+ControlPath("deploy@workbox")) {
		t.Fatalf("missing control path: %s", args)
	}
}

// fakeSSH writes an `ssh` stand-in that logs its argv. exit codes for `-O
// check` are set by checkRC; everything else succeeds.
func fakeSSH(t *testing.T, checkRC int) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	path = filepath.Join(dir, "ssh")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nif [ \"$1\" = \"-O\" ] && [ \"$2\" = \"check\" ]; then exit %d; fi\nexit 0\n", shellQuote(log), checkRC)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func runShell(t *testing.T, script string) {
	t.Helper()
	if out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

func TestSignInShellReplacesADeadControlSocketAndMakesAMaster(t *testing.T) {
	t.Setenv("HOME", shortHome(t))
	target := "deploy@workbox"
	control := ControlPath(target)
	if err := os.MkdirAll(filepath.Dir(control), 0o700); err != nil {
		t.Fatal(err)
	}
	// A socket file nobody listens on: what a killed master leaves behind.
	listener, err := net.Listen("unix", control)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	if _, err := os.Stat(control); err != nil {
		t.Fatalf("stale socket fixture missing: %v", err)
	}

	sshPath, log := fakeSSH(t, 1) // -O check: no live master
	runShell(t, signInShell(sshPath, target))

	if _, err := os.Stat(control); !os.IsNotExist(err) {
		t.Fatalf("the dead control socket must be removed before a new master is made, stat err = %v", err)
	}
	calls := readLines(t, log)
	if len(calls) != 2 {
		t.Fatalf("want a check then a sign-in, got %q", calls)
	}
	if !strings.HasPrefix(calls[0], "-O check -o ControlPath="+control+" "+target) {
		t.Fatalf("first call = %q", calls[0])
	}
	for _, want := range []string{"-o ControlMaster=yes", "-o ControlPath=" + control, "-o ControlPersist=10m", "-o AddKeysToAgent=yes", target + " true"} {
		if !strings.Contains(calls[1], want) {
			t.Fatalf("sign-in call lacks %q: %q", want, calls[1])
		}
	}
	if info, err := os.Stat(filepath.Dir(control)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("control dir must be private: %v %v", info, err)
	}
}

func TestSignInShellKeepsALiveMaster(t *testing.T) {
	t.Setenv("HOME", shortHome(t))
	sshPath, log := fakeSSH(t, 0) // -O check: master is live
	runShell(t, signInShell(sshPath, "deploy@workbox"))
	if calls := readLines(t, log); len(calls) != 1 || !strings.HasPrefix(calls[0], "-O check") {
		t.Fatalf("an existing sign-in must not be redone, calls = %q", calls)
	}
}

func TestSignInShellQuotesTheTarget(t *testing.T) {
	t.Setenv("HOME", shortHome(t))
	script := signInShell("/usr/bin/ssh", "o'brien@host")
	if !strings.Contains(script, `'o'\''brien@host'`) {
		t.Fatalf("target must be shell-quoted: %s", script)
	}
}

func TestClassifyFailure(t *testing.T) {
	cases := []struct {
		message string
		want    FailureKind
	}{
		{"wilson@127.0.0.1: Permission denied (publickey).", FailureAuth},
		{"remote platform detection failed: wilson@127.0.0.1: Permission denied (publickey).", FailureAuth},
		{"auth required", FailureAuth},
		{"Received disconnect: Too many authentication failures", FailureAuth},
		{"Host key verification failed.", FailureHostKey},
		{"@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@", FailureHostKey},
		{"ssh: connect to host 127.0.0.1 port 22222: Connection refused", FailureNetwork},
		{"ssh: Could not resolve hostname sfx-local: nodename nor servname provided, or not known", FailureNetwork},
		{"ssh: connect to host 10.0.0.9 port 22: Operation timed out", FailureNetwork},
		{"kex_exchange_identification: Connection closed by remote host", FailureNetwork},
		{"channel 2: open failed: connect failed: No such file or directory", FailureServer},
		// The remote connect failure also reads "Connection refused": it must
		// stay the remote Herdr's problem, not the network's.
		{"channel 2: open failed: connect failed: Connection refused", FailureServer},
		{"something nobody anticipated", FailureOther},
		{"", FailureOther},
	}
	for _, tc := range cases {
		if got := ClassifyFailure(tc.message); got != tc.want {
			t.Errorf("ClassifyFailure(%q) = %s, want %s", tc.message, got, tc.want)
		}
	}
}

func TestFailureOfReadsTheBridgeKindThroughWrapping(t *testing.T) {
	bridge := newBridgeError(FailureServer, errors.New("no answer"), "box: remote Herdr did not come up")
	wrapped := fmt.Errorf("switch: %w", bridge)
	if got := FailureOf(wrapped); got != FailureServer {
		t.Fatalf("FailureOf(wrapped bridge error) = %s", got)
	}
	if got := FailureOf(errors.New("Permission denied (publickey)")); got != FailureAuth {
		t.Fatalf("plain errors are classified by text, got %s", got)
	}
	if got := FailureOf(nil); got != FailureOther {
		t.Fatalf("FailureOf(nil) = %s", got)
	}
	if !errors.Is(wrapped, bridge.err) {
		t.Fatal("BridgeError must unwrap to its cause")
	}
}

func TestValidateTarget(t *testing.T) {
	for _, ok := range []string{"workbox", "deploy@workbox", "ssh://deploy@10.0.0.9:2222", "gpu-box.internal"} {
		if err := ValidateTarget(ok); err != nil {
			t.Errorf("ValidateTarget(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "   ", "-oProxyCommand=x", "two words", "tab\there", "line\nbreak"} {
		if err := ValidateTarget(bad); err == nil {
			t.Errorf("ValidateTarget(%q) accepted a bad target", bad)
		}
	}
}

func TestWaitForward(t *testing.T) {
	const tick = 5 * time.Millisecond
	ctx := context.Background()
	never := func() error { return errors.New("no answer") }

	t.Run("answers on the third probe", func(t *testing.T) {
		calls := 0
		probe := func() error {
			calls++
			if calls < 3 {
				return errors.New("not yet")
			}
			return nil
		}
		shared, err := waitForward(ctx, probe, make(chan error), tick, time.Second)
		if err != nil || shared {
			t.Fatalf("shared=%v err=%v", shared, err)
		}
	})

	t.Run("ssh exiting with success is the shared hand-over", func(t *testing.T) {
		exited := make(chan error, 1)
		exited <- nil
		calls := 0
		probe := func() error {
			calls++
			if calls < 4 {
				return errors.New("not yet")
			}
			return nil
		}
		shared, err := waitForward(ctx, probe, exited, tick, time.Second)
		if err != nil || !shared {
			t.Fatalf("a mux client exiting 0 must keep the wait going: shared=%v err=%v", shared, err)
		}
	})

	t.Run("ssh exiting with an error ends the wait at once", func(t *testing.T) {
		exited := make(chan error, 1)
		exited <- errors.New("exit status 255")
		start := time.Now()
		_, err := waitForward(ctx, never, exited, tick, 5*time.Second)
		if !errors.Is(err, errSSHExited) {
			t.Fatalf("err = %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("a failed ssh must not wait out the deadline")
		}
	})

	t.Run("a forward that never answers times out with the probe error", func(t *testing.T) {
		_, err := waitForward(ctx, never, make(chan error), tick, 40*time.Millisecond)
		if err == nil || errors.Is(err, errSSHExited) || err.Error() != "no answer" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("cancelling ends the wait", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := waitForward(cancelled, never, make(chan error), tick, 5*time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}
