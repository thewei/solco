package feedback

/**
 * [INPUT]: 一条新 Ask 的线程摘要、notify.json（Worker 地址与令牌，0600）
 * [OUTPUT]: 对外提供 Notice、Notifier、WorkerNotifier（飞书卡片）、LocalNotifier（没有 UI 在线时的 macOS 通知）、LoadWorkerConfig、Notifiers
 * [POS]: Agent 提问时的对外通知（2026-11-09 决策）：飞书卡片只带「项目 · Agent · 问题首行」与只含 id 的落地页链接，失败只记日志、不影响 Ask；
 *        Solco 窗口在线时（有 SSE 订阅者）由 App 自己发带点击落点的系统通知，broker 不重复弹
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/thewei/solco/internal/settings"
)

// Notice is what a Notifier is told about a new Ask.
type Notice struct {
	Thread     ThreadSummary
	Ask        Ask
	UIAttached bool // a Solco window is subscribed and shows its own notification
}

// Notifier delivers a Notice somewhere. It must not block for long and never fails the Ask.
type Notifier interface {
	Notify(ctx context.Context, n Notice)
}

// Notifiers fans a Notice out to several Notifiers.
type Notifiers []Notifier

func (ns Notifiers) Notify(ctx context.Context, n Notice) {
	for _, x := range ns {
		x.Notify(ctx, n)
	}
}

// WorkerConfig points at the deployed Feishu gateway Worker.
type WorkerConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// LoadWorkerConfig reads dir/notify.json, falling back to the file the old daemon's setup left behind.
func LoadWorkerConfig(dir string) (WorkerConfig, bool) {
	paths := []string{filepath.Join(dir, "notify.json")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "interactive-feedback-mcp", "notify-worker.json"))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c WorkerConfig
		if json.Unmarshal(b, &c) == nil && strings.HasPrefix(c.URL, "https://") && c.Token != "" {
			c.URL = strings.TrimRight(c.URL, "/")
			return c, true
		}
	}
	return WorkerConfig{}, false
}

// WorkerNotifier posts a card to the Worker.
type WorkerNotifier struct {
	Config WorkerConfig
	Client *http.Client
}

func (w WorkerNotifier) Notify(ctx context.Context, n Notice) {
	if len(n.Ask.Questions) == 0 {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"event":   "question",
		"title":   n.Thread.Title,
		"text":    firstLine(n.Ask.Questions[0].Text),
		"project": n.Thread.Project,
		"agent":   n.Thread.Client,
		"thread":  n.Thread.ID,
		"item":    n.Ask.ID,
	})
	cl := w.Client
	if cl == nil {
		cl = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Config.URL+"/v1/notify", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+w.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		slog.Warn("feedback: worker notify failed", "error", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		slog.Warn("feedback: worker notify refused", "status", resp.StatusCode)
	}
}

// LocalNotifier shows a plain macOS notification when no Solco window is there to show its own.
type LocalNotifier struct{}

func (LocalNotifier) Notify(ctx context.Context, n Notice) {
	if n.UIAttached || len(n.Ask.Questions) == 0 {
		return
	}
	script := `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`
	title := "Solco · " + n.Thread.Project
	_ = exec.CommandContext(ctx, "osascript", "-e", script, title, firstLine(n.Ask.Questions[0].Text)).Run()
}

// DefaultNotifier is the production fan-out: a local notification when no window is open and, when dir has a
// Worker configured, a Feishu card. The Worker file is read per Notice, so setting it up needs no restart.
func DefaultNotifier(dir string) Notifier { return configNotifier{dir: dir} }

type configNotifier struct{ dir string }

func (c configNotifier) Notify(ctx context.Context, n Notice) {
	LocalNotifier{}.Notify(ctx, n)
	if cfg, ok := LoadWorkerConfig(c.dir); ok && !channelsConfigured(c.dir) {
		WorkerNotifier{Config: cfg}.Notify(ctx, n)
	}
}

// channelsConfigured reports whether the user has an enabled webhook channel in Settings (the broker's directory
// sits under the user-data directory). A channel replaces the old always-on Worker file: the Host calls channels
// only when no phone can be told, and two calls for one Ask would double the Feishu message.
func channelsConfigured(dir string) bool {
	cur, err := settings.NewFileStore(filepath.Dir(dir)).Load(context.Background())
	if err != nil {
		return false
	}
	for _, ch := range cur.Notifications.Channels {
		if ch.Enabled {
			return true
		}
	}
	return false
}
