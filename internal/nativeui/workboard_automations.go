package nativeui

/**
 * [INPUT]: 依赖 board 缓存 extras.automations（`automations --json`，整份清单一次读出）、runBoardManage/startBoardManage 的写与确认通道
 * [OUTPUT]: projectAutomations（Project 页的 Automations 组：名称、触发器、下次/关闭原因，On·Off / Run… / Remove…）、automationName、projectAutomationList
 * [POS]: 自动化管理面（P7）：管理（On·Off/Run/Remove）+ 组头 “New Automation…” 入口（创建见 workboard_newautomation.go）；Run 真起 Agent 耗 token，Remove 不可撤销，二者都先确认；没有自动化的 Project 仍画组头（只有创建按钮），“其他”伪 Project 无自动化时不画
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"
	"time"

	"github.com/thewei/solco/internal/workboard"
)

// projectAutomationList is the Project's automations, by name.
func (s *Shell) projectAutomationList(project string) []workboard.Automation {
	var out []workboard.Automation
	for _, a := range s.workboard.board.extras.automations {
		if strings.EqualFold(a.Repo, project) {
			out = append(out, a)
		}
	}
	return out
}

// automationName is an automation's name by id, for confirms and toasts.
func (s *Shell) automationName(id string) string {
	for _, a := range s.workboard.board.extras.automations {
		if a.ID == id {
			return "“" + a.Name + "”"
		}
	}
	return "the automation"
}

// automationCaption is the trigger and, while it is on, when it fires next;
// off, why when the plugin said.
func automationCaption(a workboard.Automation) string {
	parts := []string{a.Trigger}
	switch {
	case !a.On && a.OffWhy != "":
		parts = append(parts, "off: "+a.OffWhy)
	case !a.On:
		parts = append(parts, "off")
	case a.Next > 0:
		parts = append(parts, "next "+time.UnixMilli(a.Next).Local().Format("Mon 15:04"))
	}
	if a.Chat {
		parts = append(parts, "chat only")
	} else if a.Research {
		parts = append(parts, "research only")
	}
	return strings.Join(parts, " · ")
}
