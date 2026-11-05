package personalagent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/schedule"
)

// api_routine.go: U4's commands — a Routine is a scheduled automation that
// belongs to an Assistant (routine.go).
//
//	assistant routine draft            propose one (always a draft + an approval card)
//	assistant routine list [--assistant <id>]
//	assistant routine show <name>
//	assistant routine cancel <name>    withdraw a draft or stale routine
//	assistant routine pause|resume <name>   an enabled routine (the automation's own pause flag)

var routineName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)

// RoutineDraftInput is the body of `assistant routine draft`.
type RoutineDraftInput struct {
	AssistantID    string `json:"assistantId"`
	Name           string `json:"name"`
	Cron           string `json:"cron"`
	Repo           string `json:"repo"`
	Agent          string `json:"agent"`
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Rationale      string `json:"rationale"`
	Headless       *bool  `json:"headless"`
	OnOverlap      string `json:"onOverlap"`
	TimeoutMinutes int    `json:"timeoutMinutes"`
	Source         string `json:"source"`
	RequestedBy    string `json:"requestedBy"`
	CorrelationID  string `json:"correlationId"`
}

// RoutineView is a routine with the definition it points at and how it is doing.
type RoutineView struct {
	Routine
	Cron          string `json:"cron"`
	Repo          string `json:"repo"`
	Agent         string `json:"agent"`
	Model         string `json:"model,omitempty"`
	Headless      bool   `json:"headless"`
	Paused        bool   `json:"paused"`
	Missing       bool   `json:"missing,omitempty"`
	Rationale     string `json:"rationale,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	Next          int64  `json:"next,omitempty"`
	LastState     string `json:"lastState,omitempty"`
	LastAt        int64  `json:"lastAt,omitempty"`
	ApprovalID    string `json:"approvalId,omitempty"`
	ApprovalState string `json:"approvalState,omitempty"`
}

func (a *API) cfgPut(original string, x config.Automation) (config.PutResult, error) {
	if a.ConfigPut != nil {
		return a.ConfigPut(original, x)
	}
	return config.Put(original, x, false)
}

func (a *API) cfgRemove(name string) error {
	if a.ConfigRemove != nil {
		return a.ConfigRemove(name)
	}
	return config.Remove(name)
}

func (a *API) cfgPause(name string, paused bool) error {
	if a.ConfigPause != nil {
		return a.ConfigPause(name, paused)
	}
	return config.SetPaused(name, paused)
}

func (a *API) keeper() *RoutineKeeper {
	return &RoutineKeeper{Store: a.Store, Load: a.loadConfig, Now: a.now, ApproveChecked: func(name, expectedHash string) error {
		if a.ConfigApprove != nil {
			// The injectable legacy callback is a testing seam. Normal Host
			// and CLI requests must conditionally approve the actual YAML.
			return a.ConfigApprove(name)
		}
		return ApproveRoutineDefinition(name, expectedHash)
	}}
}

func (a *API) routineView(r Routine, cfg *config.Config) RoutineView {
	v := RoutineView{Routine: r}
	if x := cfg.Find(r.Name); x != nil {
		v.Cron, v.Repo, v.Agent, v.Model, v.Headless, v.Paused = x.Cron, x.Repo, x.Agent, x.Model, x.Headless, x.Disabled
		v.Rationale = clip(x.Rationale, 600)
		v.Prompt = clip(x.Prompt, 600)
		if r.State == RoutineActive && !x.Disabled {
			if next, ok := schedule.NextRun(*x, a.now()); ok {
				v.Next = next.UnixMilli()
			}
		}
	} else {
		v.Missing = true
	}
	_ = a.Store.db.QueryRow(`SELECT state,updated_at FROM tasks WHERE rule_id=? ORDER BY created_at DESC LIMIT 1`, LegacyRuleID(r.Name)).Scan(&v.LastState, &v.LastAt)
	_ = a.Store.db.QueryRow(`SELECT id,state FROM approvals WHERE subject_kind='routine' AND subject_id=? AND expected_revision=? ORDER BY created_at DESC LIMIT 1`, r.Name, r.Revision).Scan(&v.ApprovalID, &v.ApprovalState)
	if v.ApprovalID != "" {
		if ap, err := a.Store.GetApproval(v.ApprovalID); err == nil {
			v.ApprovalState = ap.State
		}
	}
	return v
}

func (a *API) routine(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant routine <draft|list|show|cancel|pause|resume>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "draft":
		var in struct {
			common
			RoutineDraftInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		return a.routineDraft(in.RequestID, in.RoutineDraftInput)
	case "list":
		who, _ := flagValue(rest, "--assistant")
		list, err := a.Store.ListRoutines(who)
		if err != nil {
			return nil, err
		}
		cfg, err := a.loadConfig()
		if err != nil {
			return nil, coded("invalid", "automations.yaml: %v", err)
		}
		out := make([]RoutineView, 0, len(list))
		for _, r := range list {
			out = append(out, a.routineView(r, cfg))
		}
		return ok("routines", out), nil
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant routine show <name>")
		}
		r, err := a.Store.GetRoutine(rest[0])
		if err != nil {
			return nil, err
		}
		cfg, err := a.loadConfig()
		if err != nil {
			return nil, coded("invalid", "automations.yaml: %v", err)
		}
		return ok("routine", a.routineView(r, cfg)), nil
	case "cancel", "pause", "resume":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant routine %s <name>", cmd)
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		return a.routineChange(cmd, rest[0])
	}
	return nil, coded("invalid", "unknown routine command %q", cmd)
}

func (a *API) routineChange(cmd, name string) (any, error) {
	var out any
	err := withRoutineMutationLock(func() error {
		var err error
		out, err = a.routineChangeLocked(cmd, name)
		return err
	})
	return out, err
}

func (a *API) routineChangeLocked(cmd, name string) (any, error) {
	r, err := a.Store.GetRoutine(name)
	if err != nil {
		return nil, err
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, coded("invalid", "automations.yaml: %v", err)
	}
	switch cmd {
	case "cancel":
		if r.State != RoutineDraft && r.State != RoutineStale {
			return nil, coded("bad_state", "only a draft can be withdrawn; pause or delete an enabled routine from Automations")
		}
		if x := cfg.Find(name); x != nil && x.Draft {
			if err := a.cfgRemove(name); err != nil {
				return nil, coded("internal", "%v", err)
			}
		}
		if err := a.Store.setRoutineState(name, RoutineCancelled); err != nil {
			return nil, err
		}
	case "pause", "resume":
		if r.State != RoutineActive {
			return nil, coded("bad_state", "the routine is not enabled")
		}
		if cfg.Find(name) == nil {
			return nil, coded("not_found", "its automation is gone")
		}
		if err := a.cfgPause(name, cmd == "pause"); err != nil {
			return nil, coded("internal", "%v", err)
		}
	}
	r, err = a.Store.GetRoutine(name)
	if err != nil {
		return nil, err
	}
	cfg, _ = a.loadConfig()
	if cfg == nil {
		cfg = &config.Config{}
	}
	return ok("routine", a.routineView(r, cfg)), nil
}

func (a *API) routineDraft(requestID string, in RoutineDraftInput) (any, error) {
	var out any
	err := withRoutineMutationLock(func() error {
		var err error
		out, err = a.routineDraftLocked(requestID, in)
		return err
	})
	return out, err
}

func (a *API) routineDraftLocked(requestID string, in RoutineDraftInput) (any, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Repo = strings.TrimSpace(in.Repo)
	personalRoutine := in.Repo == ""
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.Rationale = strings.TrimSpace(in.Rationale)
	switch {
	case in.AssistantID == "":
		return nil, coded("invalid", "assistantId is required")
	case !routineName.MatchString(in.Name):
		return nil, coded("invalid", "name must be 2–48 characters: lowercase letters, digits and dashes")
	case in.Prompt == "" || len(in.Prompt) > 4000:
		return nil, coded("invalid", "prompt is required (at most 4000 characters)")
	case in.Rationale == "":
		return nil, coded("invalid", "rationale is required: say why this should run on a schedule")
	case !personalRoutine && !filepath.IsAbs(in.Repo):
		return nil, coded("invalid", "repo must be an absolute path")
	}
	if !personalRoutine {
		if st, err := os.Stat(in.Repo); err != nil || !st.IsDir() {
			return nil, coded("invalid", "repo %q is not a folder on this Mac", in.Repo)
		}
	}
	asst, err := a.Store.GetAssistant(in.AssistantID)
	if err != nil {
		return nil, err
	}
	if asst.Status != StatusActive {
		return nil, ErrAssistantDisabled
	}
	// Non-repository scheduled work belongs to the Assistant's private
	// controlled folder, not a made-up Git repository or an OS cron job.
	workspace := config.WorkspaceWorktree
	if personalRoutine {
		in.Repo = filepath.Join(WorkDirRoot(), "personal", "routines", asst.ID)
		if err := os.MkdirAll(in.Repo, 0o700); err != nil {
			return nil, coded("internal", "create Assistant routine directory: %v", err)
		}
		workspace = config.WorkspaceRoot
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, coded("invalid", "automations.yaml: %v", err)
	}
	original := ""
	if cur, err := a.Store.GetRoutine(in.Name); err == nil {
		switch {
		case cur.AssistantID != in.AssistantID:
			return nil, coded("invalid", "a routine named %q belongs to another assistant", in.Name)
		case cur.State == RoutineActive:
			return nil, coded("bad_state", "routine %q is already enabled; change it from Automations", in.Name)
		}
		if cfg.Find(in.Name) != nil {
			original = in.Name
		}
	} else if cfg.Declares(in.Name) {
		return nil, coded("invalid", "an automation named %q already exists", in.Name)
	}
	headless := false
	agent := in.Agent
	if agent == "" {
		agent = AssistantAgentKind
	}
	if agent != AssistantAgentKind {
		return nil, coded("invalid", "Assistant routines can only use %s during the OMP rollout", AssistantAgentKind)
	}
	if in.Headless != nil {
		headless = *in.Headless
	} else {
		// Unattended work should not be able to stall on a startup dialog.
		headless = config.KindRunsHeadless(agent)
	}
	x := config.Automation{
		Name: in.Name, Cron: strings.TrimSpace(in.Cron), Repo: in.Repo, Workspace: workspace, Agent: agent, Model: in.Model, Prompt: in.Prompt,
		Headless: headless, Draft: true, Rationale: in.Rationale, OnOverlap: in.OnOverlap, TimeoutMinutes: in.TimeoutMinutes,
	}
	if ok, why := migratable(x, cfg.Backend); !ok {
		return nil, coded("invalid", "%s", why)
	}
	source := in.Source
	if source == "" {
		source = "chat"
	}
	requestedBy := in.RequestedBy
	if requestedBy == "" {
		requestedBy = "assistant"
	}
	routineInput := RoutineInput{AssistantID: in.AssistantID, Name: in.Name, RequestedBy: requestedBy,
		Source: source, CorrelationID: in.CorrelationID, DefHash: DefinitionHash(x.WithDefaults())}
	// The command's durable receipt must be checked BEFORE any YAML writes.
	// A rejected replay must not change a Routine whose prompt it may not own.
	var preflightReplay bool
	if requestID != "" {
		_, preflightReplay, err = peekReceipt[Routine](a.Store, requestID+":r", "routine.upsert", routineInput)
		if err != nil {
			return nil, err
		}
		if preflightReplay {
			current := cfg.Find(in.Name)
			if current == nil || DefinitionHash(*current) != routineInput.DefHash || current.Rationale != x.Rationale {
				return nil, ErrRequestConflict
			}
		}
	}
	// Freeze the exact approval wording and first predicted runs before
	// touching YAML. Replaying the intent after a reboot must ask the same
	// question, even if the wall clock has advanced.
	next := []time.Time{}
	if sched, parseErr := config.CronParser.Parse(x.Cron); parseErr == nil {
		t := a.now()
		for range 3 {
			t = sched.Next(t)
			next = append(next, t)
		}
	}
	nextMS := make([]int64, len(next))
	for i, t := range next {
		nextMS[i] = t.UnixMilli()
	}
	intentID := requestID
	if intentID == "" {
		intentID = newID("routine-request")
	}
	intent := routineIntent{RequestID: intentID, Input: routineInput, Action: RoutineAction(x.WithDefaults(), next), Next: nextMS}
	if preflightReplay {
		// A successful replay may be days old; the approval receipt contains
		// the exact frozen action, not today's reconstructed NextRun.
		if prev, found, err := a.Store.routineApprovalReceipt(intentID); err != nil {
			return nil, err
		} else if found {
			if prev.SubjectKind != "routine" || prev.SubjectID != in.Name || prev.AssistantID != in.AssistantID {
				return nil, ErrRequestConflict
			}
			intent.Action = prev.Action
		}
	} else {
		// This WAL transaction commits first. Every possible process death
		// after it leaves a recoverable, dispatch-reserved intent.
		if err := a.Store.recordRoutineIntent(intent); err != nil {
			return nil, err
		}
		res, err := a.cfgPut(original, x)
		if err != nil {
			return nil, coded("internal", "%v", err)
		}
		if res.Problem != "" {
			if err := a.Store.dropRoutineIntent(in.Name); err != nil {
				return nil, err
			}
			return nil, coded("invalid", "%s", res.Problem)
		}
	}
	cfg, err = a.loadConfig()
	if err != nil || cfg.Find(in.Name) == nil {
		return nil, coded("internal", "the draft could not be read back; the intent will be retried")
	}
	r, ap, replayed, err := a.Store.completeRoutineIntent(intent, cfg, a.legacyLast(), a.now())
	if err != nil {
		return nil, err // journal is retained for recovery, never blindly undone
	}
	if !preflightReplay {
		if err := a.Store.dropRoutineIntent(in.Name); err != nil {
			return nil, err
		}
	}
	return ok("routine", a.routineView(r, cfg), "approval", ap, "next", nextMS, "replayed", replayed), nil
}

// afterApproval runs the keeper at once when the answer was a yes to a routine,
// so the person sees it enabled; the daemon's own pass is the safety net.
func (a *API) afterApproval(ap Approval, approved bool) {
	if approved && ap.SubjectKind == "routine" {
		if _, err := a.keeper().Tick(); err != nil {
			fmt.Fprintln(os.Stderr, "routine keeper:", err)
		}
	}
}
