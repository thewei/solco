package personalagent

import "strconv"

// api_c.go: the stage-C commands — the event log and the heartbeat.

func (a *API) heartbeat(args []string, body []byte) (any, error) {
	if len(args) != 2 {
		return nil, coded("invalid", "usage: assistant heartbeat <show|set|request-wake> <assistantId>")
	}
	cmd, id := args[0], args[1]
	switch cmd {
	case "show":
		h, err := a.Store.GetHeartbeat(id)
		return ok("heartbeat", h), err
	case "set":
		var in struct {
			common
			HeartbeatInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		in.AssistantID = id
		h, replayed, err := a.Store.SetHeartbeat(in.RequestID, in.HeartbeatInput, in.ExpectedRevision)
		return ok("heartbeat", h, "replayed", replayed), err
	case "request-wake":
		var in common
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		ap, replayed, err := a.Store.RequestHeartbeatApproval(in.RequestID, id)
		return ok("approval", ap, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown heartbeat command %q", cmd)
}

func (a *API) events(args []string, _ []byte) (any, error) {
	if len(args) < 2 || args[0] != "list" {
		return nil, coded("invalid", "usage: assistant event list <assistantId> [--after <seq>] [--limit <n>]")
	}
	id := args[1]
	rest := args[2:]
	afterS, rest := flagValue(rest, "--after")
	limitS, _ := flagValue(rest, "--limit")
	after, _ := strconv.ParseInt(afterS, 10, 64)
	limit, _ := strconv.Atoi(limitS)
	evs, err := a.Store.Events(id, after, limit)
	return ok("events", evs), err
}
