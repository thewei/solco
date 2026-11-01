package nativeui

import (
	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 Box/Spinner、operational.go 的 operationalState/operationalTone、design_system 的 StatusColor、tooltip.go 的 tipped
 * [OUTPUT]: 对外提供 statusSlotSize（状态槽位边长）、statusIndicator（按 operationalState 画槽位）、statusIndicatorTone（按 StatusTone 画槽位，汇总行用）、statusIndicatorService（服务方块）、indicatorShapeFor（tone→形状的唯一规则）、statusSlotEmpty（占位但不画）
 * [POS]: 全应用 Agent/行状态的唯一指示器（2026-10-30 用户拍板，design: designs/agent-surfaces）：16pt 槽位里 working=转圈、attention/review=实心圆点、服务=圆角方块（statusIndicatorService；形状表种类、颜色表紧急度，2026-10-30 UX audit）、idle=空心环，点与 Loading 同位重叠；侧栏 Agents/Workspace 行、Pane 卡片头、Chat 头、切换器与 Tasks 行都调它，不再各画各的圆点
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// statusSlotSize is the square every state indicator occupies, so a
// spinner and a dot sit on the same spot and rows keep one trailing column.
const statusSlotSize float32 = 16

// statusShapeMotion is how a state change reads in the slot: the old shape
// shrinks away as the new one grows in (working → idle is a hand-over, not a
// cut), and a dot that keeps its shape eases to the new colour.
var statusShapeMotion = ui.ElementTransition{
	Colors:   true,
	Duration: motionHandOver,
	Enter:    &ui.Motion{Scale: 0.5},
	Exit:     &ui.Motion{Scale: 0.5},
}

type indicatorShape uint8

const (
	shapeDot indicatorShape = iota
	shapeSpinner
	shapeRing
)

// indicatorShapeFor is the one rule from a status tone to its shape:
// working turns, quiet states (idle, muted, neutral) are a hollow ring,
// everything that wants a look — attention, review, a running service — is a
// solid dot.
func indicatorShapeFor(tone StatusTone) indicatorShape {
	switch tone {
	case ToneWorking:
		return shapeSpinner
	case ToneMuted, ToneNeutral:
		return shapeRing
	default:
		return shapeDot
	}
}

// indicatorToneFor maps an Agent's operational state to the tone its slot
// draws; ok is false for a state with nothing to say (no Agent).
func indicatorToneFor(state operationalState) (StatusTone, bool) {
	switch state {
	case opUnknown:
		return ToneNeutral, false
	case opIdle, opResolved:
		return ToneMuted, true
	default:
		return operationalTone(state), true
	}
}

// statusIndicator draws an Agent state in its slot, with the state's name as
// the tooltip. A state with nothing to say keeps the slot empty so the
// trailing column stays aligned.
func statusIndicator(c *ui.Context, state operationalState) ui.Element {
	tone, ok := indicatorToneFor(state)
	if !ok {
		return statusSlotEmpty(c)
	}
	return statusIndicatorTone(c, tone, operationalLabel(state))
}

// statusIndicatorTone draws one slot for a tone. detail, when set, becomes
// the tooltip. The spinner paints only while its row shows (mygo drives the
// frames), so idle rows cost nothing.
func statusIndicatorTone(c *ui.Context, tone StatusTone, detail string) ui.Element {
	t := c.Theme()
	color := designTokens(t.Dark).StatusColor(tone, t.Dark)
	slot := tipped(c, ui.Box(c).Size(statusSlotSize, statusSlotSize).Shrink(0).Center(), detail)
	slot.Children(func() {
		switch indicatorShapeFor(tone) {
		case shapeSpinner:
			ui.Spinner(c).Size(14, 14).TextColor(color).Transition(statusShapeMotion).PassThrough()
		case shapeRing:
			ui.Box(c).Size(8, 8).Radius(4).Border(1.5, t.TextMuted).Transition(statusShapeMotion).PassThrough()
		default:
			ui.Box(c).Size(7, 7).Radius(3.5).Background(color).Transition(statusShapeMotion).PassThrough()
		}
	})
	return slot
}

// statusIndicatorService draws a running service in the same slot as an Agent
// state but as a rounded square: shape says what kind of thing is running,
// colour only how urgent it is, so a green square (a dev server is up) never
// reads as a green dot (an Agent is ready for review).
func statusIndicatorService(c *ui.Context, tone StatusTone, detail string) ui.Element {
	t := c.Theme()
	color := designTokens(t.Dark).StatusColor(tone, t.Dark)
	slot := tipped(c, ui.Box(c).Size(statusSlotSize, statusSlotSize).Shrink(0).Center(), detail)
	slot.Children(func() {
		ui.Box(c).Size(7, 7).Radius(2).Background(color).Transition(statusShapeMotion).PassThrough()
	})
	return slot
}

// statusSlotEmpty reserves the slot without drawing in it.
func statusSlotEmpty(c *ui.Context) ui.Element {
	return ui.Box(c).Size(statusSlotSize, statusSlotSize).Shrink(0).PassThrough()
}
