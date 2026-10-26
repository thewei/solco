package workboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientArgv(t *testing.T) {
	var calls [][]string
	c := NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, args)
		if args[0] == "list" {
			return realList, nil
		}
		return realDetailsTodo, nil
	})
	if _, err := c.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Details(context.Background(), "repo/1"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls[0], " "); got != "list --json" {
		t.Fatalf("list argv = %q", got)
	}
	if got := strings.Join(calls[1], " "); got != "details repo/1 --json" {
		t.Fatalf("details argv = %q", got)
	}
}

func TestClientRefusesOptionLikeOrEmptyIDs(t *testing.T) {
	c := NewClientWithExec(func(context.Context, ...string) (string, error) {
		t.Fatal("no CLI call may happen for a bad id")
		return "", nil
	})
	for _, id := range []string{"", "  ", "--archived", "-x"} {
		if _, err := c.Details(context.Background(), id); err == nil {
			t.Fatalf("id %q must be refused", id)
		}
	}
}

func TestClientSurfacesExecErrors(t *testing.T) {
	boom := errors.New("boom")
	c := NewClientWithExec(func(context.Context, ...string) (string, error) { return "", boom })
	if _, err := c.List(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("List err = %v", err)
	}
	if _, err := c.Details(context.Background(), "a/1"); !errors.Is(err, boom) {
		t.Fatalf("Details err = %v", err)
	}
}

func TestEmptyRootFailsEveryCall(t *testing.T) {
	c := NewClient("", "")
	if _, err := c.List(context.Background()); err == nil {
		t.Fatal("a client without a plugin root must not run anything")
	}
}

// The CLI runs with the session's socket and without any pane identity, even
// when Solco itself was launched from a Herdr pane.
func TestCLIRunsAsTheHumanOnTheSessionSocket(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "p_leaked")
	t.Setenv("HERDR_PLUGIN_ID", "someone-else")
	t.Setenv("HERDR_SOCKET_PATH", "/wrong.sock")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "echo \"pane=${HERDR_PANE_ID:-none} plugin=${HERDR_PLUGIN_ID:-none} socket=$HERDR_SOCKET_PATH args=$*\"\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := cliExec(root, "")(context.Background(), "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pane=none plugin=none") || !strings.Contains(out, "args=list --json") {
		t.Fatalf("identity leaked or argv lost: %q", out)
	}
	if strings.Contains(out, "/wrong.sock") || !strings.Contains(out, "socket=/") {
		t.Fatalf("socket not replaced by the session's: %q", out)
	}
}

// recorder fakes the plugin CLI from the contract samples: list/details come
// from testdata, every other verb is recorded.
func recorder(t *testing.T, sampleName string, ran *[]string) *Client {
	t.Helper()
	read := func(suffix string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", sampleName+"."+suffix+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	return NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "list":
			return read("list"), nil
		case "details":
			return read("details"), nil
		}
		*ran = append(*ran, strings.Join(args, " "))
		return "", nil
	})
}

func TestApproveRunsOnlyForAReadyTaskAndNeverAnyway(t *testing.T) {
	var ran []string
	if err := recorder(t, "approval_local", &ran).Approve(context.Background(), "proj/1"); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "approve proj/1" {
		t.Fatalf("ran = %q (must be a plain approve, no --anyway)", ran)
	}
}

func TestApproveRefusesWhatIsNotReadyAtClickTime(t *testing.T) {
	for _, name := range []string{"approval_pr_failed_at_approve", "approval_pr_failed_polled"} {
		var ran []string
		err := recorder(t, name, &ran).Approve(context.Background(), "proj/1")
		if !errors.Is(err, ErrNotReady) || len(ran) != 0 {
			t.Fatalf("%s: err=%v ran=%q, want ErrNotReady and no approve", name, err, ran)
		}
	}
	// A task id the board no longer lists is refused too.
	var ran []string
	if err := recorder(t, "approval_local", &ran).Approve(context.Background(), "proj/99"); !errors.Is(err, ErrNotReady) || len(ran) != 0 {
		t.Fatalf("vanished task: err=%v ran=%q", err, ran)
	}
}

