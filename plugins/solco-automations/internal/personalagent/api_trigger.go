package personalagent

// assistant trigger draft|list|show|cancel|pause|resume (Gap R03). The model
// may only draft and list; approving is a person's card, and pause/resume/
// cancel are the person's too (they are not in the Coordinator scope).
func (a *API) trigger(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant trigger <draft|list|show|cancel|pause|resume>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "draft":
		var in struct {
			common
			TriggerDraftInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		t, ap, replayed, err := a.Store.DraftTrigger(in.RequestID, in.TriggerDraftInput)
		if err != nil {
			return nil, err
		}
		return ok("trigger", t, "approval", ap, "replayed", replayed), nil
	case "list":
		who, _ := flagValue(rest, "--assistant")
		list, err := a.Store.ListTriggers(who)
		if err != nil {
			return nil, err
		}
		return ok("triggers", list, "kinds", TriggerKinds()), nil
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant trigger show <id>")
		}
		t, err := a.Store.GetTrigger(rest[0])
		if err != nil {
			return nil, err
		}
		return ok("trigger", t), nil
	case "cancel", "pause", "resume":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant trigger %s <id>", cmd)
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		to := map[string]string{"cancel": TriggerWithdrawn, "pause": TriggerPaused, "resume": TriggerActive}[cmd]
		t, replayed, err := a.Store.SetTriggerState(in.RequestID, rest[0], to, in.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		return ok("trigger", t, "replayed", replayed), nil
	}
	return nil, coded("invalid", "unknown trigger command %q", cmd)
}
