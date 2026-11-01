package nativeui

import "time"

/**
 * [INPUT]: 无（纯常量）
 * [OUTPUT]: 提供动效时长四档 motionColor / motionRow / motionPanel / motionHandOver
 * [POS]: nativeui 里所有 ui.ElementTransition / Animate 的时长唯一来源（2026-11-01 收口，此前散落 120/140/150/160/170/180/190ms 七种）；侧栏滑动 panelSlideDuration 与工作区进入 260ms 是整页级长动效，不在这四档里
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The four lengths a small piece of the interface may take to change. Callers
// name the one that fits and never write a duration of their own.
const (
	// motionColor eases a face between rest, hover and selection.
	motionColor = 120 * time.Millisecond
	// motionRow is a row or small item arriving, leaving or turning (a chevron).
	motionRow = 140 * time.Millisecond
	// motionPanel is a chip, card or panel growing, shrinking or sliding.
	motionPanel = 160 * time.Millisecond
	// motionHandOver is one shape giving way to the next (the status slot).
	motionHandOver = 180 * time.Millisecond
)
