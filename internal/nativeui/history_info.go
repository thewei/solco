package nativeui

/**
 * [INPUT]: 依赖 history.SessionMeta / SessionUsage、historyPreview 的懒加载用量、共享 Design Tokens、gorex 图标按钮与 regularButton
 * [OUTPUT]: 提供会话详情表 sessionInfoTable（时间、文件夹、会话 ID、继续命令、换 Agent 命令、用量、路由、源文件、内容操作；命令/ID/路径各带复制）、行内动作条 sessionRowActions，以及详情页的可折叠详情卡 historyDetailInfo
 * [POS]: Sessions 列表展开行与 /history/{id} 共用的唯一详情表面；只读，用量由 history_preview.go 现读，不存任何会话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

const (
	// infoLabelWidth is the label column of the detail table.
	infoLabelWidth float32 = 104
	// infoMaxRoutes bounds the routing lines; the rest fold into "+N more".
	infoMaxRoutes = 4
)

// shellQuoted wraps one word for a POSIX shell, so a copied command survives
// spaces and quotes in the folder or file name.
func shellQuoted(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// sessionCd is the folder prefix of a copyable command.
func sessionCd(meta history.SessionMeta) string {
	if meta.ProjectPath == "" {
		return ""
	}
	return "cd " + shellQuoted(meta.ProjectPath) + " && "
}

// sessionSwitchCommand is the command that carries the session over to a
// sibling Agent that reads the same session file. Only the Pi family has one
// (pi --session / omp --resume both take the file); there is no honest
// command for other pairs, so none is offered.
func sessionSwitchCommand(meta history.SessionMeta) (label, command string, ok bool) {
	if meta.FilePath == "" {
		return "", "", false
	}
	switch meta.Agent {
	case history.AgentPi:
		return "Continue in omp", sessionCd(meta) + "omp --resume " + shellQuoted(meta.FilePath), true
	case history.AgentOMP:
		return "Continue in pi", sessionCd(meta) + "pi --session " + shellQuoted(meta.FilePath), true
	}
	return "", "", false
}

// sessionTimeSpan is "start – end": the date once when both ends share a day,
// the year only when it is not this one.
func sessionTimeSpan(createdMs, updatedMs int64, now time.Time) string {
	if createdMs <= 0 && updatedMs <= 0 {
		return ""
	}
	if createdMs <= 0 {
		createdMs = updatedMs
	}
	if updatedMs < createdMs {
		updatedMs = createdMs
	}
	start, end := time.UnixMilli(createdMs).Local(), time.UnixMilli(updatedMs).Local()
	dateLayout := "Jan 2"
	if start.Year() != now.Local().Year() {
		dateLayout = "Jan 2, 2006"
	}
	if start.YearDay() == end.YearDay() && start.Year() == end.Year() {
		if start.Equal(end) || start.Format("15:04") == end.Format("15:04") {
			return start.Format(dateLayout + ", 15:04")
		}
		return start.Format(dateLayout+", 15:04") + " – " + end.Format("15:04")
	}
	return start.Format(dateLayout+", 15:04") + " – " + end.Format(dateLayout+", 15:04")
}

// sessionUsageLine is "Input 1.8k · Output 468 · Cache 6.5k"; the cache part
// is left out when nothing was cached.
func sessionUsageLine(u history.SessionUsage) string {
	parts := []string{
		"Input " + agent.FormatTokenCount(u.Input),
		"Output " + agent.FormatTokenCount(u.Output),
	}
	if cache := u.Cache(); cache > 0 {
		parts = append(parts, "Cache "+agent.FormatTokenCount(cache))
	}
	return strings.Join(parts, " · ")
}

// sessionRouteName is "provider/model", or the model alone.
func sessionRouteName(r history.UsageRoute) string {
	if r.Provider == "" {
		return r.Model
	}
	return r.Provider + "/" + r.Model
}

// sessionRouteFacts is the muted tail of a routing line.
func sessionRouteFacts(r history.UsageRoute) string {
	var parts []string
	if r.Level != "" {
		parts = append(parts, r.Level)
	}
	parts = append(parts, fmt.Sprintf("%d call%s", r.Calls, pluralS(r.Calls)))
	if r.Tokens > 0 {
		parts = append(parts, agent.FormatTokenCount(r.Tokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}

// copyIconButton is the small copy glyph beside a value.
func (s *Shell) copyIconButton(c *ui.Context, text, what string) {
	if gorexIconButton(c, gorexColorsOf(c.Theme().Dark), iconCopy, "Copy "+what, "Copy "+what, false, gorexRowBtn, gorexRowGlyph).
		Shrink(0).Clicked() {
		s.copyToClipboard(text)
	}
}

// sessionInfoTable is the detail table of one session. info carries the lazy
// usage read; withView adds the "View Session" action (the list's way into
// the transcript — the detail page is already there).
func (s *Shell) sessionInfoTable(c *ui.Context, meta history.SessionMeta, info historyPreview, withView bool) {
	sp := Spacing()
	t := c.Theme()
	tokens := designTokens(t.Dark)
	muted := func(text string) {
		ui.Text(c, text).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
	}
	row := func(label string, value func()) {
		ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(Typography().Caption).TextColor(t.TextMuted).
				Width(infoLabelWidth).Shrink(0).SingleLine().Ellipsis("…")
			value()
		})
	}
	// code is a value in the mono face, copyable.
	code := func(label, text, what string) {
		row(label, func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Padding(2, sp.S).Radius(Radius().Row).Background(tokens.PanelHover).MinWidth(0).Shrink(1).Children(func() {
					tipped(c, ui.Text(c, text).Font(gdCodeFont()).FontSize(Typography().BodySmall).SingleLine().Ellipsis("…"), text)
				})
				s.copyIconButton(c, text, what)
			})
		})
	}
	ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
		if span := sessionTimeSpan(meta.CreatedAt, meta.UpdatedAt, time.Now()); span != "" {
			row("Time", func() { ui.Text(c, span).FontSize(Typography().BodySmall) })
		}
		if meta.ProjectPath != "" {
			row("Folder", func() {
				tipped(c, ui.Text(c, meta.ProjectPath).FontSize(Typography().BodySmall).Font(gdCodeFont()).
					SingleLine().Ellipsis("…").Grow(1).MinWidth(0), meta.ProjectPath)
			})
		}
		if meta.ID != "" {
			code("Session ID", meta.ID, "Session ID")
		}
		if meta.ID != "" {
			code("Continue", sessionResumeCommand(meta), "Resume Command")
		}
		if label, command, ok := sessionSwitchCommand(meta); ok {
			code(label, command, "Command")
		}
		switch {
		case info.loading:
			row("Usage", func() {
				ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(12, 12)
					muted("Reading…")
				})
			})
		case info.usage != nil && (info.usage.Total() > 0 || info.usage.Cache() > 0):
			row("Usage", func() { ui.Text(c, sessionUsageLine(*info.usage)).FontSize(Typography().BodySmall) })
			if routes := info.usage.Routes; len(routes) > 0 {
				row("Routed to", func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						for i, r := range routes {
							if i == infoMaxRoutes {
								muted(fmt.Sprintf("+%d more", len(routes)-infoMaxRoutes))
								break
							}
							ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
								ui.Text(c, sessionRouteName(r)).Font(gdCodeFont()).FontSize(Typography().BodySmall).
									SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
								ui.Text(c, sessionRouteFacts(r)).FontSize(Typography().Caption).TextColor(t.TextMuted).
									SingleLine().Shrink(0)
							})
						}
					})
				})
			}
		}
		if meta.FilePath != "" {
			code("File", meta.FilePath, "File Path")
		}
		row("Transcript", func() {
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				if withView && regularButton(c, "View Session").Clicked() {
					s.router.Push("/history/" + meta.Key)
				}
				if regularButton(c, "Export as Markdown").Clicked() {
					s.exportSessionMarkdown(meta)
				}
				if meta.FilePath != "" && regularButton(c, "Reveal in Finder").Clicked() {
					mygo.Shell.ShowItemInFolder(meta.FilePath)
				}
			})
		})
	})
}

// sessionRowActions are the open row's trailing actions, in place of its date:
// Continue, copy the resume command, Continue in another Agent, Trash.
func (s *Shell) sessionRowActions(c *ui.Context, meta history.SessionMeta) {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	can := s.canContinue()
	if regularButton(c, "Continue").Disabled(!can).Shrink(0).Clicked() {
		s.requestContinue(meta, nil)
	}
	if gorexIconButton(c, k, iconTerminal, "Copy Resume Command", "Copy Resume Command", false, gorexRowBtn+4, gorexRowGlyph+2).
		Shrink(0).Clicked() {
		s.copySessionResume(meta)
	}
	if targets := s.continueTargets(meta); len(targets) > 0 {
		regularButton(c, "Continue in").Disabled(!can).Shrink(0).Menu(func(m *ui.Menu) {
			for _, target := range targets {
				if m.Item(target.DisplayName()).Chosen() {
					s.requestContinue(meta, &target)
				}
			}
		})
	}
	if s.hist.trash != nil {
		if gorexIconButton(c, k, iconTrash, "Move to Trash", "Move to Trash", false, gorexRowBtn+4, gorexRowGlyph+2).
			Shrink(0).Clicked() {
			s.moveSessionsToTrash([]history.SessionMeta{meta})
			s.closeHistoryPreview()
		}
	}
}

// historyDetailInfo is the detail page's collapsible card: a "Session details"
// strip above the transcript (the title itself stays in the title bar) that
// unfolds the same table the list shows. Collapsed by default; the usage read
// starts on the first unfold of a session.
func (s *Shell) historyDetailInfo(c *ui.Context, key string) {
	meta := s.historyDetailMeta(key)
	if meta == nil {
		return
	}
	sp := Spacing()
	t := c.Theme()
	tokens := designTokens(t.Dark)
	open := s.hist.detailInfoOpen
	strip := ui.ButtonBase(c.Key("detail-info:"+key)).FillWidth().Padding(sp.S, sp.XL).Gap(sp.S).AlignItems(ui.Center).
		FocusRing(false).Label("Session details")
	if strip.Hovered() {
		strip.Background(tokens.PanelHover)
	}
	strip.Children(func() {
		treeChevron(c, true, open, t.TextMuted)
		ui.Text(c, "Session details").FontSize(Typography().BodySmall).FontWeight(WeightMedium).TextColor(t.TextMuted)
	})
	if strip.Clicked() {
		s.hist.detailInfoOpen = !open
		open = !open
	}
	if !open {
		return
	}
	if s.hist.detailInfo.key != key {
		s.startDetailUsage(*meta)
	}
	ui.Column(c).FillWidth().Padding(sp.S, sp.XL, sp.M, sp.XL).Children(func() {
		ui.Column(c).FillWidth().Padding(sp.M, sp.L).Radius(Radius().Row).Background(tokens.Content).Children(func() {
			s.sessionInfoTable(c, *meta, s.hist.detailInfo, false)
		})
	})
}

// startDetailUsage reads the open detail page's usage; the result is dropped
// when the page has moved to another session (or closed) meanwhile.
func (s *Shell) startDetailUsage(meta history.SessionMeta) {
	key := meta.Key
	s.hist.detailInfo = historyPreview{key: key, loading: true}
	s.readSessionUsage(context.Background(), meta, func(usage *history.SessionUsage) {
		if s.hist.detailInfo.key == key {
			s.hist.detailInfo = historyPreview{key: key, usage: usage}
		}
	})
}
