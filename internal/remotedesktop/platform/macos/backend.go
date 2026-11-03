// Package macos is the macOS Remote Desktop backend. It supervises the native Swift helper
// (solco-remote-helper, embedded at Contents/Helpers/): launches it on demand, authenticates it over
// the per-user Unix socket (package helperipc), and turns its capture/input/permission commands into
// the platform interfaces. All ScreenCaptureKit, VideoToolbox and CGEvent work lives in the helper;
// this package contains no cgo and no AppKit.
//
// [INPUT]: platform, helperipc, protocol, os/exec, codesign (bundled copy only)
// [OUTPUT]: Backend (New/Options), Locate, KeyCode
// [POS]: the process boundary — a helper crash ends the session, never the app
package macos

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/helperipc"
	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// Options configures the backend; every field is optional.
type Options struct {
	// Executable is the running app's path (os.Executable) used to find Contents/Helpers.
	Executable string
	// WorkDir is the working directory used by the development search.
	WorkDir string
	Getenv  func(string) string
	// Verify checks a bundled helper's signature before launch; nil = `codesign --verify --strict`.
	Verify func(path string) error
	Logger *slog.Logger
}

// Backend implements platform.Backend.
type Backend struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	verified map[string]verifiedStamp
}

type verifiedStamp struct {
	mod  time.Time
	size int64
	err  error
}

var _ platform.Backend = (*Backend)(nil)

// New builds the backend; it launches nothing.
func New(opts Options) *Backend {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Executable == "" {
		opts.Executable, _ = os.Executable()
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}
	if opts.Verify == nil {
		opts.Verify = codesignVerify
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Backend{opts: opts, log: log, verified: map[string]verifiedStamp{}}
}

func (b *Backend) ID() string { return "macos" }

// resolve finds the helper and, for the bundled copy, checks its signature (cached per file stamp).
func (b *Backend) resolve() (string, error) {
	loc, ok := Locate(b.opts.Executable, b.opts.WorkDir, b.opts.Getenv)
	if !ok {
		return "", errors.New("remote desktop helper not found")
	}
	if !loc.Bundled {
		return loc.Path, nil
	}
	st, err := os.Stat(loc.Path)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if v, ok := b.verified[loc.Path]; ok && v.mod.Equal(st.ModTime()) && v.size == st.Size() {
		return loc.Path, v.err
	}
	verr := b.opts.Verify(loc.Path)
	b.verified[loc.Path] = verifiedStamp{mod: st.ModTime(), size: st.Size(), err: verr}
	if verr != nil {
		return "", fmt.Errorf("remote desktop helper signature invalid: %w", verr)
	}
	return loc.Path, nil
}

func (b *Backend) Available() (bool, string) {
	if _, err := b.resolve(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func codesignVerify(path string) error {
	return exec.Command("/usr/bin/codesign", "--verify", "--strict", path).Run()
}

// oneShot runs a helper subcommand that prints one JSON document.
func (b *Backend) oneShot(ctx context.Context, out any, args ...string) error {
	path, err := b.resolve()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = helperEnv()
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("helper %s: %w", args[0], err)
	}
	return json.Unmarshal(raw, out)
}

func (b *Backend) Permissions(ctx context.Context) (protocol.PermissionStatus, error) {
	var st protocol.PermissionStatus
	err := b.oneShot(ctx, &st, "permissions")
	return st, err
}

func (b *Backend) RequestPermission(ctx context.Context, kind protocol.PermissionKind) (protocol.PermissionStatus, error) {
	arg := "screen"
	if kind == protocol.PermissionAccessibility {
		arg = "accessibility"
	}
	var st protocol.PermissionStatus
	err := b.oneShot(ctx, &st, "permissions", "--request", arg)
	return st, err
}

func helperEnv() []string {
	env := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, k := range []string{"HOME", "TMPDIR", "USER", "LANG"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// Open launches the helper for one session and waits for its authenticated hello.
func (b *Backend) Open(ctx context.Context) (platform.Capturer, error) {
	path, err := b.resolve()
	if err != nil {
		return nil, protocol.Errf(protocol.CodeUnavailable, "%v", err)
	}
	srv, err := helperipc.Listen()
	if err != nil {
		return nil, protocol.Errf(protocol.CodeHelperFailed, "listen: %v", err)
	}
	cmd := exec.Command(path, "serve", "--socket", srv.Path())
	cmd.Env = append(helperEnv(), "SOLCO_RD_TOKEN="+srv.Token())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		_ = srv.Close()
		return nil, protocol.Errf(protocol.CodeHelperFailed, "launch: %v", err)
	}
	c := &capturer{
		cmd: cmd, log: b.log,
		frames: make(chan platform.Frame, 8), events: make(chan platform.Event, 16),
		done: make(chan struct{}), procDone: make(chan struct{}),
	}
	go c.pumpLogs(stderr)
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.exitErr = err
		c.mu.Unlock()
		close(c.procDone)
		_ = srv.Close() // unblocks a pending Accept when the helper died before saying hello
		if c.conn != nil {
			_ = c.conn.Close()
		}
	}()

	type accepted struct {
		conn *helperipc.Conn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		conn, err := srv.Accept(cmd.Process.Pid)
		ch <- accepted{conn, err}
	}()
	select {
	case a := <-ch:
		if a.err != nil {
			c.kill()
			_ = srv.Close()
			return nil, protocol.Errf(protocol.CodeHelperFailed, "%v", a.err)
		}
		c.conn = a.conn
	case <-ctx.Done():
		c.kill()
		_ = srv.Close()
		return nil, ctx.Err()
	}
	_ = srv.Close() // exactly one helper connects
	b.log.Info("remote desktop helper connected", "version", c.conn.Hello.HelperVersion, "pid", c.conn.Hello.PID)
	go c.forward()
	return c, nil
}

// capturer is one launched helper.
type capturer struct {
	cmd  *exec.Cmd
	conn *helperipc.Conn
	log  *slog.Logger

	frames chan platform.Frame
	events chan platform.Event
	done   chan struct{}

	procDone chan struct{}
	mu       sync.Mutex
	exitErr  error
	closed   bool
}

var _ platform.Capturer = (*capturer)(nil)

func (c *capturer) pumpLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 8192)
	for sc.Scan() {
		c.log.Info("remote desktop helper", "line", sc.Text())
	}
}

