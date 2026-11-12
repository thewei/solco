//go:build darwin

package nativeui

/**
 * [INPUT]: purego/objc（AppKit：NSScreen、NSWindow），只用 Apple 文档里的具名常量
 * [OUTPUT]: 对外提供 islandNotchSize、islandMenuBarHeight、islandRaise、islandPin
 * [POS]: 刘海岛的 macOS 原生缝（2026-11-12 决策 + H1 spike）：量刘海、把非激活 Panel 抬到菜单栏之上并跨所有 Space / 全屏 App、钉在屏幕顶部中央。不含任何第三方代码；不碰 Herdr、终端或任何运行时
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import "github.com/ebitengine/purego/objc"

// AppKit's documented constants (NSWindow.h).
const (
	islandStyleMaskNonactivatingPanel  = 1 << 7  // NSWindowStyleMaskNonactivatingPanel
	islandStyleMaskFullSizeContentView = 1 << 15 // NSWindowStyleMaskFullSizeContentView

	islandCollectionJoinsAllSpaces      = 1 << 0 // NSWindowCollectionBehaviorCanJoinAllSpaces
	islandCollectionStationary          = 1 << 4 // NSWindowCollectionBehaviorStationary
	islandCollectionIgnoresCycle        = 1 << 6 // NSWindowCollectionBehaviorIgnoresCycle
	islandCollectionFullScreenAuxiliary = 1 << 8 // NSWindowCollectionBehaviorFullScreenAuxiliary

	islandMainMenuLevel = 24 // NSMainMenuWindowLevel
)

type islandPoint struct{ X, Y float64 }
type islandSize struct{ W, H float64 }
type islandRect struct {
	Origin islandPoint
	Size   islandSize
}
type islandInsets struct{ Top, Left, Bottom, Right float64 }

func islandSel(name string) objc.SEL { return objc.RegisterName(name) }

// islandScreen is the display that carries the menu bar: NSScreen's first.
func islandScreen() objc.ID {
	screens := objc.ID(objc.GetClass("NSScreen")).Send(islandSel("screens"))
	if objc.Send[uint](screens, islandSel("count")) == 0 {
		return 0
	}
	return screens.Send(islandSel("objectAtIndex:"), uint(0))
}

// islandNotchSize is the camera housing's size in points: the strip of the
// top edge that the two auxiliary areas leave out, as high as the top safe-area
// inset. False when the display has no housing.
func islandNotchSize() (w, h float64, ok bool) {
	screen := islandScreen()
	if screen == 0 || !objc.Send[bool](screen, islandSel("respondsToSelector:"), islandSel("safeAreaInsets")) {
		return 0, 0, false
	}
	insets := objc.Send[islandInsets](screen, islandSel("safeAreaInsets"))
	if insets.Top <= 0 {
		return 0, 0, false
	}
	frame := objc.Send[islandRect](screen, islandSel("frame"))
	leftArea := objc.Send[islandRect](screen, islandSel("auxiliaryTopLeftArea"))
	rightArea := objc.Send[islandRect](screen, islandSel("auxiliaryTopRightArea"))
	return frame.Size.W - leftArea.Size.W - rightArea.Size.W, insets.Top, true
}

// islandMenuBarHeight is what the menu bar takes off the top of the screen
// that carries it.
func islandMenuBarHeight() float64 {
	screen := islandScreen()
	if screen == 0 {
		return 0
	}
	frame := objc.Send[islandRect](screen, islandSel("frame"))
	visible := objc.Send[islandRect](screen, islandSel("visibleFrame"))
	return (frame.Origin.Y + frame.Size.H) - (visible.Origin.Y + visible.Size.H)
}

// islandRaise makes a window a borderless non-activating panel above the menu
// bar that follows every Space and stays over full-screen apps, and that never
// takes part in the window cycle: clicking it does not activate Solco.
func islandRaise(handle uintptr) {
	w := objc.ID(handle)
	w.Send(islandSel("setStyleMask:"), uint(islandStyleMaskNonactivatingPanel|islandStyleMaskFullSizeContentView))
	w.Send(islandSel("setLevel:"), islandMainMenuLevel+3)
	w.Send(islandSel("setCollectionBehavior:"),
		uint(islandCollectionJoinsAllSpaces|islandCollectionStationary|islandCollectionIgnoresCycle|islandCollectionFullScreenAuxiliary))
}

// islandPin puts a window of w×h points at the top centre of the menu-bar
// display, over the menu bar, where AppKit would not otherwise let it go.
func islandPin(handle uintptr, w, h float64) {
	screen := islandScreen()
	if screen == 0 {
		return
	}
	f := objc.Send[islandRect](screen, islandSel("frame"))
	frame := islandRect{islandPoint{f.Origin.X + (f.Size.W-w)/2, f.Origin.Y + f.Size.H - h}, islandSize{w, h}}
	objc.ID(handle).Send(islandSel("setFrame:display:"), frame, true)
}
