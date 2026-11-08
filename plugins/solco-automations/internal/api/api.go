// Package api is what `--json` prints: the shapes the Herdr app panel reads.
// The panel never parses automations.yaml or history.jsonl itself (ADR 0011),
// so these structs are the contract — rename a field here and the panel breaks.
package api

import (
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/daemon"
	"github.com/thewei/solco/plugins/solco-automations/internal/history"
	"github.com/thewei/solco/plugins/solco-automations/internal/schedule"
)

// Listing is `ls --json`.
type Listing struct {
	Backend config.Backend `json:"backend"`
	Paused  bool           `json:"paused"`
	// Scheduler is the daemon's pid, 0 when none is running.
	Scheduler int    `json:"scheduler"`
	Config    string `json:"config"`
	// State is the folder history.jsonl lives in, for the panel to watch.
	State       string       `json:"state"`
	Automations []Automation `json:"automations"`
	Invalid     []Invalid    `json:"invalid"`
}

// Automation is one entry as the form edits it, plus what the list shows.
// The upstream spellings (workspace, next, last) are kept; the Solco
// contract's spellings (workspace_mode, next_run, last_run, history30) ride
// alongside so both clients decode the same bytes.
type Automation struct {
	Name           string           `json:"name"`
	Cron           string           `json:"cron"`
	Repo           string           `json:"repo"`
	Workspace      config.Workspace `json:"workspace"`
	WorkspaceID    string           `json:"workspace_id,omitempty"`
	Agent          string           `json:"agent,omitempty"`
	Model          string           `json:"model,omitempty"`
	Template       string           `json:"template,omitempty"`
	Prompt         string           `json:"prompt,omitempty"`
	Headless       bool             `json:"headless,omitempty"`
	Workflow       string           `json:"workflow,omitempty"`
	MCPConfig      string           `json:"mcp_config,omitempty"`
	AgentArgs      []string         `json:"agent_args,omitempty"`
	TimeoutMinutes int              `json:"timeout_minutes"`
	CatchUpMinutes int              `json:"catch_up_minutes"`
	Disabled       bool             `json:"disabled"`
	// Draft marks an automation the daemon will not schedule until someone
	// runs `approve`. Rationale is the analysis the drafter saved with it.
	Draft     bool   `json:"draft"`
	Rationale string `json:"rationale,omitempty"`
	// OnOverlap is what happens to an occurrence due while the previous run
	// is still working; CleanupTTLHours bounds a merged run worktree's life.
	OnOverlap       string    `json:"on_overlap"`
	CleanupTTLHours int       `json:"cleanup_ttl_hours"`
	Line            int       `json:"line"`
	Next            time.Time `json:"next"`
	Last            *Run      `json:"last"`
	// ScheduleText says the cron in words; OnceAt exists because the client
	// decodes it — this plugin has no one-shot schedules, so it is always
	// null rather than a lie.
	ScheduleText  string           `json:"schedule_text"`
	OnceAt        *time.Time       `json:"once_at"`
	WorkspaceMode config.Workspace `json:"workspace_mode"`
	NextRun       *time.Time       `json:"next_run"`
	LastRun       *RunSummary      `json:"last_run"`
	History30     map[string]int   `json:"history30"`
}

// Invalid is an entry that did not load, and where to fix it.
type Invalid struct {
	Name    string `json:"name"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Run is one run as the inbox and the detail view show it. The upstream
// spellings are kept; the Solco contract's (name, started_at, finished_at,
// duration_seconds, worktree, workspace_id, pane_id, agent) ride alongside.
type Run struct {
	RunID           string         `json:"run_id"`
	Automation      string         `json:"automation"`
	Name            string         `json:"name"`
	Status          history.Status `json:"status"`
	Trigger         string         `json:"trigger,omitempty"`
	Started         time.Time      `json:"started"`
	At              time.Time      `json:"at"`
	Seconds         int            `json:"seconds"`
	Space           string         `json:"space,omitempty"`
	Terminal        string         `json:"terminal,omitempty"`
	Error           string         `json:"error,omitempty"`
	Seen            bool           `json:"seen"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
	DurationSeconds int            `json:"duration_seconds"`
	Worktree        string         `json:"worktree,omitempty"`
	WorkspaceID     string         `json:"workspace_id,omitempty"`
	PaneID          string         `json:"pane_id,omitempty"`
	Agent           string         `json:"agent,omitempty"`
}

// RunSummary is the contract's last_run: the facts the list shows inline,
// without dragging the whole inbox record along.
type RunSummary struct {
	RunID           string         `json:"run_id"`
	Status          history.Status `json:"status"`
	StartedAt       time.Time      `json:"started_at"`
	DurationSeconds int            `json:"duration_seconds"`
	Worktree        string         `json:"worktree,omitempty"`
	WorkspaceID     string         `json:"workspace_id,omitempty"`
	PaneID          string         `json:"pane_id,omitempty"`
	// Seen is whether the run was opened (history's own flag); the clients'
	// unread counts read it from here instead of fetching the whole history.
	Seen bool `json:"seen"`
}

