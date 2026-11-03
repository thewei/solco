package personalagent

import "time"

func (a *API) goal(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant goal <create|list|show|approve|pause|resume|cancel|complete>")
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

func (a *API) memory(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant memory <propose|list|visible|approve|correct|forget|share|unshare>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "propose":
		var in struct {
			common
			ProposeMemoryInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		m, replayed, err := a.Store.ProposeMemory(in.RequestID, in.ProposeMemoryInput)
		return ok("memory", m, "replayed", replayed), err
	case "list", "visible":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant memory %s <assistantId>", cmd)
		}
		var list []MemoryItem
		var err error
		if cmd == "list" {
			list, err = a.Store.ListMemory(rest[0])
		} else {
			list, err = a.Store.VisibleMemory(rest[0])
		}
		return ok("memory", list), err
	case "approve", "correct", "forget", "share", "unshare":
		var in struct {
			common
			Content     string `json:"content"`
			AssistantID string `json:"assistantId"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant memory %s <id>", cmd)
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		var m MemoryItem
		var replayed bool
		var err error
		switch cmd {
		case "approve":
			m, replayed, err = a.Store.ApproveMemory(in.RequestID, rest[0], in.ExpectedRevision)
		case "correct":
			m, replayed, err = a.Store.CorrectMemory(in.RequestID, rest[0], in.Content, in.ExpectedRevision)
		case "forget":
			m, replayed, err = a.Store.ForgetMemory(in.RequestID, rest[0], in.ExpectedRevision)
		case "share":
			m, replayed, err = a.Store.ShareMemory(in.RequestID, rest[0], in.AssistantID, in.ExpectedRevision)
		case "unshare":
			m, replayed, err = a.Store.UnshareMemory(in.RequestID, rest[0], in.AssistantID, in.ExpectedRevision)
		}
		return ok("memory", m, "replayed", replayed), err
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
		return ok("approval", x, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown approval command %q", cmd)
}
