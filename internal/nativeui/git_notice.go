package nativeui

/**
 * [INPUT]: runGitOpBudget 的失败（gitworkbench.Explain 的人话、冲突路径、merge 是否停在半路）
 * [OUTPUT]: gitNoticeState + gitNoticeDialog：合并/变基没能直接完成时的说明弹窗
 * [POS]: 用户要求（2026-11-03）：不能直接 merge/rebase 时弹窗说清是冲突还是别的原因，再由用户处理——toast 会在 4 s 内消失、装不下文件列表；弹窗只读，不做任何 Git 调用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// integrationOps are the operations whose failure deserves a dialog.
var integrationOps = map[string]bool{
	"Fast-forward Merge": true,
	"Merge Commit":       true,
	"Rebase":             true,
	"Interactive Rebase": true,
}

// gitNoticeState is one open explanation of why a merge/rebase did not land.
type gitNoticeState struct {
	open  bool
	title string
	body  string
	paths []string
}

// openGitNotice words the failure of an integration op. paused means a merge
// stopped half-way (MERGE_HEAD present): Local Changes then offers
// Continue/Abort; a stopped rebase is always aborted by the Runner.
func (s *Shell) openGitNotice(op string, err error, paths []string, paused bool) {
	n := &s.gitNotice
	n.paths = paths
	reason := gitFailureText(err)
	switch {
	case (op == "Rebase" || op == "Interactive Rebase") && len(paths) > 0:
		n.title = "Rebase Stopped on Conflicts"
		n.body = "The rebase was cancelled and nothing changed. Merge instead, or rebase in the Terminal and resolve the conflicts there."
	case paused:
		n.title = "Merge Needs Your Help"
		n.body = "These files conflict. The merge is paused in your working tree: resolve them in Local Changes, then continue — or abort to go back."
	case op == "Fast-forward Merge":
		n.title = "Can't Fast-Forward"
		n.body = capitalizeFirst(reason)
	default:
		n.title = op + " Didn't Happen"
		n.body = capitalizeFirst(reason)
	}
	n.open = true
}

func capitalizeFirst(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

const gitNoticeMaxPaths = 8

func (s *Shell) gitNoticeDialog(c *ui.Context) {
	n := &s.gitNotice
	if !n.open {
		return
	}
	ui.Modal(c, &n.open, func() {
		ui.Column(c).Width(430).MaxWidth(480).Gap(Spacing().L).Children(func() {
			modalTitle(c, n.title)
			ui.Text(c, n.body).FontSize(Typography().Body)
			if len(n.paths) > 0 {
				ui.Column(c).Gap(Spacing().XS).Children(func() {
					for i, path := range n.paths {
						if i == gitNoticeMaxPaths {
							ui.Text(c, "and "+itoa(len(n.paths)-i)+" more").FontSize(Typography().Caption).TextColor(c.Theme().TextMuted)
							break
						}
						ui.Text(c, path).FontSize(Typography().BodySmall).Font(gdCodeFont()).SingleLine()
					}
				})
			}
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				if regularPrimaryButton(c, "OK").AutoFocus().Clicked() {
					n.open = false
				}
			})
		})
	})
}
