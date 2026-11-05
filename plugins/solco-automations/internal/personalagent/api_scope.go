package personalagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// C02/C08: the Coordinator's identity is decided here, not in its extension.
// `assistant --as <assistantId> <command> …` is the only entry the extension
// uses. The core checks that the caller is a real Assistant, that the command
// is one of the actions a Coordinator is allowed, and that every record the
// call names belongs to that Assistant. Whatever the model writes in a payload
// can neither widen the scope nor pick another owner. This is an authorisation
// check on the product's own commands, not an OS sandbox: a local process that
// runs the bare CLI is still the same user.

type scopeRule struct {
	// target is the record kind the first positional names ("task", "goal",
	// "routine"); the core refuses it unless the caller owns it.
	target string
	// own says how the caller's own id travels: "body" (assistantId in the
	// JSON), "flag" (--assistant <id>), "pos" (first positional).
	own string
	// fillRev means a call with no expectedRevision reads the record's
	// current one (the model cannot know it).
	fillRev bool
}

// coordinatorScope is the complete list of what a Coordinator may call.
// Everything else — approving, starting or completing goals, enabling
// routines, publishing coding work, its own lifecycle, memory approval — is a
// person's and answers `forbidden`.
var coordinatorScope = map[string]scopeRule{
	"task create":    {own: "body"},
	"task list":      {own: "flag"},
	"task graph":     {own: "pos"},
	"task show":      {target: "task"},
	"task why":       {target: "task"},
	"task proof":     {target: "task"},
	"trace show":     {target: "any"},
	"task retry":     {target: "task", fillRev: true},
	"goal create":    {own: "body"},
	"goal list":      {own: "flag"},
	"goal show":      {target: "goal"},
	"goal plan":      {target: "goal"},
	"goal set-plan":  {target: "goal"},
	"goal pause":     {target: "goal", fillRev: true},
	"goal resume":    {target: "goal", fillRev: true},
	"routine draft":  {own: "body"},
	"routine list":   {own: "flag"},
	"routine show":   {target: "routine"},
	"trigger draft":  {own: "body"},
	"trigger list":   {own: "flag"},
	"memory propose": {own: "body"},
	"memory list":    {own: "pos"},
	"checkpoint get": {own: "pos"},
	"checkpoint set": {own: "body"},
	"recovery show":  {own: "pos"},
}

// CoordinatorActions lists the allowed commands ("task show", …) for tests that
// keep the extension and the core in step.
func CoordinatorActions() []string {
	out := make([]string, 0, len(coordinatorScope))
	for k := range coordinatorScope {
		out = append(out, k)
	}
	return out
}

func forbidden(format string, a ...any) error { return coded("forbidden", format, a...) }

// scoped runs one command as the Coordinator of `who`.
func (a *API) scoped(who string, args []string, body []byte) (any, error) {
	if who == "" {
		return nil, forbidden("--as needs an Assistant id")
	}
	if _, err := a.Store.GetAssistant(who); err != nil {
		return nil, forbidden("this is not an Assistant of Solco")
	}
	// args = assistant <group> <sub> [positional…]
	if len(args) < 3 || args[0] != "assistant" {
		return nil, forbidden("this action is not available to a Coordinator")
	}
	group, sub, rest := args[1], args[2], args[3:]
	rule, allowed := coordinatorScope[group+" "+sub]
	if !allowed {
		return nil, forbidden("%s %s is a person's action, not a Coordinator's", group, sub)
	}
	switch rule.own {
	case "flag":
		id, _ := flagValue(rest, "--assistant")
		if id != who {
			return nil, forbidden("this list is limited to your own Assistant")
		}
	case "pos":
		if len(rest) < 1 || rest[0] != who {
			return nil, forbidden("this record belongs to another Assistant")
		}
	case "body":
		var err error
		if body, err = pinOwner(body, who); err != nil {
			return nil, err
		}
		if group == "task" && sub == "create" {
			if body, err = a.guardDuplicateTask(body); err != nil {
				return nil, err
			}
		}
	}
	if rule.target != "" {
		if len(rest) < 1 {
			return nil, coded("invalid", "a record id is required")
		}
		owner, rev, err := a.ownerOf(rule.target, rest[0])
		if err != nil || owner != who {
			// One answer for "not yours" and "not there": ids of other
			// Assistants are not probeable.
			return nil, forbidden("this record is not owned by the current Assistant")
		}
		if rule.fillRev {
			if body, err = fillRevision(body, rev); err != nil {
				return nil, err
			}
		}
	}
	return a.handle(args, body)
}

