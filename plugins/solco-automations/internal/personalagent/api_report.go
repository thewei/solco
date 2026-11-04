// [INPUT]: Coordinator report status and an explicit human decision for uncertain Herdr delivery
// [OUTPUT]: read-only status, or a request-ID-fenced human-verified skip/retry
// [POS]: CLI recovery path. Daemon never auto-retries an uncertain submitted prompt.
// [PROTOCOL]: Update this header on changes, then check CLAUDE.md
package personalagent

func (a *API) report(args []string, body []byte) (any, error) {
	// No Assistant identity is required for capability probing. Older plugins
	// reject the unknown subcommand, so Hosts fail closed instead of exposing
	// report routes they cannot actually serve.
	if len(args) == 1 && args[0] == "probe" {
		return ok("supported", true), nil
	}
	if len(args) != 2 {
		return nil, coded("invalid", "usage: assistant report <show|resolve> <assistantId>")
	}
	action, id := args[0], args[1]
	switch action {
	case "show":
		r, err := a.Store.GetCoordinatorReport(id)
		return ok("report", r), err
	case "resolve":
		var in struct {
			RequestID  string `json:"requestId"`
			ThroughSeq int64  `json:"throughSeq"`
			Action     string `json:"action"`
			Confirmed  bool   `json:"confirmed"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "requestId is required")
		}
		r, replayed, err := a.Store.ResolveCoordinatorReport(in.RequestID, id, in.ThroughSeq, in.Action, in.Confirmed)
		return ok("report", r, "replayed", replayed), err
	default:
		return nil, coded("invalid", "unknown report operation")
	}
}
