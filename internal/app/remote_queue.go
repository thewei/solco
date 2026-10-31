package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 LaunchService（桌面同一条跟进队列 + 投递 worker）、remotehost.FollowUps 契约
 * [OUTPUT]: 对外提供 RemoteFollowUps（手机 Chat 的排队/查询/取消）
 * [POS]: 手机 Host 与桌面队列之间的薄适配——只翻译类型与错误；入队沿用桌面的 QueueItem（身份指纹由 Host 从 Herdr agent_session 给出），状态机/边界等待/不确定墓碑全在 conversation 包
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type RemoteFollowUps struct {
	service *LaunchService
}

func NewRemoteFollowUps(service *LaunchService) *RemoteFollowUps {
	return &RemoteFollowUps{service: service}
}

func (r *RemoteFollowUps) Enqueue(instance, paneID, conversationID, fingerprint, text string) error {
	err := r.service.EnqueueFollowUp(conversation.QueueItem{
		RequestID:      fmt.Sprintf("remote-queue-%s-%d", paneID, time.Now().UnixNano()),
		ConversationID: conversation.ConversationID(conversationID),
		AgentPaneID:    paneID,
		Fingerprint:    fingerprint,
		Text:           text,
		Instance:       instance,
	})
	if errors.Is(err, conversation.ErrQueueSlotTaken) {
		return remotehost.ErrFollowUpFull
	}
	return err
}

// live reports whether the item still waits to be (or is being) delivered or needs a look.
func live(state conversation.QueueState) bool {
	switch state {
	case conversation.Queued, conversation.WaitingForTurnBoundary, conversation.Delivering,
		conversation.FailedRecoverable, conversation.DeliveryUncertain:
		return true
	}
	return false
}

func (r *RemoteFollowUps) Get(conversationID string) (remotehost.FollowUp, bool) {
	item, ok := r.service.Queue().Get(conversation.ConversationID(conversationID))
	if !ok || !live(item.State) {
		return remotehost.FollowUp{}, false
	}
	return remotehost.FollowUp{Text: item.Text, State: string(item.State), Detail: item.Detail}, true
}

func (r *RemoteFollowUps) Cancel(conversationID string) error {
	_, err := r.service.Queue().Cancel(conversation.ConversationID(conversationID))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, conversation.ErrNoQueueItem):
		return remotehost.ErrFollowUpNone
	default:
		return remotehost.ErrFollowUpCommitted
	}
}
