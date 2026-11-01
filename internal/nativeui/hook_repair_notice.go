package nativeui

import (
	"strings"

	"github.com/thewei/solco/internal/agenthooks"
)

/*
 * [INPUT]: 依赖 agenthooks.Manager.Diagnose（本地文件读取）与 Shell.pendingToastAction
 * [OUTPUT]: hooksNeedingRepair、Shell.noticeHookRepair（每次启动在首个会话进入后检查一次，已装过的状态 hook 过期/接线缺失时提示一条带「Update」按钮的 toast，按钮直达 /settings/providers）
 * [POS]: Hook Doctor 的闭环入口：从未安装过的 hook 不提示（不打扰），只提示 Solco 自己装过、升级后落后的那些；修复动作仍是 Providers 页的用户确认安装，不在此自动写配置
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// hooksNeedingRepair names the providers whose hook Solco installed earlier
// (a script on disk) and that is no longer current: an older script version,
// or wiring that lost an event. A hook that was never installed is the
// user's choice and stays quiet.
func hooksNeedingRepair(rows []agenthooks.Diagnosis) []string {
	var names []string
	for _, row := range rows {
		if row.Version == 0 {
			continue
		}
		if row.State == agenthooks.StateOutdated || row.State == agenthooks.StateNotInstalled {
			names = append(names, row.Provider.DisplayName())
		}
	}
	return names
}

// hookRepairMessage is the toast line for the providers hooksNeedingRepair named.
func hookRepairMessage(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, " and ") + " live status needs an update"
}

// noticeHookRepair runs once per launch, off the render path.
func (s *Shell) noticeHookRepair() {
	if s.hookRepairChecked || s.win == nil || s.providers.hooks == nil {
		return
	}
	s.hookRepairChecked = true
	hooks := s.providers.hooks
	s.dispatch(func() {
		message := hookRepairMessage(hooksNeedingRepair(hooks.Diagnose()))
		if message == "" {
			return
		}
		s.applyOnWindow(func() {
			s.pendingToastAction = toastAction{message: message, label: "Update", action: func() { s.router.Push("/settings/providers") }}
		})
	})
}
