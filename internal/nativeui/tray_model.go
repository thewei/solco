// [INPUT]: 依赖 StatusCenterSnapshot（status_center_model.go）的 NeedsAttention/ReviewPending/Working/Entries、agent.OperationalLabel
// [OUTPUT]: 导出 TrayCounts、TrayCountsFromSnapshot、TrayTitle、TrayToolTip、TrayRemoteDesktop、TrayFingerprint
// [POS]: nativeui 托盘的纯投影层。tray.go 持有它；quick_panel.go 的 attentionSegments 共享同一份 counts 语义。无 IO。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

package nativeui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/thewei/solco/internal/agent"
)

// TrayCounts are the status-center counts the menu bar projects (0.7
// §3/§17.3): attention, ready-for-review, working, and the total number of
// agents the status center lists.
type TrayCounts struct {
	Attention int
	Review    int
	Working   int
	Total     int
}

// TrayCountsFromSnapshot reads the shared StatusCenterSnapshot counters; the
// tray never derives its own status semantics.
func TrayCountsFromSnapshot(snapshot StatusCenterSnapshot) TrayCounts {
	return TrayCounts{
		Attention: snapshot.NeedsAttention,
		Review:    snapshot.ReviewPending,
		Working:   snapshot.Working,
		Total:     len(snapshot.Entries),
	}
}

// WaitingText is the one number the menu-bar title and the Dock badge share
// (2026-11-07 round 3): what waits for the user — attention + review, the
// Assistants' "needs you" inbox and the unopened Automation runs folded in by
// the caller — and nothing else. Working agents never count (0.9 plan P12).
// Empty when nothing waits, so the menu bar stays quiet and the Dock badge
// clears.
func WaitingText(counts TrayCounts) string {
	n := counts.Attention + counts.Review
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// TrayTitle is the §17.3 status-first menu-bar title: the waiting count alone
// (no "/total"), empty when nothing waits. Raw token totals never appear in
// the title (usage belongs inside the panel where the Agent context is
// visible).
func TrayTitle(counts TrayCounts) string { return WaitingText(counts) }

// TrayToolTip renders the §17.4 summary wording shared with the titlebar:
// "Solco — 2 need attention, 1 for review, 3 working", "Solco —
// Ready", or "Solco — Herdr disconnected". It reads the one shared
// segment source with the quick-panel header (§17.4).
func TrayToolTip(counts TrayCounts, connected bool) string {
	if !connected {
		return "Solco — Herdr disconnected"
	}
	segments := attentionSegments(counts, "for review")
	if len(segments) == 0 {
		return "Solco — Ready"
	}
	return "Solco — " + strings.Join(segments, ", ")
}

// TrayRemoteDesktop marks the menu bar while a phone is on this Mac's screen: a screen glyph before the
// status title and the viewer's name in the tooltip, so a session can never run unnoticed. `remote` is
// "" when no session is live (title and tooltip pass through unchanged).
func TrayRemoteDesktop(title, tip, remote string) (string, string) {
	if remote == "" {
		return title, tip
	}
	return strings.TrimSpace("🖥 " + title), tip + " · Remote Desktop: " + remote
}

// TrayFingerprint captures exactly the snapshot state the tray may react to
// (0.7 §18): agent enter/leave, status, unread/review, interaction
// attention, connection state, and title/project identity. Navigation
// selection alone never changes it, so a selection change cannot rebuild
// the tray. Entry order is normalized; the derived snapshot order is a
// presentation concern.
func TrayFingerprint(snapshot StatusCenterSnapshot, connected bool) string {
	rows := make([]string, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		rows = append(rows, fmt.Sprintf("%s|%s|%s|%s|%s|%t|%t|%t",
			entry.Card.Key.TerminalID,
			entry.Card.PaneID,
			entry.Card.Title,
			entry.Card.ProjectName,
			string(entry.Card.RuntimePhase)+"/"+agent.OperationalLabel(entry.Card.Attention),
			entry.Card.Unread,
			entry.Card.ReviewPending,
			entry.HasInteraction,
		))
	}
	sort.Strings(rows)
	sum := sha256.Sum256([]byte(fmt.Sprintf("connected=%t|interactions=%d|%s",
		connected, snapshot.Interactions, strings.Join(rows, "\x00"))))
	return hex.EncodeToString(sum[:])
}
