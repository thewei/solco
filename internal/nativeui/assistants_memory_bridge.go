package nativeui

/**
 * [INPUT]: 依赖 assistants_bridge 的 client/write 与 internal/personalagent 的 memory/proactivity 客户端
 * [OUTPUT]: AssistantsBridge.Notes（两个 Markdown 文件——你的 USER.md 与助手自己的 ASSISTANT.md——各带 rev；桌面不在 App 内写，
 *           写入只由手机的 Host 路由经核心做）、OpenMemoryFolder（在 Finder 打开记忆文件夹）、
 *           OpenMemoryFile（用用户自己的文本编辑器打开其一）、Behavior / SaveBehavior（主动等级、安静时段、汇报频率、每日预算）
 * [POS]: 记忆就是这两个文件，没有候选/审核/版本：助手要记就直接记，你随时可改自己的文件；权威是插件核心的文件读写（原子、O_NOFOLLOW、rev 围栏），
 *        桥只转发并把拒绝原样（带稳定码）交给页面；日志与快照之外不留记忆正文
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/thewei/solco/internal/personalagent"
	"github.com/thewei/solco/internal/platform"
)

// openFolder reveals a directory in Finder; a seam so tests never open one.
var openFolder = func(path string) error { return exec.Command("open", path).Start() }

// openMemoryFile hands one memory file to the person's own text editor; a seam
// so tests never open one.
var openMemoryFile = platform.OpenFile

// Notes reads one Assistant's two memory files with the rev of each.
func (b *AssistantsBridge) Notes(ctx context.Context, id string) (personalagent.Notes, error) {
	var out personalagent.Notes
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	return c.ReadNotes(ctx, id)
}

// OpenMemoryFolder opens the Assistant's memory folder in Finder.
func (b *AssistantsBridge) OpenMemoryFolder(ctx context.Context, id string) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	path, err := c.NotesFolder(ctx, id)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("the memory folder is unavailable")
	}
	return openFolder(path)
}

// OpenMemoryFile opens one memory file ("user" or "assistant") in the person's
// own text editor — the desktop never edits memory inside the app. The core
// makes the file first, so a fresh Assistant has an empty one to open.
func (b *AssistantsBridge) OpenMemoryFile(ctx context.Context, id, which string) error {
	name := map[string]string{personalagent.NotesUser: "USER.md", personalagent.NotesAssistant: "ASSISTANT.md"}[which]
	if name == "" {
		return errors.New("unknown memory file")
	}
	c, err := b.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	dir, err := c.NotesFolder(ctx, id)
	if err != nil {
		return err
	}
	if dir == "" {
		return errors.New("the memory folder is unavailable")
	}
	return openMemoryFile(filepath.Join(dir, name))
}

// Behavior reads the proactivity rules (defaults when never set).
func (b *AssistantsBridge) Behavior(ctx context.Context, id string) (personalagent.Proactivity, error) {
	var out personalagent.Proactivity
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	return c.Proactivity(ctx, id)
}

// SaveBehavior stores the rules; they govern only what starts unasked.
func (b *AssistantsBridge) SaveBehavior(ctx context.Context, requestID, id string, in personalagent.ProactivityInput, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.SetProactivity(ctx, requestID, id, in, revision)
		return err
	})
}
