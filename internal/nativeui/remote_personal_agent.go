package nativeui

/**
 * [INPUT]: 依赖 assistants_state 的门与概览、internal/personalagent 的 CLI 客户端、remotehost.Server.NotifyPersonalAgent
 * [OUTPUT]: Shell.syncRemoteAssistants（窗口线程在门/概览变化后刷新手机用的客户端）、Shell.hostPersonalAgent（Host goroutine 取客户端；门关 ok=false）、Shell.notifyAssistantsChanged（概览计数摘要变了才向手机发 `personal-agent.changed`）
 * [POS]: 手机 Assistants 的桌面缝（2026-11-03，B4）：Host 的 goroutine 不碰 Shell 状态，只拿一个只读指针；客户端与桌面桥同一个 CLI 入口，权威仍是插件 daemon 的 SQLite；手机的写入走同一个核心，所以不需要第二份状态——桌面侧栏在下一次读取时自己追上；变更帧只带助手数与 Needs You 数，不带名字/目标/记忆
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"strings"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
	"github.com/thewei/solco/internal/remotehost"
)

// syncRemoteAssistants publishes (or withdraws) the client the phones' routes
// use. Window lane only.
func (s *Shell) syncRemoteAssistants() {
	if s.assistantsAvailable() {
		s.assistantsRemote.Store(newAssistantsClient(s.automations.avail.Root, herdr.ParseRef(s.activeInstance).Session))
		return
	}
	s.assistantsRemote.Store(nil)
}

// hostPersonalAgent serves the Host's Assistants routes; ok is false while the
// gate is closed or the first overview has not landed.
func (s *Shell) hostPersonalAgent(context.Context) (*personalagent.Client, bool) {
	c := s.assistantsRemote.Load()
	return c, c != nil
}

// hostAssistantAgents is the Agent choice the phone's Start conversation
// offers: the same list the desktop chip shows (window lane read).
func (s *Shell) hostAssistantAgents() []remotehost.PersonalAgentKind {
	out := []remotehost.PersonalAgentKind{}
	s.callOnWindow(func() {
		list, _ := s.assistantAgents()
		for _, a := range list {
			out = append(out, remotehost.PersonalAgentKind{ID: a.ID, Label: a.Label})
		}
	})
	return out
}

// notifyAssistantsChanged tells the open phones the overview moved. The first
// read after launch (or after the gate reopens) announces nothing; the phone
// reads authoritative state when it connects anyway.
func assistantsOverviewFingerprint(overview personalagent.Overview) string {
	var b strings.Builder
	for _, a := range overview.Assistants {
		fmt.Fprintf(&b, "%s:%d:%s:%d:%d:%d:%d:%d:%d:%d;", a.ID, a.Revision, a.Status, a.Goals, a.Tasks, a.Running, a.Queued, a.Done, a.NeedsYou, a.Approvals)
	}
	// A Fact can arrive without changing the sidebar counters. Include its
	// committed sequence in the hint fingerprint so open phones refresh.
	fmt.Fprintf(&b, "activity:%d;", overview.ActivitySeq)
	return "v2|" + b.String()
}

func (s *Shell) notifyAssistantsChanged() {
	st := &s.assistants
	next := assistantsOverviewFingerprint(st.overview)
	prev := st.fingerprint
	st.fingerprint = next
	if next == prev || prev == "" || s.remote.srv == nil {
		return
	}
	s.remote.srv.NotifyPersonalAgent(len(st.overview.Assistants), st.overview.NeedsYou)
}
