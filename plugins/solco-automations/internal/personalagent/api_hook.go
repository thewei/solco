package personalagent

import "fmt"

// assistant hook key create|list|revoke / assistant hook ingest. Keys are the
// person's (not in the Coordinator scope); `ingest` is the Host's call and
// proves itself with the delivery's own signature, never with the caller.
func (a *API) hook(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant hook <key|ingest>")
	}
	switch args[0] {
	case "ingest":
		var d HookDelivery
		if err := decode(body, &d); err != nil {
			return nil, err
		}
		r, err := a.Store.IngestHook(d)
		if err != nil {
			return nil, err
		}
		return ok("receipt", r), nil
	case "events":
		if len(args) < 2 {
			return nil, coded("invalid", "usage: assistant hook events <assistantId> [--limit N]")
		}
		limit := 20
		if v, _ := flagValue(args[2:], "--limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		list, err := a.Store.ListHookRecords(args[1], limit)
		if err != nil {
			return nil, err
		}
		return ok("events", list), nil
	case "key":
		if len(args) < 2 {
			return nil, coded("invalid", "usage: assistant hook key <create|list|revoke>")
		}
		switch args[1] {
		case "create":
			var in struct {
				common
				AssistantID string   `json:"assistantId"`
				Sources     []string `json:"sources"`
			}
			if err := decode(body, &in); err != nil {
				return nil, err
			}
			k, secret, replayed, err := a.Store.CreateHookKey(in.RequestID, in.AssistantID, in.Sources)
			if err != nil {
				return nil, err
			}
			if replayed {
				secret = "" // shown once; a replay never repeats it
			}
			return ok("key", k, "secret", secret, "path", HookPath(k.AssistantID), "replayed", replayed), nil
		case "list":
			who, _ := flagValue(args[2:], "--assistant")
			keys, err := a.Store.ListHookKeys(who)
			if err != nil {
				return nil, err
			}
			return ok("keys", keys), nil
		case "revoke":
			var in common
			if len(args) != 3 {
				return nil, coded("invalid", "usage: assistant hook key revoke <keyId>")
			}
			if err := decode(body, &in); err != nil {
				return nil, err
			}
			k, replayed, err := a.Store.RevokeHookKey(in.RequestID, args[2])
			if err != nil {
				return nil, err
			}
			return ok("key", k, "replayed", replayed), nil
		}
	}
	return nil, coded("invalid", "unknown hook command")
}
