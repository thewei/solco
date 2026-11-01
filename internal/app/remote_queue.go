package app

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 LaunchService（桌面同一条跟进队列 + 投递 worker）、remotehost.FollowUps 契约
 * [OUTPUT]: 对外提供 RemoteFollowUps（手机 Chat 的排队/查询/单条与全清取消）
 * [POS]: 手机 Host 与桌面队列之间的薄适配——只翻译类型与错误；入队沿用桌面的 QueueItem（身份指纹由 Host 从 Herdr agent_session 给出，会话头 DeliveryUncertain 冻结 → ErrFollowUpFrozen），Items 直通（已投递/已取消的条目不会出现在列表里），CancelItem/Cancel(全清) 按条身份撤回；状态机/边界等待/不确定墓碑全在 conversation 包
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// remoteQueueSeq keeps two enqueues in one clock tick (macOS time has µs
// resolution) from sharing a request id.
var remoteQueueSeq atomic.Uint64

type RemoteFollowUps struct {
	service *LaunchService
}

func NewRemoteFollowUps(service *LaunchService) *RemoteFollowUps {
	return &RemoteFollowUps{service: service}
}

func (r *RemoteFollowUps) Enqueue(instance, paneID, conversationID, fingerprint, text string) error {
	err := r.service.EnqueueFollowUp(conversation.QueueItem{
		RequestID:      fmt.Sprintf("remote-queue-%s-%d-%d", paneID, time.Now().UnixNano(), remoteQueueSeq.Add(1)),
		ConversationID: conversation.ConversationID(conversationID),
		AgentPaneID:    paneID,
		Fingerprint:    fingerprint,
		Text:           text,
		Instance:       instance,
	})
	if errors.Is(err, conversation.ErrQueueFrozen) {
		return remotehost.ErrFollowUpFrozen
	}
	return err
}

func (r *RemoteFollowUps) Items(conversationID string) ([]remotehost.FollowUpItem, bool) {
	items, ok := r.service.Queue().Items(conversation.ConversationID(conversationID))
	if !ok {
		return nil, false
	}
	out := make([]remotehost.FollowUpItem, 0, len(items))
	for _, item := range items {
		out = append(out, remotehost.FollowUpItem{ID: item.RequestID, Text: item.Text, State: string(item.State), Detail: item.Detail})
	}
	return out, true
}

func (r *RemoteFollowUps) CancelItem(conversationID, requestID string) error {
	_, err := r.service.Queue().CancelItem(conversation.ConversationID(conversationID), requestID)
	return mapCancelError(err)
}

func (r *RemoteFollowUps) EditItem(conversationID, requestID, text string) error {
	_, err := r.service.Queue().Edit(conversation.ConversationID(conversationID), requestID, text)
	return mapCancelError(err)
}

func (r *RemoteFollowUps) MoveItem(conversationID, requestID string, to int) error {
	return mapCancelError(r.service.Queue().Move(conversation.ConversationID(conversationID), requestID, to))
}

func (r *RemoteFollowUps) Cancel(conversationID string) error {
	_, err := r.service.Queue().CancelAll(conversation.ConversationID(conversationID))
	return mapCancelError(err)
}

// mapCancelError translates the conversation ledger's withdraw outcomes onto
// the Host's named errors; anything else passes through (502 at the wire).
func mapCancelError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, conversation.ErrNoQueueItem):
		return remotehost.ErrFollowUpNone
	case errors.Is(err, conversation.ErrAlreadyDelivered), errors.Is(err, conversation.ErrQueueClaimed):
		return remotehost.ErrFollowUpCommitted
	case errors.Is(err, conversation.ErrQueueRange):
		return remotehost.ErrFollowUpRange
	default:
		return err
	}
}
