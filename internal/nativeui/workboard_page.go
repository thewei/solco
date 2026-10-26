package nativeui

/**
 * [INPUT]: 依赖 workboard_tasks 的 board 缓存/GroupOf 分组/projectForWorktree、workboard_actions 的 workboardActionsFor/startWorkboardAction、internal/workboard Client.Details、route_header 的 routeHeaderSpec、ds_components 的 settingsGroup/settingsCard
 * [OUTPUT]: routeTask（/task 路由）、openWorkboardTaskPage（Tasks 行点击的落点）、workboardTaskPage（页面内容）、taskRouteHeader（标题栏身份：任务名 + id · 分组）
 * [POS]: herdr-projects 单个任务的详情页；一次一个任务，id 存在 workboardPage（路由不带 id：任务 id 含 “/”）；内容 = 显眼的写操作按钮（主按钮：Approve / Approve Plan…）+ 状态（含 Queue/限额）/描述/Plan/失败/Review（含 findings 与往轮）/Handovers/Attempts/Report（Open Report 开服务链接或文件），数据来自 board 缓存加一次新读的 details；任务从看板消失时显示空态并可返回，不猜测
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/solco/internal/herdr"
	"github.com/wh-studio/solco/internal/workboard"
)

const routeTask = "/task"

// workboardPage is the open task page: which task, and the freshest details.
type workboardPage struct {
	id      string
	detail  *workboard.Detail
	loading bool
	draft   string // the unsent message to the task's agent
}

// openWorkboardTaskPage opens the task page from the Tasks tab.
func (s *Shell) openWorkboardTaskPage(id string) {
	if s.workboardBoardMode() {
		s.hideQuickPanel()
		s.openWorkBoard()
		return
	}
	s.hideQuickPanel()
	page := workboardPage{id: id}
	if d, ok := s.workboard.board.details[id]; ok {
		detail := d.detail
		page.detail = &detail
	}
	s.workboard.page = page
	if s.router.Path() == routeTask {
		s.router.Replace(routeTask)
	} else {
		s.router.Push(routeTask)
	}
	s.loadWorkboardPage()
}

// loadWorkboardPage reads the open task's details once, off the UI lane. A
// result for a task the page has since left is dropped.
func (s *Shell) loadWorkboardPage() {
	page := &s.workboard.page
	if page.id == "" || page.loading || !s.workboardAvailable() {
		return
	}
	page.loading = true
	id := page.id
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		detail, err := client.Details(ctx, id)
		s.applyOnWindow(func() {
			page := &s.workboard.page
			page.loading = false
			if page.id != id || err != nil {
				return
			}
			page.detail = &detail
		})
	})
}

// workboardTaskByID finds a task on the last board read.
func (s *Shell) workboardTaskByID(id string) (workboard.Task, bool) {
	for _, task := range s.workboard.board.tasks {
		if task.ID == id {
			return task, true
		}
	}
	return workboard.Task{}, false
}

// workboardTaskGroup groups a task with the freshest details the page has.
func (s *Shell) workboardTaskGroup(task workboard.Task) workboard.Group {
	if page := s.workboard.page; page.id == task.ID && page.detail != nil {
		return workboard.GroupOf(task, page.detail)
	}
	var detail *workboard.Detail
	if d, ok := s.workboard.board.details[task.ID]; ok {
		detail = &d.detail
	}
	return workboard.GroupOf(task, detail)
}

// taskRouteHeader names the task in the title bar.
func (s *Shell) taskRouteHeader() routeHeaderSpec {
	spec := routeHeaderSpec{title: "Task"}
	task, ok := s.workboardTaskByID(s.workboard.page.id)
	if !ok {
		return spec
	}
	spec.title = fallbackText(task.Title, task.ID)
	spec.facts = task.ID + " · " + s.workboardTaskGroup(task).Label()
	spec.actions = func(m *ui.Menu) {
		if m.Item("Open Work Board").Chosen() {
			s.openWorkBoard()
		}
	}
	return spec
}

// workboardTaskPage is the page content: actions first, then what the
// plugin knows about the task.
func (s *Shell) workboardTaskPage(c *ui.Context) {
	sp := Spacing()
	if !s.workboardAvailable() {
		emptyState(c, "The work board isn't available", "")
		return
	}
	task, ok := s.workboardTaskByID(s.workboard.page.id)
	if !ok {
		ui.Column(c).Grow(1).Center().Gap(sp.M).Children(func() {
			emptyState(c, "This task is no longer on the board", "")
			if ui.Button(c, "Back").Clicked() {
				s.router.Back()
			}
		})
		return
	}
	group := s.workboardTaskGroup(task)
	detail := s.workboard.page.detail
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			contentColumn(c, formColumnWidth, func() {
				ui.Column(c).FillWidth().Padding(pageTopInset(), sp.XL, sp.XL).Gap(sp.XL).Children(func() {
					s.workboardTaskActions(c, task, group, detail)
					s.workboardTaskFacts(c, task, group, detail)
					if detail != nil {
						s.workboardTaskNarrative(c, detail, group)
					}
				})
			})
		})
		// A task nobody is on (to do) or that is finished has no agent to tell.
		if group != workboard.GroupTodo && group != workboard.GroupDone {
			s.workboardMessageBox(c, "Message "+task.ID+"'s agent", "Say something to the agent…",
				&s.workboard.page.draft, nil, func(text string, _ bool) { s.sendTaskMessage(task.ID, text) })
		}
	})
}

// workboardTaskActions is the button row: the group's writes (Approve is the
// one primary action), then the way into the task's Project.
func (s *Shell) workboardTaskActions(c *ui.Context, task workboard.Task, group workboard.Group, detail *workboard.Detail) {
	sp := Spacing()
	ui.Row(c).FillWidth().Gap(sp.M).Children(func() {
		for _, a := range workboardActionsForTask(task, group, detail) {
			var button ui.Element
			if isPrimaryWrite(a.Kind) {
				button = ui.PrimaryButton(c, a.Label)
			} else {
				button = ui.Button(c, a.Label)
			}
			if button.Clicked() {
				s.startWorkboardAction(a, task)
			}
		}
		if task.NeedsPick() && ui.Button(c, "Compare in Work Board").Clicked() {
			s.openWorkBoard() // the attempts to pick from are not in the list rows
		}
		pinned := s.workboard.board.extras.pinnedTasks[task.ID]
		pinLabel := "Pin"
		if pinned {
			pinLabel = "Unpin"
		}
		if ui.Button(c, pinLabel).Clicked() {
			s.setWorkboardPin(task.ID, !pinned)
		}
		if detail != nil && projectForWorktree(s.projection, detail.Worktree) != "" {
			if ui.Button(c, "Open Project").Clicked() {
				s.selectProject(projectForWorktree(s.projection, detail.Worktree))
				s.showViewTerminal()
			}
		}
	})
}

// workboardTaskFacts is the status group: only facts the plugin reported.
func (s *Shell) workboardTaskFacts(c *ui.Context, task workboard.Task, group workboard.Group, detail *workboard.Detail) {
	rows := []settingsRow{row("Status", "", func() { valueText(c, group.Label()) })}
	add := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			rows = append(rows, row(label, "", func() { valueText(c, value) }))
		}
	}
	add("Note", task.Msg)
	add("Stage", task.Stage)
	if detail != nil {
		add("Agent", detail.Agent)
		add("Branch", detail.Branch)
		add("Pull request", detail.PR)
		add("Queue", detail.QueueWords)
		add("Time limit", detail.LimitTime)
		add("Token limit", detail.LimitTokens)
	} else {
		add("Pull request", task.PR)
	}
	settingsGroup(c, "Task", rows...)
}

// workboardTaskNarrative is the free text: what was asked, what failed, what
// the reviewer said (g is the task's group). All of it is clipped; the plugin's board has the rest.
func (s *Shell) workboardTaskNarrative(c *ui.Context, d *workboard.Detail, g workboard.Group) {
	t := c.Theme()
	body := func(text string, maxLines int) {
		ui.Text(c, clipPreview(text, 1600, maxLines)).FontSize(Typography().BodySmall).Selectable()
	}
	if d.Description != "" {
		settingsCard(c, "Description", func() { body(d.Description, 12) })
	}
	// The plugin keeps a failure record after a Stop; a task nobody is on (to
	// do) or that is finished is not failing now, so the card would mislead.
	if d.Failure && g != workboard.GroupTodo && g != workboard.GroupDone {
		settingsCard(c, "Failure", func() {
			if d.FailureTitle != "" {
				ui.Text(c, d.FailureTitle).FontWeight(WeightSemibold)
			}
			if d.FailureDetail != "" {
				body(d.FailureDetail, 14)
			}
			if d.FailureNext != "" {
				ui.Text(c, d.FailureNext).FontSize(Typography().Caption).TextColor(t.TextMuted)
			}
		})
	}
	if d.Plan != "" {
		settingsCard(c, "Plan", func() { body(d.Plan, 16) })
	}
	if d.ReviewText != "" || d.ReviewBy != "" || d.ReviewFailed || len(d.Findings) > 0 {
		settingsCard(c, "Review", func() {
			verdict := "Passed"
			if d.ReviewFailed {
				verdict = "Did not pass"
			}
			if d.ReviewFindings > 0 {
				verdict += " · " + strconv.Itoa(d.ReviewFindings) + " finding" + pluralS(d.ReviewFindings)
			}
			if d.ReviewBy != "" {
				verdict += " · " + d.ReviewBy
			}
			ui.Text(c, verdict).FontWeight(WeightSemibold)
			if d.ReviewText != "" {
				body(d.ReviewText, 12)
			}
			workboardFindings(c, d.Findings)
			// The current round is the last of the history; the rest are one line each.
			for i := 0; i+1 < len(d.Reviews); i++ {
				r := d.Reviews[i]
				line := "Round " + strconv.Itoa(i+1) + " · " + map[bool]string{true: "Did not pass", false: "Passed"}[r.Failed]
				if n := len(r.Findings); n > 0 {
					line += " · " + strconv.Itoa(n) + " finding" + pluralS(n)
				}
				ui.Text(c, line).FontSize(Typography().Caption).TextColor(t.TextMuted)
			}
		})
	}
	if d.LimitStopped != "" {
		settingsCard(c, "Stopped at a limit", func() { body(d.LimitStopped, 4) })
	}
	if len(d.Handovers) > 0 {
		settingsCard(c, "Handovers", func() {
			for _, h := range d.Handovers {
				body(h, 3)
			}
		})
	}
	if len(d.Attempts) > 0 {
		settingsCard(c, "Attempts", func() {
			for _, a := range d.Attempts {
				line := strings.ToUpper(a.Letter) + " · " + fallbackText(a.Agent, "agent") + " · " + a.Standing
				if a.Stat != "" {
					line += " · " + a.Stat
				}
				ui.Text(c, line).FontSize(Typography().BodySmall)
			}
		})
	}
	if d.ReportLink != "" {
		settingsCard(c, "Report", func() {
			ui.Text(c, clipPreview(d.ReportLink, 300, 2)).FontSize(Typography().Caption).TextColor(t.TextMuted).Selectable()
			if ui.Button(c, "Open Report").Clicked() {
				s.openBoardLink(d.ReportLink)
			}
		})
	}
}

// workboardFindings lists a reviewer's findings: the plugin's severity word,
// the subject, then where and the proof, clipped.
func workboardFindings(c *ui.Context, findings []workboard.Finding) {
	t := c.Theme()
	sp := Spacing()
	for _, f := range findings {
		if f.Subject == "" {
			continue
		}
		ui.Row(c).FillWidth().Gap(sp.M).Children(func() {
			ui.Text(c, strings.ToUpper(fallbackText(f.Severity, "major"))).FontSize(Typography().Micro).
				FontWeight(WeightSemibold).TextColor(t.TextMuted).Width(52).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				ui.Text(c, clipPreview(f.Subject, 400, 4)).FontSize(Typography().BodySmall).Selectable()
				for _, extra := range []string{f.Where, f.Proof} {
					if extra != "" {
						ui.Text(c, clipPreview(extra, 400, 3)).FontSize(Typography().Caption).TextColor(t.TextMuted).Selectable()
					}
				}
			})
		})
	}
}

// openBoardLink opens what the plugin gave: a served http(s) report in the
// browser, a file's path (no page server) in the system's opener. Anything
// else is refused.
func (s *Shell) openBoardLink(link string) {
	switch {
	case strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://"):
		if err := openBoardURL(link); err != nil {
			s.pendingToast = "Could not open the report"
		}
	case strings.HasPrefix(link, "/"):
		s.openExternalPath(link)
	default:
		s.pendingToast = "Could not open the report"
	}
}

// openBoardURL is the browser door, a seam for tests.
var openBoardURL = func(u string) error { return mygo.Shell.OpenExternal(u) }
