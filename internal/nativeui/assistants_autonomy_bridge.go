package nativeui

/**
 * [INPUT]: 依赖 assistants_bridge 的 client/write 与 internal/personalagent 的 HookKeys/HookRecords/CreateHookKey/RevokeHookKey/Triggers/TriggerAction；Shell.remote.srv 的监听地址
 * [OUTPUT]: AssistantsBridge.Autonomy（webhook 密钥、最近收据、事件触发器与入口地址）、CreateHookKey（secret 只在这一次返回）、RevokeHookKey、TriggerAction（pause|resume|cancel）
 * [POS]: 2026-11-05 主动性管理面（Gaps A05/A06/A09）的桌面入口：权威是插件 SQLite 核心，桥只转发并把拒绝原样（带稳定码）交给页面；
 *        触发器的批准仍只走审批卡；收据不含发送方 body；入口地址只在 Remote Access 真在监听时给出（不监听就没有人能投递，页面如实说）；
 *        secret/reason/brief 不进日志，也不在 Snapshot 里（只此处一次性返回）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"

	"github.com/thewei/solco/internal/personalagent"
)

// AssistantsAutonomy is the Autonomy tab's content for one Assistant.
type AssistantsAutonomy struct {
	// Endpoint is the Host's base address ("" while Remote Access is not
	// listening: nobody outside can deliver anything then). HookPath is the one
	// path this Assistant is reached on.
	Endpoint string                     `json:"endpoint"`
	HookPath string                     `json:"hookPath"`
	Keys     []personalagent.HookKey    `json:"keys"`
	Events   []personalagent.HookRecord `json:"events"`
	Triggers []personalagent.Trigger    `json:"triggers"`
}

// AssistantsHookKey is a freshly made key: Secret is shown once, here, and is
// never listed again (a replayed request answers with no secret).
type AssistantsHookKey struct {
	Key    personalagent.HookKey `json:"key"`
	Secret string                `json:"secret"`
	Path   string                `json:"path"`
}

// Autonomy reads one Assistant's webhook keys, receipts and triggers.
func (b *AssistantsBridge) Autonomy(ctx context.Context, id string) (AssistantsAutonomy, error) {
	out := AssistantsAutonomy{Keys: []personalagent.HookKey{}, Events: []personalagent.HookRecord{}, Triggers: []personalagent.Trigger{}}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	keys, err := c.HookKeys(ctx, id)
	if err != nil {
		return out, err
	}
	events, err := c.HookRecords(ctx, id, 20)
	if err != nil {
		return out, err
	}
	triggers, err := c.Triggers(ctx, id)
	if err != nil {
		return out, err
	}
	if keys != nil {
		out.Keys = keys
	}
	if events != nil {
		out.Events = events
	}
	if triggers != nil {
		out.Triggers = triggers
	}
	out.HookPath = "/api/v3/hooks/assistants/" + id + "/wake"
	b.shell.callOnWindow(func() {
		if srv := b.shell.remote.srv; srv != nil {
			if addr := srv.ListenAddress(); addr != "" {
				out.Endpoint = "https://" + addr
			}
		}
	})
	return out, nil
}

// CreateHookKey makes a key for the named sources. Its secret comes back once.
func (b *AssistantsBridge) CreateHookKey(ctx context.Context, requestID, id string, sources []string) (AssistantsHookKey, error) {
	var out AssistantsHookKey
	err := b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		key, secret, path, err := c.CreateHookKey(ctx, requestID, id, sources)
		out = AssistantsHookKey{Key: key, Secret: secret, Path: path}
		return err
	})
	return out, err
}

// RevokeHookKey stops a key at once; deliveries already received stay listed.
func (b *AssistantsBridge) RevokeHookKey(ctx context.Context, requestID, id, keyID string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		keys, err := c.HookKeys(ctx, id)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k.ID == keyID {
				_, err := c.RevokeHookKey(ctx, requestID, keyID)
				return err
			}
		}
		return errors.New("not_found: no such key")
	})
}

// TriggerAction pauses, resumes or withdraws a trigger (approving one is the
// approval card's answer, never this).
func (b *AssistantsBridge) TriggerAction(ctx context.Context, requestID, action, id, triggerID string, revision int) error {
	if action != "pause" && action != "resume" && action != "cancel" {
		return errors.New("unknown trigger action")
	}
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		list, err := c.Triggers(ctx, id)
		if err != nil {
			return err
		}
		for _, t := range list {
			if t.ID == triggerID {
				_, err := c.TriggerAction(ctx, requestID, action, triggerID, revision)
				return err
			}
		}
		return errors.New("not_found: no such trigger")
	})
}
