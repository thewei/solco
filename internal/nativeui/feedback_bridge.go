package nativeui

/**
 * [INPUT]: 依赖 feedback_ui.go 的 feedbackState（broker 客户端与落点）、internal/feedback 的模型、solco_git_link.go 的 openInSolcoGit、script_actions.go 的 revealInFinder
 * [OUTPUT]: FeedbackBridge（`mygo.BindAs("feedback", …)`）——Snapshot（线程列表 + 选中线程的时间线 + 一次性落点）、Answer、End、OpenProject
 * [POS]: Feedback WebView 页（2026-11-09 决策）的唯一数据与命令口：全部读写走 broker 的回环 HTTP（线程/Ask 状态的唯一所有者在进程外），Solco 不缓存也不重算；broker 不在时返回 online=false 而不是错误；IO 在桥 goroutine 上，窗口线程只被借来读落点与发起 Solco Git 打开；日志不记问题、答案与路径
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"time"

	"github.com/thewei/solco/internal/feedback"
	"github.com/thewei/solco/internal/solcogit"
)

// feedbackCallTimeout bounds one bridge call (a loopback read or write).
const feedbackCallTimeout = 10 * time.Second

// FeedbackBridge is the page's bound service.
type FeedbackBridge struct{ shell *Shell }

// NewFeedbackBridge binds the page to the shell.
func NewFeedbackBridge(shell *Shell) *FeedbackBridge { return &FeedbackBridge{shell: shell} }

// FeedbackView is everything the page draws, one value per read.
type FeedbackView struct {
	Theme string `json:"theme"`
	// Online is false while no process serves the broker; the page shows its
	// offline state and keeps polling.
	Online  bool                     `json:"online"`
	Threads []feedback.ThreadSummary `json:"threads"`
	Waiting int                      `json:"waiting"`
	// Selected is the thread Detail belongs to ("" = none).
	Selected string           `json:"selected"`
	Detail   *feedback.Thread `json:"detail,omitempty"`
	// Focus is a landing a notification or link asked for, delivered once.
	Focus *feedbackFocus `json:"focus,omitempty"`
	// CanOpenGit says Solco Git is on this machine (the thread header's
	// "open project" menu offers it).
	CanOpenGit bool `json:"canOpenGit"`
}

// Snapshot reads the thread list and, for the thread the page is on, its
// timeline. selected is client-local navigation; a pending Focus overrides it.
func (b *FeedbackBridge) Snapshot(ctx context.Context, selected string) (FeedbackView, error) {
	s := b.shell
	view := FeedbackView{Threads: []feedback.ThreadSummary{}}
	var client *feedback.Client
	s.callOnWindow(func() {
		view.Theme = s.chatBridgeTheme()
		view.CanOpenGit = s.solcoGitReady()
		client = s.feedback.client
		if f := s.feedback.focus; f.Thread != "" {
			view.Focus = &f
			selected = f.Thread
			s.feedback.focus = feedbackFocus{}
		}
	})
	if client == nil {
		return view, nil
	}
	ctx, cancel := context.WithTimeout(ctx, feedbackCallTimeout)
	defer cancel()
	threads, err := client.Threads(ctx, false, "")
	if err != nil {
		if errors.Is(err, feedback.ErrOffline) {
			return view, nil
		}
		return view, err
	}
	view.Online = true
	if threads != nil {
		view.Threads = threads
	}
	for _, t := range view.Threads {
		view.Waiting += t.Waiting
	}
	if selected == "" {
		return view, nil
	}
	detail, err := client.Thread(ctx, selected)
	if err != nil {
		if errors.Is(err, feedback.ErrNotFound) {
			return view, nil // the thread is gone (history trimmed); the page falls back to the list
		}
		return view, err
	}
	view.Selected = selected
	view.Detail = &detail
	return view, nil
}

func (b *FeedbackBridge) call(ctx context.Context, fn func(context.Context, *feedback.Client) error) error {
	client := b.shell.feedback.client
	if client == nil {
		return feedback.ErrOffline
	}
	ctx, cancel := context.WithTimeout(ctx, feedbackCallTimeout)
	defer cancel()
	return fn(ctx, client)
}

// Answer delivers the user's answers to a pending Ask, or to an expired one
// as a late answer.
func (b *FeedbackBridge) Answer(ctx context.Context, askID string, answers map[string]feedback.Answer, attachments []feedback.Attachment) error {
	return b.call(ctx, func(ctx context.Context, c *feedback.Client) error {
		return c.Answer(ctx, askID, answers, attachments)
	})
}

// End closes an Ask without an answer.
func (b *FeedbackBridge) End(ctx context.Context, askID string) error {
	return b.call(ctx, func(ctx context.Context, c *feedback.Client) error {
		return c.End(ctx, askID)
	})
}

// OpenProject hands a thread's project folder to Solco Git ("git") or shows
// it in Finder ("finder"). The folder comes from the broker's own record of
// the thread, never from the page.
func (b *FeedbackBridge) OpenProject(ctx context.Context, threadID, target string) error {
	var root string
	err := b.call(ctx, func(ctx context.Context, c *feedback.Client) error {
		t, err := c.Thread(ctx, threadID)
		root = t.ProjectRoot
		return err
	})
	if err != nil {
		return err
	}
	if root == "" {
		return errors.New("this thread has no project folder")
	}
	s := b.shell
	switch target {
	case "git":
		s.callOnWindow(func() { s.openInSolcoGit(root, solcogit.Changes, "") })
	case "finder":
		revealInFinder(root)
	default:
		return errors.New("unknown target")
	}
	return nil
}
