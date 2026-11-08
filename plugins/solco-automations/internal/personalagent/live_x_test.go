//go:build live

package personalagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// LIVE acceptance (AC-02/AC-04, real X.com, read-only). The user authorised a
// read-only look at their own logged-in timeline on 2026-11-05 ("把我时间线的
// 消息去重总结"). Real OMP + real ego-browser + their real browser session, but an
// ISOLATED Herdr server (own socket under a temp XDG dir), own DB and workdir:
// the user's live Herdr, Solco data and Assistants are never touched.
//
//	SOLCO_LIVE_X=1 go test -tags live -run TestLiveXTimelineDigest -v -timeout 20m ./internal/personalagent
func TestLiveXTimelineDigest(t *testing.T) {
	if os.Getenv("SOLCO_LIVE_X") != "1" {
		t.Skip("set SOLCO_LIVE_X=1: this reads the user's real X.com timeline (authorised, read-only)")
	}
	herdr, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr not installed")
	}
	root, err := os.MkdirTemp("/tmp", "sx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	xdg := filepath.Join(root, "xdg")
	_ = os.MkdirAll(filepath.Join(xdg, "herdr"), 0o755)
	cfg := filepath.Join(root, "herdr.toml")
	_ = os.WriteFile(cfg, []byte("data_dir = \""+filepath.Join(root, "herdr-data")+"\"\n"), 0o644)
	env := map[string]string{
		"XDG_CONFIG_HOME": xdg, "HERDR_CONFIG_PATH": cfg,
		"HERDR_PLUGIN_STATE_DIR": filepath.Join(root, "state"), "HERDR_PLUGIN_CONFIG_DIR": filepath.Join(root, "conf"),
		"SOLCO_ASSISTANTS_WORKDIR": filepath.Join(root, "assistants-work"), "SOLCO_ASSISTANTS_DB": filepath.Join(root, "state", "a.sqlite"),
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	for _, k := range []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_SESSION", "HERDR_WORKSPACE_ID"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	srv := exec.Command(herdr, "server")
	srv.Env = os.Environ()
	logf, _ := os.Create(filepath.Join(root, "herdr.log"))
	srv.Stdout, srv.Stderr = logf, logf
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop := exec.Command(herdr, "server", "stop")
		stop.Env = os.Environ()
		_ = stop.Run()
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	})
	sock := filepath.Join(xdg, "herdr", "herdr.sock")
	for i := 0; ; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if i > 100 {
			t.Fatalf("isolated herdr did not start (socket %s)", sock)
		}
		time.Sleep(100 * time.Millisecond)
	}

	s, err := Open(DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := mustAssistant(t, s, "live-x-a", "Ada")
	brief := "Use the ego-browser skill (read its guide first with `ego-browser skill`) to open https://x.com/home in the user's already signed-in ego-browser session. " +
		"Read the home timeline (For you, then Following if present) over about five screens of scrolling. Collect posts with author, text, link (https://x.com/<user>/status/<id>) and engagement. " +
		"Deduplicate: the same post, a repost or quote of the same status, or the same linked article counts once. Then write a short summary grouped by topic, each item with its link, and say how many raw posts collapsed into how many distinct ones. " +
		"STRICTLY READ-ONLY: do not post, reply, like, repost, bookmark, follow, mute, message, or change any setting. Never print cookies, tokens or account identifiers. " +
		"If the page is not reachable or the user is not signed in, say exactly that and stop; do not try another browser or tool."
	task, _, err := s.CreateTask("live-x-t", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "X timeline digest", Brief: brief, RequiredSkills: []string{"ego-browser"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSkill(a.ID, "ego-browser"); err != nil { // the user's explicit yes, 2026-11-05
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "live", Timeout: 15 * time.Minute}
	run, err := d.RunTask(t.Context(), task.ID)
	t.Logf("run=%+v err=%v", run, err)
	res, ok, rerr := s.RunResult(run.ID)
	t.Logf("result ok=%v err=%v\nSUMMARY:\n%s\nSKILLS=%v TOOLS=%v", ok, rerr, res.Summary, res.Skills, res.Tools)
	for _, src := range res.Sources {
		t.Logf("source grounded=%v %s", src.Grounded, src.URL)
	}
	if out := os.Getenv("SOLCO_LIVE_OUT"); out != "" {
		_ = os.WriteFile(out, []byte(strings.TrimSpace(res.Summary)+"\n"), 0o600)
	}
	if run.Status != RunDone {
		t.Fatalf("run = %s error=%q", run.Status, run.Error)
	}
	if len(res.Skills) == 0 || res.Skills[0] != "ego-browser" {
		t.Fatalf("ego-browser was not loaded by the worker: %v", res.Skills)
	}
}
