package nativeui

import (
	"time"

	"github.com/egoist/mygo"
)

/**
 * [INPUT]: assistants_state 的 assistantsState、Shell.win（事件出口）、timeNow
 * [OUTPUT]: AssistantsIntent（页面要办的一件事：开 New Assistant 表单）、postAssistantsIntent（登记意图、导航、推事件）、pendingIntent/PendingIntent（页面独立轻读，不经插件 CLI，ACK 前不清除）、AckIntent（桥命令）、assistantsIntentEvent
 * [POS]: 取代 2026-11-04 之前的 `newForm bool`（Snapshot 读取即清除 → 页面未挂载/响应乱序/请求失败/连续点击都会丢事件）：意图带单调 seq，PendingIntent 每次都带着（不经 Snapshot：CLI 读失败会连带丢意图，2026-11-04 实机复现），页面开表单后 AckIntent(seq) 才撤；同 seq 重复送达页面按 seq 去重；事件只是让已挂载的页面立刻 pull，丢了也由轮询补上
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// assistantsIntentTTL drops an intent nobody picked up (a page that never
// mounted must not pop a sheet minutes later).
const assistantsIntentTTL = 2 * time.Minute

// The one intent kind the sidebar posts.
const intentNewAssistant = "assistant"

// AssistantsIntent is one thing the sidebar asked the page to do. Seq grows
// with every post; the page acts on a Seq once and acknowledges it.
type AssistantsIntent struct {
	Seq  int64  `json:"seq"`
	Kind string `json:"kind"`
}

// assistantsIntentEvent wakes a mounted page so it reads the intent now
// instead of at its next poll.
var assistantsIntentEvent = mygo.NewEvent[AssistantsIntent]("assistants:intent")

// intentState is the Shell's record of the latest outstanding intent.
type intentState struct {
	seq   int64
	kind  string
	at    time.Time
	acked int64
}

// postAssistantsIntent registers the intent first, then navigates, then tells
// a mounted page. Window lane. A blocked navigation (a commit in flight)
// posts nothing, so no sheet appears on a page the user is not on.
func (s *Shell) postAssistantsIntent(kind string) {
	if !s.assistantsAvailable() || s.holdCommitNavigation() {
		return
	}
	in := &s.assistants.intent
	in.seq++
	in.kind, in.at = kind, timeNow()
	s.showAssistant("")
	if s.win != nil {
		_ = assistantsIntentEvent.Emit(s.win, AssistantsIntent{Seq: in.seq, Kind: in.kind})
	}
}

// pendingIntent is the intent the page still owes an answer to, nil once it
// was acknowledged or went stale. Window lane.
func (s *Shell) pendingIntent() *AssistantsIntent {
	in := s.assistants.intent
	if in.seq == 0 || in.seq <= in.acked || timeNow().Sub(in.at) > assistantsIntentTTL {
		return nil
	}
	return &AssistantsIntent{Seq: in.seq, Kind: in.kind}
}

// PendingIntent is the page's own cheap read of the outstanding intent: no
// plugin call, so a failing or slow CLI read can never hide a click (the
// first version rode the Snapshot and an erroring read dropped it).
func (b *AssistantsBridge) PendingIntent() *AssistantsIntent {
	var in *AssistantsIntent
	b.shell.callOnWindow(func() { in = b.shell.pendingIntent() })
	return in
}

// AckIntent is the page telling the shell it opened what seq asked for.
func (b *AssistantsBridge) AckIntent(seq int64) {
	b.shell.applyOnWindow(func() {
		if in := &b.shell.assistants.intent; seq > in.acked {
			in.acked = seq
		}
	})
}
