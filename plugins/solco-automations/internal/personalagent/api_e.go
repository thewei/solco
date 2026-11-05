package personalagent

// api_e.go: stage E's commands — review/merge and the approved check list.
//
//	assistant review start <taskId>          evidence + approved checks
//	assistant review show <taskId>           the task's newest review
//	assistant review get <reviewId>
//	assistant review agent <reviewId>        optional second-Agent review task
//	assistant review request-merge <reviewId>  an approval card, never a merge
//	assistant review merge <reviewId>        merges only an approved, still-fresh review
//	assistant review resume <reviewId>       {requestId, feedback}: back to the same worktree as a new attempt
//	assistant review reject <reviewId>       {requestId, reason}: close the task, merge nothing, keep the work
//	assistant checks show|set <assistantId>  check commands run only once approved

func (a *API) review(args []string, body []byte) (any, error) {
	if a.Reviewer == nil {
		return nil, coded("unavailable", "review is not available here")
	}
	if len(args) != 2 {
		return nil, coded("invalid", "usage: assistant review <start|show|get|agent|request-merge|merge|resume|reject> <id>")
	}
	cmd, id := args[0], args[1]
	var in struct {
		common
		Feedback string `json:"feedback"`
		Reason   string `json:"reason"`
	}
	if cmd == "start" || cmd == "agent" || cmd == "request-merge" || cmd == "resume" || cmd == "reject" {
		if err := decode(body, &in); err != nil {
			return nil, err
		}
	}
	switch cmd {
	case "start":
		rv, replayed, err := a.Reviewer.Start(in.RequestID, id)
		return ok("review", rv, "replayed", replayed), err
	case "show":
		rv, found, err := a.Reviewer.ForTask(id)
		if err == nil && !found {
			err = ErrNotFound
		}
		return ok("review", rv), err
	case "get":
		rv, err := a.Reviewer.Get(id)
		return ok("review", rv), err
	case "agent":
		rv, child, replayed, err := a.Reviewer.RequestAgentReview(in.RequestID, id)
		return ok("review", rv, "task", child, "replayed", replayed), err
	case "request-merge":
		rv, ap, replayed, err := a.Reviewer.RequestMerge(in.RequestID, id)
		return ok("review", rv, "approval", ap, "replayed", replayed), err
	case "merge":
		rv, err := a.Reviewer.Merge(id)
		return ok("review", rv), err
	case "resume":
		rv, replayed, err := a.Reviewer.Resume(in.RequestID, id, in.Feedback)
		return ok("review", rv, "replayed", replayed), err
	case "reject":
		rv, err := a.Reviewer.Reject(in.RequestID, id, in.Reason)
		return ok("review", rv), err
	}
	return nil, coded("invalid", "unknown review command %q", cmd)
}

func (a *API) checks(args []string, body []byte) (any, error) {
	if len(args) != 2 {
		return nil, coded("invalid", "usage: assistant checks <show|set> <assistantId>")
	}
	cmd, id := args[0], args[1]
	switch cmd {
	case "show":
		c, err := a.Store.Checks(id)
		return ok("checks", c), err
	case "set":
		var in struct {
			common
			Commands []string `json:"commands"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		ap, replayed, err := a.Store.RequestChecks(in.RequestID, id, in.Commands)
		return ok("approval", ap, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown checks command %q", cmd)
}