// List reads the config and the latest run of each automation.
func List() (Listing, error) {
	cfg, err := config.Load()
	if err != nil {
		return Listing{}, err
	}
	pid, _ := daemon.Running()
	out := Listing{
		Backend: cfg.Backend, Paused: cfg.Paused, Scheduler: pid, Config: config.Path(), State: config.StateDir(),
		Automations: []Automation{}, Invalid: []Invalid{},
	}
	now := time.Now()
	agents := agentByAutomation(cfg)
	for _, a := range cfg.Automations {
		entry := Automation{
			Name: a.Name, Cron: a.Cron, Repo: a.Repo, Workspace: a.Workspace, WorkspaceID: a.WorkspaceID,
			Agent: a.Agent, Model: a.Model, Template: a.Template, Prompt: a.Prompt, Headless: a.Headless, Workflow: a.Workflow,
			MCPConfig: a.MCPConfig, AgentArgs: a.AgentArgs, TimeoutMinutes: a.TimeoutMinutes,
			CatchUpMinutes: a.CatchUpMinutes, Disabled: a.Disabled, Line: a.Line,
			Draft: a.Draft, Rationale: a.Rationale, OnOverlap: a.OnOverlap,
			CleanupTTLHours: a.CleanupTTLHours,
			ScheduleText:    schedule.ScheduleText(a),
			WorkspaceMode:   a.Workspace,
		}
		if a.Template != "" {
			entry.Agent = "" // the default filled in at load; a template brings its own
		}
		if sched, err := config.CronParser.Parse(a.Cron); err == nil {
			entry.Next = sched.Next(now)
			// A draft and a disabled automation have no next run to show;
			// null says so without inventing a timestamp.
			if !a.Draft && !a.Disabled {
				next := entry.Next
				entry.NextRun = &next
			}
		}
		if r, _ := history.LastRun(a.Name); r != nil {
			last := toRun(*r, agents)
			entry.Last = &last
			summary := toSummary(*r)
			entry.LastRun = &summary
		}
		entry.History30 = history30(a.Name, now)
		out.Automations = append(out.Automations, entry)
	}
	for _, d := range cfg.Invalid {
		out.Invalid = append(out.Invalid, Invalid{Name: d.Name, Line: d.Line, Message: d.Message})
	}
	return out, nil
}

// ListJSON is List in the envelope the Solco client decodes: everything
// under "result".
func ListJSON() (Envelope, error) {
	listing, err := List()
	return Envelope{Result: listing}, err
}

// Envelope wraps every --json payload the Solco client reads: {"result": ...}.
type Envelope struct {
	Result Listing `json:"result"`
}

// HistoryJSON is History in the contract's envelope: {"result":{"runs":[...]}}.
type historyEnvelope struct {
	Result struct {
		Runs []Run `json:"runs"`
	} `json:"result"`
}

// History lists recent runs, newest first; name filters to one automation.
func History(name string, limit int) ([]Run, error) {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{} // the log outlives the config; show it unattributed
	}
	agents := agentByAutomation(cfg)
	recs, err := history.Runs(name, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(recs))
	for _, r := range recs {
		out = append(out, toRun(r, agents))
	}
	return out, nil
}

// HistoryJSON is history --json: the contract's {"result":{"runs":[...]}}.
func HistoryJSON(name string, limit int) (historyEnvelope, error) {
	var out historyEnvelope
	runs, err := History(name, limit)
	if err != nil {
		return out, err
	}
	out.Result.Runs = runs
	return out, nil
}

// agentByAutomation maps each loaded automation to its agent kind, so a run
// record can say who worked it without the log storing it.
func agentByAutomation(cfg *config.Config) map[string]string {
	agents := map[string]string{}
	for _, a := range cfg.Automations {
		agents[a.Name] = a.Agent
	}
	return agents
}

// history30 tallies an automation's runs of the last retention window by
// final status — the "history30" the list shows under each row.
func history30(name string, now time.Time) map[string]int {
	recs, err := history.Runs(name, 0)
	if err != nil {
		return map[string]int{}
	}
	cutoff := now.Add(-history.HistoryRetention)
	counts := map[string]int{}
	for _, r := range recs {
		if r.At.Before(cutoff) {
			continue
		}
		counts[string(r.Status)]++
	}
	return counts
}

func toRun(r history.Record, agents map[string]string) Run {
	secs := int(r.At.Sub(r.Started).Seconds())
	return Run{
		RunID: r.RunID, Automation: r.Automation, Name: r.Automation, Status: r.Status,
		Trigger: string(r.Trigger),
		Started: r.Started, At: r.At, Seconds: secs,
		Space: r.WorkspaceID, Terminal: r.PaneID, Error: r.Error, Seen: r.Seen,
		StartedAt: r.Started, FinishedAt: r.At, DurationSeconds: secs,
		Worktree: r.Worktree, WorkspaceID: r.WorkspaceID, PaneID: r.PaneID,
		Agent: agents[r.Automation],
	}
}

func toSummary(r history.Record) RunSummary {
	return RunSummary{
		RunID: r.RunID, Status: r.Status,
		StartedAt:       r.Started,
		DurationSeconds: int(r.At.Sub(r.Started).Seconds()),
		Worktree:        r.Worktree, WorkspaceID: r.WorkspaceID, PaneID: r.PaneID, Seen: r.Seen,
	}
}