// forward converts helper frames/events to platform types until the link ends.
func (c *capturer) forward() {
	defer func() {
		close(c.done)
		close(c.frames)
		close(c.events)
	}()
	video, events := c.conn.Video(), c.conn.Events()
	for video != nil || events != nil {
		select {
		case f, ok := <-video:
			if !ok {
				video = nil
				continue
			}
			select {
			case c.frames <- platform.Frame{CaptureID: f.CaptureID, Keyframe: f.Keyframe, PTS: f.PTS, Data: f.Data}:
			default:
				// The session loop is behind: drop and ask for a keyframe rather than queue latency.
				c.conn.Dropped.Add(1)
				go c.RequestKeyframe()
			}
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if ev, ok := translateEvent(e); ok {
				select {
				case c.events <- ev:
				default:
				}
			}
		}
	}
}

func translateEvent(e helperipc.Event) (platform.Event, bool) {
	switch e.Name {
	case helperipc.EventDisplaysChanged:
		var d helperipc.DisplaysResult
		if json.Unmarshal(e.Data, &d) != nil {
			return platform.Event{}, false
		}
		return platform.Event{Kind: platform.EventDisplaysChanged, Displays: d.Displays}, true
	case helperipc.EventCaptureStopped:
		var s helperipc.CaptureStopped
		if json.Unmarshal(e.Data, &s) != nil {
			return platform.Event{}, false
		}
		ev := platform.Event{Kind: platform.EventCaptureStopped, CaptureID: s.CaptureID}
		if s.Error != nil {
			ev.Err = translateError(s.Error)
		}
		return ev, true
	case helperipc.EventError:
		var pe helperipc.ProtocolError
		if json.Unmarshal(e.Data, &pe) != nil {
			return platform.Event{}, false
		}
		return platform.Event{Kind: platform.EventError, Err: translateError(&pe)}, true
	}
	return platform.Event{}, false
}

