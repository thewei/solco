package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 Client 写动词（Approve/SendBack/Retry/Fix/Delegate/Stop/Archive/ApprovePlan/DelegateNow/Tell）、workboardGo/newWorkboardClient 缝、Shell 的 openConfirm/openTextDialog/pendingToast、git.root
 * [OUTPUT]: workboardActionsFor（按分组给出可用写操作）、runWorkboardAction（确认后的唯一执行入口）、workboardActionsForTask（plan-ready: Approve Plan…/Send Plan Back…；pick: Pick…（见 workboard_pick.go）而非恢复动词；after 阶段: Start Now…）、submitWorkboardPlanBack；New Task 见 workboard_newtask.go
 * [POS]: Tasks 页签的写入面；任务状态权威仍归插件，这里只把用户意图转成插件 CLI 动词、把插件的拒绝原文变成 toast、成功后强制刷新。Approve 必经 shared confirm，且在执行时由 Client 重读 list+details 复核仍是 Ready；Stop 与 Start Now 同样先 confirm；Approve Plan 同样先 confirm 且重读须仍在 plan 阶段（二者从不带 --anyway）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

const (
	workboardApproveConfirm     = "workboard-approve"
	workboardApprovePlanConfirm = "workboard-approve-plan"
	workboardPlanBackDialog     = "workboard-plan-back"
	workboardStartNowConfirm    = "workboard-start-now"
	workboardStopConfirm        = "workboard-stop"
	workboardToastMax           = 160
	workboardNoRepoToast        = "Open a Project inside a Git repository to add a task."
	workboardNotReadyToast      = "That task changed and is no longer ready to approve."
	workboardUnknownFailureText = "The work board refused it."
)

// boardWrite is one write the Tasks tab offers on a row.
type boardWrite struct {
	Label string
	Kind  string // approve | approve-plan | plan-back | start-now | review | retry | fix | delegate | stop | archive
}

// workboardActionsFor lists what a row in a group may do. With the task's
// details in hand, the two "send back" verbs are offered only when there is a
// review or a failure to send; without them every verb is offered. The plugin
// remains the judge of what applies and refuses the rest with its own message.
func workboardActionsFor(g workboard.Group, d *workboard.Detail) []boardWrite {
	var out []boardWrite
	switch g {
	case workboard.GroupReady:
		out = append(out, boardWrite{"Approve…", "approve"})
	case workboard.GroupNeedsYou:
		out = append(out, boardWrite{"Retry", "retry"})
		if d == nil || d.ReviewFailed || d.ReviewText != "" {
			out = append(out, boardWrite{"Send review back", "review"})
		}
		if d == nil || d.Failure {
			out = append(out, boardWrite{"Send failure details", "fix"})
		}
	case workboard.GroupTodo:
		out = append(out, boardWrite{"Delegate", "delegate"}, boardWrite{"Delegate with…", "delegate-with"})
	}
	// A task whose agent still runs cannot be archived (the plugin says so):
	// Stop is the way out. To do and Done have no running agent.
	if g != workboard.GroupTodo && g != workboard.GroupDone {
		out = append(out, boardWrite{"Stop", "stop"})
	}
	if g != workboard.GroupWorking {
		out = append(out, boardWrite{"Archive", "archive"})
	}
	return out
}

// workboardActionsForTask is workboardActionsFor with the two stages whose
// verbs differ from their group's: a written plan waits for a yes or a reply
// (not a retry), and several passed attempts wait for a pick the list rows
// cannot describe, so no recovery verb is offered for them.
func workboardActionsForTask(task workboard.Task, g workboard.Group, d *workboard.Detail) []boardWrite {
	switch {
	case task.PlanReady():
		return []boardWrite{
			{"Approve Plan…", "approve-plan"}, {"Send Plan Back…", "plan-back"},
			{"Stop", "stop"}, {"Archive", "archive"},
		}
	case task.NeedsPick():
		return []boardWrite{{"Pick…", "pick"}, {"Stop", "stop"}, {"Archive", "archive"}}
	case task.Stage == "after":
		// Queued behind other tasks: nothing runs yet, so there is no agent to
		// stop or retry; it can be started anyway.
		return []boardWrite{{"Start Now…", "start-now"}, {"Archive", "archive"}}
	}
	return workboardActionsFor(g, d)
}

// isPrimaryWrite marks the one verb that moves the task forward.
func isPrimaryWrite(kind string) bool { return kind == "approve" || kind == "approve-plan" || kind == "pick" }

