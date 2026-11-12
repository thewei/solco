//go:build !darwin

package nativeui

/**
 * [INPUT]: 无
 * [OUTPUT]: 对外提供 islandNotchSize、islandMenuBarHeight、islandRaise、islandPin、islandWatchPointer、islandScreenFrame、islandMouseLocation、islandButtonsDown 的空实现
 * [POS]: 刘海岛只属于 macOS；其他平台这些缝什么都不做，island.go 在 GOOS != darwin 时根本不建窗口
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func islandNotchSize() (w, h float64, ok bool)            { return 0, 0, false }
func islandMenuBarHeight() float64                        { return 0 }
func islandRaise(handle uintptr)                          {}
func islandPin(handle uintptr, w, h float64)              {}
func islandWatchPointer(fn func()) (stop func(), ok bool) { return func() {}, false }
func islandScreenFrame() (w, h float64, ok bool)          { return 0, 0, false }
func islandMouseLocation() (x, y float64)                 { return 0, 0 }
func islandButtonsDown() bool                             { return false }
