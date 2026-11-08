package personalagent

import "context"

// persona handles `assistant persona …`: the Assistant's working style
// (instructions), its own Agent/Model/Effort (runtime) and its MCP grants.
func (a *API) persona(args []string, body []byte) (any, error) {
	if len(args) != 2 {
		return nil, coded("invalid", "usage: assistant persona <instructions|set-instructions|restore-instructions|runtime|set-runtime|mcp|mcp-grant|mcp-verify> <assistant-id>")
	}
	cmd, id := args[0], args[1]
	switch cmd {
	case "instructions":
		x, err := a.Store.GetInstructions(id)
		return ok("instructions", x), err
	case "set-instructions":
		var in struct {
			common
			Body string `json:"body"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.SetInstructions(in.RequestID, id, in.Body, in.ExpectedRevision)
		return ok("instructions", x, "replayed", replayed), err
	case "restore-instructions":
		var in struct {
			common
			Revision int `json:"revision"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.RestoreInstructions(in.RequestID, id, in.Revision, in.ExpectedRevision)
		return ok("instructions", x, "replayed", replayed), err
	case "runtime":
		x, err := a.Store.GetRuntime(id)
		return ok("runtime", x), err
	case "set-runtime":
		var in struct {
			common
			Agent  string `json:"agent"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
			// IdleSleepMin is optional: left out, the Assistant's earlier choice stays.
			IdleSleepMin *int `json:"idleSleepMin"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		var idle []int
		if in.IdleSleepMin != nil {
			idle = []int{*in.IdleSleepMin}
		}
		x, replayed, err := a.Store.SetRuntime(in.RequestID, id, in.Agent, in.Model, in.Effort, in.ExpectedRevision, idle...)
		return ok("runtime", x, "replayed", replayed), err
	case "mcp":
		x, err := a.Store.GetMCP(id)
		return ok("mcp", x), err
	case "mcp-grant":
		var in struct {
			common
			ServerID string `json:"serverId"`
			Granted  bool   `json:"granted"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.SetMCPGrant(in.RequestID, id, in.ServerID, in.Granted)
		return ok("mcp", x, "replayed", replayed), err
	case "mcp-verify":
		x, err := a.Store.VerifyMCP(context.Background(), id)
		return ok("mcp", x), err
	}
	return nil, coded("invalid", "unknown persona command %q", cmd)
}