// startWorkboardAction is a row menu choice: Approve, Approve Plan, Start Now
// and Stop ask first, everything else runs at once.
func (s *Shell) startWorkboardAction(a boardWrite, task workboard.Task) {
	if a.Kind == "pick" {
		s.openWorkboardPick(task)
		return
	}
	if a.Kind == "delegate-with" {
		s.openWorkboardDelegateWith(task)
		return
	}
	if a.Kind == "approve" {
		title := task.Title
		if title == "" {
			title = task.ID
		}
		s.openConfirm(workboardApproveConfirm, task.ID, "Approve and merge?",
			"“"+title+"” ("+task.ID+") will be merged. Its review and pull request checks are re-checked first.")
		return
	}
	if a.Kind == "approve-plan" {
		title := task.Title
		if title == "" {
			title = task.ID
		}
		s.openConfirm(workboardApprovePlanConfirm, task.ID, "Approve the plan?",
			"The agent will build “"+title+"” ("+task.ID+") from its plan. Nothing in the repo changed before this.")
		return
	}
	if a.Kind == "start-now" {
		s.openConfirm(workboardStartNowConfirm, task.ID, "Start without waiting?",
			task.ID+" is queued after other tasks. Starting it now puts an agent on it before they are done.")
		return
	}
	if a.Kind == "stop" {
		title := task.Title
		if title == "" {
			title = task.ID
		}
		s.openConfirm(workboardStopConfirm, task.ID, "Stop this task?",
			"The agent on “"+title+"” ("+task.ID+") stops. The task goes back to To do.")
		return
	}
	if a.Kind == "plan-back" {
		s.hideQuickPanel()
		s.openTextDialog(workboardPlanBackDialog, task.ID, "Send Plan Back", "What should change?", "")
		s.dialogAction = "Send"
		return
	}
	s.runWorkboardAction(a.Kind, task.ID)
}

// submitWorkboardPlanBack sends the plan back to its writer with the user's words.
func (s *Shell) submitWorkboardPlanBack(id, text string) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "plan-back", "task", id)
	s.workboardWrite("plan-back", func(ctx context.Context) error {
		return client.Tell(ctx, id, text)
	}, func() { s.pendingToast = "Sent the plan of " + id + " back" })
}

// runWorkboardAction runs one write off the UI lane, toasts the outcome and
// forces a board refresh.
func (s *Shell) runWorkboardAction(kind, id string) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", kind, "task", id)
	s.workboardWrite(kind, func(ctx context.Context) error {
		switch kind {
		case "approve":
			return client.Approve(ctx, id)
		case "approve-plan":
			return client.ApprovePlan(ctx, id)
		case "start-now":
			return client.DelegateNow(ctx, id)
		case "review":
			return client.SendBack(ctx, id)
		case "retry":
			return client.Retry(ctx, id)
		case "fix":
			return client.Fix(ctx, id)
		case "delegate":
			return client.Delegate(ctx, id)
		case "stop":
			return client.Stop(ctx, id)
		case "archive":
			return client.Archive(ctx, id)
		}
		return errors.New("unknown work board action")
	}, func() {
		// An archived task has left the board: leave its page too.
		if kind == "archive" && s.router.Path() == routeTask && s.workboard.page.id == id {
			s.router.Back()
		}
	})
}

// setWorkboardPin stars or unstars a task (by id) or a Project (by name): the
// plugin's own ★, so its board shows the same mark.
func (s *Shell) setWorkboardPin(target string, pinned bool) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	op := "unpin"
	if pinned {
		op = "pin"
	}
	slog.Info("work board write requested", "op", op, "target", target)
	s.workboardWrite(op, func(ctx context.Context) error {
		if pinned {
			return client.Pin(ctx, target)
		}
		return client.Unpin(ctx, target)
	}, nil)
}

// workboardWrite is the single execution channel for board writes.
func (s *Shell) workboardWrite(op string, run func(ctx context.Context) error, onOK func()) {
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		err := run(ctx)
		s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("work board write failed", "op", op, "error", err)
				s.pendingToast = workboardFailureToast(err)
			} else if onOK != nil {
				onOK()
			}
			s.refreshWorkboardTasks(true)
			if s.workboard.showArchived {
				s.loadWorkboardArchived()
			}
			if s.router.Path() == routeTask {
				s.loadWorkboardPage()
			}
		})
	})
}

// workboardFailureToast shows the plugin's own refusal, bounded.
func workboardFailureToast(err error) string {
	if errors.Is(err, workboard.ErrNotReady) {
		return workboardNotReadyToast
	}
	text := err.Error()
	// The CLI wrapper reads "workboard: <verb>: <exit>: <plugin message>".
	if parts := strings.SplitN(text, ": ", 4); len(parts) == 4 && parts[0] == "workboard" {
		text = parts[3]
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return workboardUnknownFailureText
	}
	if r := []rune(text); len(r) > workboardToastMax {
		text = string(r[:workboardToastMax]) + "…"
	}
	return text
}