// translateError maps the helper's codes onto protocol codes.
func translateError(pe *helperipc.ProtocolError) *protocol.Error {
	switch pe.Code {
	case helperipc.CodePermissionScreenRecording, helperipc.CodePermissionAccessibility:
		return &protocol.Error{Code: protocol.CodePermissionRequired, Message: pe.Code}
	case helperipc.CodeDisplayNotFound:
		return &protocol.Error{Code: protocol.CodeDisplayNotFound}
	case helperipc.CodeBadRequest, helperipc.CodeUnknownOp:
		return &protocol.Error{Code: protocol.CodeBadRequest, Message: pe.Code}
	case helperipc.CodeCaptureFailed, helperipc.CodeEncoderFailed, helperipc.CodeEncoderUnavailable, helperipc.CodeNotCapturing:
		return &protocol.Error{Code: protocol.CodeCaptureFailed, Message: pe.Code}
	}
	return &protocol.Error{Code: protocol.CodeHelperFailed, Message: pe.Code}
}

func asProtocolErr(err error) error {
	var pe *helperipc.ProtocolError
	if errors.As(err, &pe) {
		return translateError(pe)
	}
	if errors.Is(err, helperipc.ErrClosed) {
		return protocol.Errf(protocol.CodeHelperFailed, "helper gone")
	}
	return err
}

func (c *capturer) Displays(ctx context.Context) ([]protocol.DisplayInfo, error) {
	var out helperipc.DisplaysResult
	if err := c.conn.Call(ctx, helperipc.OpDisplaysList, nil, &out); err != nil {
		return nil, asProtocolErr(err)
	}
	return out.Displays, nil
}

func (c *capturer) Permissions(ctx context.Context) (protocol.PermissionStatus, error) {
	var out protocol.PermissionStatus
	err := c.conn.Call(ctx, helperipc.OpPermissionsCheck, nil, &out)
	return out, asProtocolErr(err)
}

func (c *capturer) StartCapture(ctx context.Context, cfg protocol.CaptureConfig) (protocol.CaptureStarted, error) {
	var out protocol.CaptureStarted
	err := c.conn.Call(ctx, helperipc.OpCaptureStart, cfg, &out)
	return out, asProtocolErr(err)
}

func (c *capturer) StopCapture(ctx context.Context) error {
	return asProtocolErr(c.conn.Call(ctx, helperipc.OpCaptureStop, nil, nil))
}

func (c *capturer) RequestKeyframe() { _ = c.conn.Send(helperipc.OpCaptureKeyframe, nil) }

func (c *capturer) TuneBitrate(kbps int) {
	_ = c.conn.Send(helperipc.OpCaptureTune, helperipc.TuneArgs{BitrateKbps: &kbps})
}

func (c *capturer) Frames() <-chan platform.Frame { return c.frames }
func (c *capturer) Events() <-chan platform.Event { return c.events }
func (c *capturer) Input() platform.Input         { return &input{conn: c.conn} }
func (c *capturer) Clipboard() platform.Clipboard { return &clipboard{conn: c.conn} }
func (c *capturer) Done() <-chan struct{}         { return c.done }

func (c *capturer) Err() error {
	select {
	case <-c.done:
	default:
		return nil
	}
	c.mu.Lock()
	closed, exit := c.closed, c.exitErr
	c.mu.Unlock()
	if closed {
		return nil
	}
	if err := c.conn.Err(); err != nil {
		return err
	}
	return exit
}

// Close asks the helper to release input and capture and exit; if it lingers it is terminated.
func (c *capturer) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Send(helperipc.OpInputReleaseAll, nil)
		_ = c.conn.Send(helperipc.OpShutdown, nil)
	}
	if !c.waitExit(1500 * time.Millisecond) {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
		if !c.waitExit(1500 * time.Millisecond) {
			c.kill()
			c.waitExit(time.Second)
		}
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return nil
}

func (c *capturer) waitExit(d time.Duration) bool {
	select {
	case <-c.procDone:
		return true
	case <-time.After(d):
		return false
	}
}

func (c *capturer) kill() {
	if c.cmd.Process != nil {
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL) // the helper's own process group
		_ = c.cmd.Process.Kill()
	}
}
