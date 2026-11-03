package nativeui

/**
 * [INPUT]: 依赖 gitService 的 upstream（ahead/behind）、已加载的提交图（commits）、ChangesSnapshot.Head，以及 pushRepo/pullRepo/fetchRepo/showGitCommits
 * [OUTPUT]: 对外提供 Shell.gitCleanState（Changes 干净态的状态页：标题 + 分支/ahead 芯片 + 同步主按钮 + 最近提交）与 headCommits（从提交图里沿 HEAD 第一父链取最近 n 条，纯数据可测）
 * [POS]: Changes 工作区干净时中间区的唯一内容（2026-10-30 UX 审计第二轮，design: designs/ux-audit-2 议题 3 方案 A）；只读展示 + 既有 Git 动作入口，不新增 Git 调用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// cleanStateRecent is how many recent commits the clean page lists.
const cleanStateRecent = 3

// headCommits lists up to n commits of HEAD's first-parent line, newest
// first, from the loaded commit graph (which spans every branch). It stops
// where the loaded window ends.
func headCommits(commits []gitworkbench.CommitInfo, head string, n int) []gitworkbench.CommitInfo {
	byHash := make(map[string]gitworkbench.CommitInfo, len(commits))
	for _, cm := range commits {
		byHash[cm.Hash] = cm
	}
	var out []gitworkbench.CommitInfo
	for hash := head; len(out) < n; {
		cm, ok := byHash[hash]
		if !ok {
			break
		}
		out = append(out, cm)
		if len(cm.Parents) == 0 {
			break
		}
		hash = cm.Parents[0]
	}
	return out
}

// gitCleanState is the Changes view of a clean worktree: say so, say where
// the branch stands against its upstream with the one action that follows
// (Push when ahead, Pull when behind, else Fetch), and list the latest
// commits — the unpushed ones marked.
func (s *Shell) gitCleanState(c *ui.Context, title, detail string) {
	t := c.Theme()
	pal := gdPaletteFor(t)
	sp := Spacing()
	snap := s.gitSnapshot()
	st := s.git.upstream
	branch := s.currentBranchName()
	busy := s.git.opBusy || s.git.sequencerChecked && s.git.sequencer.Active()

	ui.Column(c).Grow(1).MinWidth(0).Padding(sp.XL).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(640).Gap(sp.L).Children(func() {
			ui.Row(c).FillWidth().Gap(sp.L).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Icon(c, iconCheck).Size(18, 18).TextColor(pal.addText).Shrink(0)
				ui.Column(c).Grow(1).Basis(200).MinWidth(0).Gap(sp.XS).Children(func() {
					ui.Text(c, title).FontSize(Typography().Section).FontWeight(WeightSemibold)
					ui.Row(c).Gap(sp.M).AlignItems(ui.Center).Wrap().Children(func() {
						if branch != "" {
							ui.Text(c, branch).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
						} else if detail != "" {
							ui.Text(c, detail).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
						}
						s.upstreamChips(c, st, pal)
					})
				})
				if branch != "" {
					switch {
					case st.Diverged():
						// Push is refused for sure and a fast-forward pull too:
						// the one way forward is to bring the remote's work in.
						if compactPrimaryButton(c, "Merge "+st.Remote+"/"+st.Branch+"…").Disabled(busy).Clicked() {
							s.openMergeDialog(st.Remote + "/" + st.Branch)
						}
					case !st.OK:
						if compactPrimaryButton(c, "Publish Branch").Disabled(busy).Clicked() {
							s.pushRepo()
						}
					case st.Ahead > 0:
						if compactPrimaryButton(c, "Push "+itoa(st.Ahead)+" commit"+pluralS(st.Ahead)).Disabled(busy).Clicked() {
							s.pushRepo()
						}
					case st.Behind > 0:
						if compactPrimaryButton(c, "Pull "+itoa(st.Behind)+" commit"+pluralS(st.Behind)).Disabled(busy).Clicked() {
							s.pullRepo()
						}
					default:
						if compactButton(c, "Fetch").Disabled(busy).Clicked() {
							s.fetchRepo()
						}
					}
				}
			})

			if snap != nil {
				if recent := headCommits(s.git.commits, snap.Head, cleanStateRecent); len(recent) > 0 {
					s.cleanStateCommits(c, recent, st.Ahead)
				}
			}
			ui.Row(c).Gap(sp.S).Children(func() {
				if compactButton(c, "View All Commits").Clicked() {
					s.showGitCommits()
				}
				if compactButton(c, "Open Terminal").Clicked() {
					s.showSurface(WorkspaceSurfaceTerminal)
				}
			})
		})
	})
}

// upstreamChips says where the branch stands against its upstream: ahead,
// behind, or nothing to say when level.
func (s *Shell) upstreamChips(c *ui.Context, st gitworkbench.UpstreamStatus, pal *gdPalette) {
	t := c.Theme()
	switch {
	case !st.OK:
		ui.Text(c, "No upstream").FontSize(Typography().BodySmall).TextColor(t.TextMuted)
	case st.Ahead == 0 && st.Behind == 0:
		ui.Text(c, "Up to date with "+st.Remote+"/"+st.Branch).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
	default:
		if st.Ahead > 0 {
			ui.Text(c, "↑"+itoa(st.Ahead)+" ahead of "+st.Remote+"/"+st.Branch).FontSize(Typography().BodySmall).
				FontWeight(WeightSemibold).TextColor(pal.addText)
		}
		if st.Behind > 0 {
			ui.Text(c, "↓"+itoa(st.Behind)+" behind").FontSize(Typography().BodySmall).
				FontWeight(WeightSemibold).TextColor(pal.delText)
		}
		if st.Diverged() {
			ui.Text(c, "Diverged — merge or rebase before pushing").FontSize(Typography().BodySmall).TextColor(t.TextMuted)
		}
	}
}

// cleanStateCommits lists the latest commits of HEAD, the first `ahead` of
// them marked unpushed.
func (s *Shell) cleanStateCommits(c *ui.Context, recent []gitworkbench.CommitInfo, ahead int) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	sp := Spacing()
	now := time.Now()
	ui.Column(c).FillWidth().Gap(sp.XS).Children(func() {
		ui.Text(c, "Recent Commits").FontSize(Typography().Caption).FontWeight(WeightSemibold).TextColor(t.TextMuted).
			Padding(0, 0, sp.XS)
		for i, cm := range recent {
			ui.Row(c).FillWidth().Height(Controls().RegularHeight).Gap(sp.L).AlignItems(ui.Center).
				BorderWidth(0, 0, boolPx(i < len(recent)-1), 0).BorderColor(tokens.BorderSubtle).Children(func() {
				ui.Text(c, cm.Short).Font(gdCodeFont()).FontSize(Typography().Caption).TextColor(t.TextMuted).Shrink(0).SingleLine()
				tipped(c, ui.Text(c, cm.Subject).FontSize(Typography().Body).SingleLine().Ellipsis("…").Grow(1).MinWidth(0), cm.Subject)
				age := agentAgo(cm.Time, now)
				if i < ahead {
					age += " · unpushed"
				}
				ui.Text(c, age).FontSize(Typography().Caption).TextColor(t.TextMuted).Shrink(0).SingleLine()
			})
		}
	})
}

// boolPx is a 1-point rule when on, none when off.
func boolPx(on bool) float32 {
	if on {
		return 1
	}
	return 0
}