func TestWriteVerbsArgv(t *testing.T) {
	var ran []string
	c := recorder(t, "approval_local", &ran)
	ctx := context.Background()
	for _, f := range []func(context.Context, string) error{c.SendBack, c.Retry, c.Fix, c.Delegate, c.Stop, c.Archive} {
		if err := f(ctx, "proj/1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Add(ctx, "Ship it", "/work/repo", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(ctx, "Plain", "", false); err != nil {
		t.Fatal(err)
	}
	want := "review proj/1|retry proj/1|fix proj/1|delegate proj/1|stop proj/1|archive proj/1|add Ship it --repo /work/repo --delegate|add Plain"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
}

func TestWriteVerbsRefuseOptionLikeInput(t *testing.T) {
	c := NewClientWithExec(func(context.Context, ...string) (string, error) {
		t.Fatal("no CLI call may happen for bad input")
		return "", nil
	})
	ctx := context.Background()
	for _, id := range []string{"", " ", "--anyway", "-x"} {
		for _, f := range []func(context.Context, string) error{c.Approve, c.SendBack, c.Retry, c.Fix, c.Delegate, c.Stop, c.Archive} {
			if f(ctx, id) == nil {
				t.Fatalf("id %q must be refused", id)
			}
		}
	}
	for _, args := range [][2]string{{"", "r"}, {"--delegate", "r"}, {"t", "--machine"}} {
		if c.Add(ctx, args[0], args[1], false) == nil {
			t.Fatalf("add %q must be refused", args)
		}
	}
}

func TestTellVerbsArgvAndRefusals(t *testing.T) {
	var ran []string
	c := recorder(t, "approval_local", &ran)
	ctx := context.Background()
	if err := c.Tell(ctx, "proj/1", "  keep going "); err != nil {
		t.Fatal(err)
	}
	if err := c.CoordinatorTell(ctx, "proj", "add a dark mode", false); err != nil {
		t.Fatal(err)
	}
	if err := c.CoordinatorTell(ctx, "proj", "do it now", true); err != nil {
		t.Fatal(err)
	}
	want := "tell proj/1 keep going|coordinator --project proj tell add a dark mode|coordinator --project proj tell do it now --delegate"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
	ran = nil
	bad := NewClientWithExec(func(context.Context, ...string) (string, error) {
		t.Fatal("no CLI call may happen for bad input")
		return "", nil
	})
	for _, f := range []func() error{
		func() error { return bad.Tell(ctx, "", "x") },
		func() error { return bad.Tell(ctx, "--anyway", "x") },
		func() error { return bad.Tell(ctx, "p/1", "") },
		func() error { return bad.Tell(ctx, "p/1", "--delegate") },
		func() error { return bad.CoordinatorTell(ctx, "", "x", false) },
		func() error { return bad.CoordinatorTell(ctx, "-p", "x", false) },
		func() error { return bad.CoordinatorTell(ctx, "p", "  ", false) },
		func() error { return bad.CoordinatorTell(ctx, "p", "--machine", false) },
	} {
		if f() == nil {
			t.Fatal("bad input must be refused")
		}
	}
}

func TestApprovePlanOnlyApprovesAPlan(t *testing.T) {
	var calls []string
	list := stageRows
	c := NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "list" {
			return list, nil
		}
		return "", nil
	})
	ctx := context.Background()
	if err := c.ApprovePlan(ctx, "r/1"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, "|"); got != "list --json|approve r/1" {
		t.Fatalf("argv = %q", got)
	}
	calls = nil
	for _, id := range []string{"r/2", "r/3", "r/9"} { // a pick, a queued task, a missing one: never `approve`
		if err := c.ApprovePlan(ctx, id); !errors.Is(err, ErrNotReady) {
			t.Fatalf("%s: err = %v, want ErrNotReady", id, err)
		}
	}
	list = `[{"id":"r/1","state":"blocked","kind":"permission","stage":"approval","msg":"Approve to merge? Did v1."}]`
	calls = nil
	if err := c.ApprovePlan(ctx, "r/1"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("a task that moved on to merge approval must not be approved as a plan: %v", err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "approve") {
			t.Fatalf("ran %q", call)
		}
	}
	if c.ApprovePlan(ctx, "-x") == nil {
		t.Fatal("option-shaped id refused")
	}
}
