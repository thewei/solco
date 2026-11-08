package personalagent

import "time"

func (a *API) goal(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant goal <create|list|show|plan|set-plan|facts|approve|pause|resume|cancel|complete|progress>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "create":
		var in struct {
			common
			CreateGoalInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		g, replayed, err := a.Store.CreateGoal(in.RequestID, in.CreateGoalInput)
		return ok("goal", g, "replayed", replayed), err
	case "list":
		who, _ := flagValue(rest, "--assistant")
		list, err := a.Store.ListGoals(who)
		return ok("goals", list), err
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant goal show <id>")
		}
		g, err := a.Store.GetGoal(rest[0])
		return ok("goal", g), err
	case "plan":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant goal plan <id>")
		}
		p, err := a.Store.GetGoalPlan(rest[0])
		return ok("plan", p), err
	case "set-plan":
		var in struct {
			common
			GoalPlanInput
			Reason string `json:"reason"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant goal set-plan <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		p, replayed, err := a.Store.SetGoalPlan(in.RequestID, rest[0], in.GoalPlanInput, in.Reason, in.ExpectedRevision)
		return ok("plan", p, "replayed", replayed), err
	case "facts":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant goal facts <id>")
		}
		fs, err := a.Store.Facts("", rest[0], 100)
		return ok("facts", fs), err
	case "progress":
		var in struct {
			common
			Note string `json:"note"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant goal progress <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		e, replayed, err := a.Store.AddProgress(in.RequestID, rest[0], in.Note)
		return ok("event", e, "replayed", replayed), err
	case "approve", "pause", "resume", "cancel", "complete":
		var in struct {
			common
			Evidence string `json:"evidence"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant goal %s <id>", cmd)
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		var g Goal
		var replayed bool
		var err error
		switch cmd {
		case "approve":
			g, replayed, err = a.Store.ApproveGoal(in.RequestID, rest[0], in.ExpectedRevision)
		case "pause":
			g, replayed, err = a.Store.SetGoalState(in.RequestID, rest[0], GoalPaused, in.ExpectedRevision)
		case "resume":
			g, replayed, err = a.Store.SetGoalState(in.RequestID, rest[0], GoalActive, in.ExpectedRevision)
		case "cancel":
			g, replayed, err = a.Store.SetGoalState(in.RequestID, rest[0], GoalCancelled, in.ExpectedRevision)
		case "complete":
			g, replayed, err = a.Store.CompleteGoal(in.RequestID, rest[0], in.ExpectedRevision, in.Evidence)
		}
		return ok("goal", g, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown goal command %q", cmd)
}

// memory serves the two-file memory (notes.go): `read <id>`, `write <id>` (the
// Assistant's file) and `write-user <id>` (the person's), both fenced by the
// rev that was read, `add <id>` (one line, no approval) and `folder <id>`.
func (a *API) memory(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant memory <read|write|write-user|add|folder> <assistantId>")
	}
	cmd, rest := args[0], args[1:]
	if len(rest) != 1 {
		return nil, coded("invalid", "usage: assistant memory %s <assistantId>", cmd)
	}
	id := rest[0]
	switch cmd {
	case "read":
		n, err := a.Store.ReadNotes(id)
		return ok("notes", n), err
	case "folder":
		dir, err := a.Store.NotesFolder(id)
		return ok("path", dir), err
	case "add":
		var in struct {
			Note string `json:"note"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		ack, err := a.Store.AddAssistantNote(id, in.Note)
		return ok("note", ack), err
	case "write", "write-user":
		var in struct {
			Text        string `json:"text"`
			ExpectedRev string `json:"expectedRev"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		which := NotesAssistant
		if cmd == "write-user" {
			which = NotesUser
		}
		n, err := a.Store.WriteNotes(id, which, in.Text, in.ExpectedRev)
		return ok("notes", n), err
	}
	return nil, coded("invalid", "unknown memory command %q", cmd)
}

func (a *API) approval(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant approval <request|list|show|decide>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "request":
		var in struct {
			common
			RequestApprovalInput
			TTLSeconds int `json:"ttlSeconds"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		in.TTL = time.Duration(in.TTLSeconds) * time.Second
		x, replayed, err := a.Store.RequestApproval(in.RequestID, in.RequestApprovalInput)
		return ok("approval", x, "replayed", replayed), err
	case "list":
		who, _ := flagValue(rest, "--assistant")
		list, err := a.Store.ListApprovals(who)
		return ok("approvals", list), err
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant approval show <id>")
		}
		x, err := a.Store.GetApproval(rest[0])
		return ok("approval", x), err
	case "decide":
		var in struct {
			common
			ActionHash string `json:"actionHash"`
			Approve    bool   `json:"approve"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant approval decide <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.DecideApproval(in.RequestID, rest[0], in.ActionHash, in.Approve)
		if err == nil {
			a.afterApproval(x, in.Approve)
		}
		return ok("approval", x, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown approval command %q", cmd)
}
