package nativeui

/**
 * [INPUT]: 依赖 assistants_bridge 的 client/write 机制、internal/personalagent 的 Group 客户端
 * [OUTPUT]: AssistantsBridge 的 Group 方法——CreateGroup（建群并选中）、GroupFeed（增量读群日志）、PostGroupMessage（本人发言，@ 精确）、RenameGroup、GroupMember（加/移成员）
 * [POS]: Assistants 3.1 U1：群聊的权威是插件 SQLite 里的一条有序消息日志；桌面与手机读同一份，页面只渲染并带 requestId 回写；只有本人的 @ 会唤起对应助理的 Agent，渲染路径零 IO，日志不含消息正文
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"

	"github.com/thewei/solco/internal/personalagent"
)

// CreateGroup records a Group of at least two Assistants and opens it.
func (b *AssistantsBridge) CreateGroup(ctx context.Context, requestID, title string, members []string) (string, error) {
	c, err := b.client()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	g, err := c.CreateGroup(ctx, requestID, strings.TrimSpace(title), members)
	if err != nil {
		return "", err
	}
	b.SelectGroup(g.ID)
	b.shell.applyOnWindow(func() { b.shell.refreshAssistants(true) })
	return g.ID, nil
}

// GroupFeed reads a Group's log: the newest page when since is 0, else what
// came after it. The page asks every couple of seconds while it is visible.
func (b *AssistantsBridge) GroupFeed(ctx context.Context, id string, since int64) (personalagent.GroupFeed, error) {
	c, err := b.client()
	if err != nil {
		return personalagent.GroupFeed{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	return c.GroupFeed(ctx, id, since, 0)
}

// PostGroupMessage is the person's own message. mentions are member ids; each
// member's @Name must be in the text, and only those Assistants are woken.
func (b *AssistantsBridge) PostGroupMessage(ctx context.Context, requestID, groupID, text string, mentions []string, replyTo string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.PostGroupMessage(ctx, requestID, groupID, text, mentions, replyTo)
		return err
	})
}

// RenameGroup changes a Group's title.
func (b *AssistantsBridge) RenameGroup(ctx context.Context, requestID, id, title string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.RenameGroup(ctx, requestID, id, strings.TrimSpace(title), revision)
		return err
	})
}

// GroupMember adds or removes one Assistant.
func (b *AssistantsBridge) GroupMember(ctx context.Context, requestID, id, assistantID string, add bool) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.GroupMember(ctx, requestID, id, assistantID, add)
		return err
	})
}
