package personalagent

import "fmt"

// assistant grant create|list|show|revoke|decide (Gap A04). Every one is the
// owner's or the system's: none is in the Coordinator scope, so `--as` answers
// forbidden. `create` stores what the owner's client signed (the seal is
// checked when a grant is used, not here); `decide` is the engine's entry for
// the code that is about to perform a protected action.
func (a *API) grant(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant grant <create|list|show|revoke|decide>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "create":
		var in struct {
			common
			GrantInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		g, replayed, err := a.Store.CreateGrant(in.RequestID, in.GrantInput)
		if err != nil {
			return nil, err
		}
		return ok("grant", g, "replayed", replayed), nil
	case "list":
		who, _ := flagValue(rest, "--assistant")
		list, err := a.Store.ListGrants(who)
		if err != nil {
			return nil, err
		}
		return ok("grants", list, "capabilities", Capabilities()), nil
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant grant show <id>")
		}
		g, err := a.Store.GetGrant(rest[0])
		if err != nil {
			return nil, err
		}
		return ok("grant", g), nil
	case "revoke":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant grant revoke <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		g, replayed, err := a.Store.RevokeGrant(in.RequestID, rest[0])
		if err != nil {
			return nil, err
		}
		return ok("grant", g, "replayed", replayed), nil
	case "decide":
		var in AuthorizeInput
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		r, replayed, err := a.Store.Authorize(in)
		if err != nil {
			return nil, err
		}
		return ok("result", r, "replayed", replayed), nil
	}
	return nil, coded("invalid", "unknown grant command %q", cmd)
}

// assistant owner status|register|rotate — the owner's public key (A04). Like
// grants, none of it is in the Coordinator scope.
func (a *API) owner(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant owner <status|register|rotate>")
	}
	path := OwnerKeyPath()
	var in struct {
		common
		PublicKey string `json:"publicKey"`
		Seal      string `json:"seal"`
	}
	switch args[0] {
	case "status":
		st, err := OwnerKeyStatus(path)
		if err != nil {
			return nil, err
		}
		return ok("owner", st), nil
	case "register":
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		st, err := RegisterOwnerKey(path, in.PublicKey)
		if err != nil {
			return nil, err
		}
		return ok("owner", st), nil
	case "rotate":
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		st, err := RotateOwnerKey(path, in.PublicKey, in.Seal)
		if err != nil {
			return nil, err
		}
		return ok("owner", st), nil
	}
	return nil, coded("invalid", "unknown owner command %q", args[0])
}

// assistant budget show <assistantId> [--limit N] — what the system started on
// its own today and why it held back (A05). Read-only, owner/system surface.
func (a *API) budget(args []string) (any, error) {
	if len(args) < 2 || args[0] != "show" {
		return nil, coded("invalid", "usage: assistant budget show <assistantId> [--limit N]")
	}
	limit := 20
	if v, _ := flagValue(args[2:], "--limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	sum, err := a.Store.BudgetSummary(args[1], limit)
	if err != nil {
		return nil, err
	}
	return ok("budget", sum), nil
}
