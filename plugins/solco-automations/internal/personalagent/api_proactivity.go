package personalagent

// proactivity handles `assistant proactivity <show|set> <assistant-id>`: the
// rules for what the system may start on its own for one Assistant.
func (a *API) proactivity(args []string, body []byte) (any, error) {
	if len(args) != 2 {
		return nil, coded("invalid", "usage: assistant proactivity <show|set> <assistant-id>")
	}
	switch args[0] {
	case "show":
		p, err := a.Store.GetProactivity(args[1])
		return ok("proactivity", p), err
	case "set":
		var in struct {
			common
			ProactivityInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		in.AssistantID = args[1]
		p, replayed, err := a.Store.SetProactivity(in.RequestID, in.ProactivityInput, in.ExpectedRevision)
		return ok("proactivity", p, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown proactivity command %q", args[0])
}