// ownerOf names the Assistant that owns a record and its current revision.
func (a *API) ownerOf(kind, id string) (string, int, error) {
	switch kind {
	case "task":
		t, err := a.Store.GetTask(id)
		return t.AssistantID, t.Revision, err
	case "goal":
		g, err := a.Store.GetGoal(id)
		return g.AssistantID, g.Revision, err
	case "routine":
		r, err := a.Store.GetRoutine(id)
		return r.AssistantID, r.Revision, err
	case "any": // a task id, else a goal id
		if t, err := a.Store.GetTask(id); err == nil {
			return t.AssistantID, t.Revision, nil
		}
		g, err := a.Store.GetGoal(id)
		return g.AssistantID, g.Revision, err
	}
	return "", 0, ErrNotFound
}

func objectOf(body []byte) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if s := strings.TrimSpace(string(body)); s != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, coded("invalid", "body is not a JSON object: %v", err)
		}
	}
	return m, nil
}

// pinOwner makes the body's assistantId the caller's: absent is filled in,
// anything else is refused rather than rewritten.
func pinOwner(body []byte, who string) ([]byte, error) {
	m, err := objectOf(body)
	if err != nil {
		return nil, err
	}
	if raw, ok := m["assistantId"]; ok {
		var got string
		if json.Unmarshal(raw, &got) != nil || got != who {
			return nil, forbidden("a Coordinator can only act for its own Assistant")
		}
	}
	m["assistantId"], _ = json.Marshal(who)
	return json.Marshal(m)
}

func fillRevision(body []byte, rev int) ([]byte, error) {
	m, err := objectOf(body)
	if err != nil {
		return nil, err
	}
	var cur int
	if raw, ok := m["expectedRevision"]; ok {
		_ = json.Unmarshal(raw, &cur)
	}
	if cur == 0 {
		m["expectedRevision"] = json.RawMessage(fmt.Sprint(rev))
	}
	return json.Marshal(m)
}

// guardDuplicateTask refuses a new task that restates an unfinished one under
// a fresh requestId (a model forgetting what it already filed), unless the
// caller says so with allowDuplicate. A repeat of a requestId that already has
// a receipt is a replay and passes through untouched.
func (a *API) guardDuplicateTask(body []byte) ([]byte, error) {
	m, err := objectOf(body)
	if err != nil {
		return nil, err
	}
	allow := false
	if raw, ok := m["allowDuplicate"]; ok {
		_ = json.Unmarshal(raw, &allow)
		delete(m, "allowDuplicate")
	}
	str := func(k string) string {
		var v string
		if raw, ok := m[k]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		return v
	}
	if !allow {
		var seen int
		if req := str("requestId"); req != "" {
			_ = a.Store.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE request_id=?`, req).Scan(&seen)
		}
		if seen == 0 {
			if dup, found := a.Store.openDuplicate(str("assistantId"), str("goalId"), str("title")); found {
				return nil, coded("duplicate_task", "an unfinished task already covers this: %s %q (%s). Look at it with task.why or task.show instead, or set allowDuplicate to true if you really need another.", dup.ID, clip(dup.Title, 60), dup.State)
			}
		}
	}
	return json.Marshal(m)
}
