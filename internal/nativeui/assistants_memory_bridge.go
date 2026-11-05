package nativeui

/**
 * [INPUT]: 依赖 assistants_bridge 的 client/write 与 internal/personalagent 的 memory/proactivity 客户端
 * [OUTPUT]: AssistantsBridge.Memory（活记忆=已批准+候选，附整理发现：重复/久未回应/改动冲突）、AddMemory / EditMemory /
 *           ApproveMemory / ForgetMemory、MemoryHistory（逐版本，含谁改的）、OpenMemoryFolder（让核心把文件夹对齐后在 Finder 打开）、
 *           Behavior / SaveBehavior（主动等级、安静时段、汇报频率、每日预算）
 * [POS]: Assistants 3.1 U5 的桌面面：权威是插件 SQLite 核心，记忆文件夹只是它的可编辑投影（核心的 Sync 校验后才信文件）；
 *        桥只转发并把拒绝原样（带稳定码）交给页面；日志与快照之外不留记忆正文
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"os/exec"

	"github.com/thewei/solco/internal/personalagent"
)

// openFolder reveals a directory in Finder; a seam so tests never open one.
var openFolder = func(path string) error { return exec.Command("open", path).Start() }

// Memory reads one Assistant's live memory with the tidy-up findings.
func (b *AssistantsBridge) Memory(ctx context.Context, id string) (personalagent.MemoryReview, error) {
	var out personalagent.MemoryReview
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	if out, err = c.ReviewMemory(ctx, id); err != nil {
		return out, err
	}
	if out.Items == nil {
		out.Items = []personalagent.MemoryItem{}
	}
	if out.Duplicates == nil {
		out.Duplicates = [][]string{}
	}
	if out.StaleCandidates == nil {
		out.StaleCandidates = []string{}
	}
	if out.Conflicts == nil {
		out.Conflicts = []string{}
	}
	return out, nil
}

// MemoryHistory lists an item's versions, newest first.
func (b *AssistantsBridge) MemoryHistory(ctx context.Context, memoryID string) ([]personalagent.MemoryVersion, error) {
	c, err := b.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	out, err := c.MemoryHistory(ctx, memoryID)
	if out == nil {
		out = []personalagent.MemoryVersion{}
	}
	return out, err
}

// AddMemory writes a fact down; it is approved at once.
func (b *AssistantsBridge) AddMemory(ctx context.Context, requestID, id, content string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.AddMemory(ctx, requestID, id, content, "")
		return err
	})
}

// EditMemory rewrites an item; what the Assistant may use changes at once.
func (b *AssistantsBridge) EditMemory(ctx context.Context, requestID, memoryID string, revision int, content string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.EditMemory(ctx, requestID, "correct", memoryID, revision, content)
		return err
	})
}

// ApproveMemory turns a candidate into something the Assistant may use.
func (b *AssistantsBridge) ApproveMemory(ctx context.Context, requestID, memoryID string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.EditMemory(ctx, requestID, "approve", memoryID, revision, "")
		return err
	})
}

// ForgetMemory removes an item's text everywhere (versions and file included).
func (b *AssistantsBridge) ForgetMemory(ctx context.Context, requestID, memoryID string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.EditMemory(ctx, requestID, "forget", memoryID, revision, "")
		return err
	})
}

// OpenMemoryFolder brings the Assistant's memory folder up to date and opens it in Finder.
func (b *AssistantsBridge) OpenMemoryFolder(ctx context.Context, id string) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	path, err := c.MemoryFolder(ctx, id)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("the memory folder is unavailable")
	}
	return openFolder(path)
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

// Budget reads today's budget ledger: what the system started on its own, what
// it held back and why (Gap A05). Tokens and money are reported as unmeasured.
func (b *AssistantsBridge) Budget(ctx context.Context, id string) (personalagent.Budget, error) {
	out := personalagent.Budget{Spent: map[string]int{}, Held: []personalagent.BudgetHold{}, Recent: []personalagent.BudgetEvent{}, Unmeasured: []string{}}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	return c.Budget(ctx, id, 12)
}
