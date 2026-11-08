package nativeui

/**
 * [INPUT]: 一个过滤串与若干候选文本
 * [OUTPUT]: filterMatches——大小写不敏感的子串过滤（空串全匹配），弹层列表（项目/设备/Tab）共用
 * [POS]: 原先寄居在 Git 参考侧栏文件里，2026-11-08 G5 随 Git 整页退役搬出
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import "strings"

// filterMatches reports whether any value holds the filter, ignoring case; an
// empty filter matches everything. The popover lists (projects, devices, tabs)
// narrow with it.
func filterMatches(filter string, values ...string) bool {
	q := strings.ToLower(strings.TrimSpace(filter))
	if q == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), q) {
			return true
		}
	}
	return false
}
