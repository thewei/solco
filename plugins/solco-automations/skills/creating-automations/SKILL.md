---
name: herdr-automations
description: Schedule a recurring agent task with herdr-automations (cron + prompt), or edit/inspect existing ones. Use whenever the user wants something to run on a schedule — "every morning", "each Monday", "nightly", "every week", "on a cron", "recurring", "automate this", "schedule an agent", "run this while I sleep" — and for triage/dependency-bump/report/standup chores that repeat. Also use to list, disable, or debug scheduled automations and their run history. New automations are created as drafts and approved by a human.
---

# Scheduling Herdr automations

An automation is a cron schedule plus a prompt. At the scheduled time the
daemon spawns the agent in a fresh git worktree of a repo and submits the
prompt. Your job: analyse what the user wants, draft a valid automation with
`add --draft --rationale`, and let a person approve it. **Creation defaults to
draft; scheduling starts only when a human approves.**

## Where the file lives

Ask the CLI rather than guessing:

```bash
herdr plugin config-dir com.whstudio.solco.automations    # → <dir>/automations.yaml
```

Fallback when Herdr isn't installed: `~/.config/herdr-automations/automations.yaml`.

## Entry format

```yaml
automations:
  - name: issue-triage            # unique
    cron: "0 9 * * 1-5"           # 5-field crontab, or @daily / @hourly / @weekly
    repo: ~/Projects/myapp        # repo the agent works in
    workspace: worktree           # worktree (default, fresh branch per run) | root | existing
    # workspace_id: w7           # required by, and only by, workspace: existing
    # session: automations       # optional Herdr session every run happens in
    agent: claude                 # any kind `herdr agent start` supports
    model: sonnet                 # optional → passed as --model
    prompt: |
      Triage new GitHub issues: label them, close duplicates,
      draft replies for the ones needing more info.
    rationale: |                 # the analysis behind a draft: goal, inputs, guardrails, cost
      Goal: keep the issue queue under 50 open. Inputs: gh issue list.
      Guardrails: labels only, no closes. Cost: one sonnet pass per weekday.
    draft: true                   # not scheduled until `approve <name>`
    on_overlap: skip              # what a due occurrence does when the last run still works
    cleanup_ttl_hours: 72         # daily pass removes merged run worktrees older than this
    mcp_config: ~/.config/mcp/github.json   # optional → passed as --mcp-config
    agent_args: ["--verbose"]               # optional, verbatim agent flags
    timeout_minutes: 60           # optional, default 60
    catch_up_minutes: 120         # optional: how late a sleep-delayed run may start; -1 never
    # disabled: true              # keep the entry, stop scheduling it
```

Instead of `prompt`, an entry may delegate to a herdr-workflows workflow:

```yaml
    workflow: nightly-deps        # runs `hwf run nightly-deps` in the pane
```

Exactly one of `prompt` / `workflow` is required.

## Workflow: draft first, approval second

```bash
# 1. Analyse, then draft. The rationale is what the approver reads — write
#    the goal, the inputs, the guardrails and the cost, not a restatement
#    of the prompt.
herdr-automations add --draft \
  --name issue-triage \
  --cron "0 9 * * 1-5" \
  --repo ~/Projects/myapp \
  --agent claude --model sonnet \
  --rationale "Goal: … Inputs: … Guardrails: … Cost: …" \
  --prompt "Triage new GitHub issues: …"

# 2. A person approves (the app's drawer, or the CLI):
herdr-automations approve issue-triage

# 3. Inspect and operate:
herdr-automations list --json       # draft/rationale/next_run/last_run/history30 per entry
herdr-automations history --json    # runs; status queued = waiting to catch up
herdr-automations run issue-triage  # refused while it is a draft
herdr-automations pause issue-triage
herdr-automations resume issue-triage
herdr-automations rm issue-triage
```

Edits from a form go through `put --original <name> < automation.json`, which
carries the new fields (`draft`, `rationale`, `on_overlap`,
`cleanup_ttl_hours`) too.

## Rules

- **Read the existing file first and append.** Never rewrite entries you didn't
  come to change.
- Names must be unique. Spaces are allowed; the branch name is slugified.
- Every entry is validated on load. A bad one is skipped with a diagnostic and
  the rest still run, so check `herdr-automations list` after writing: it names
  what did not load, with the line to fix.
- Times are local to the machine running the Herdr server.
- **Always set `model`** on kinds that accept it (`claude`, `codex`, `cursor`,
  `gemini`, `opencode`). Omitting it leaves an unattended run on whatever the
  agent defaults to. Match the model to the job: a summary or a triage pass is
  not a reason to reach for the most expensive one. Any other kind must use
  `agent_args` — a `model` on those fails to load.
- **Write the rationale before the prompt.** If you cannot say the goal,
  inputs, guardrails and cost in the rationale, the prompt is not ready.
- `on_overlap` decides collisions with a still-working run: `skip` (default)
  drops the occurrence; `queue` holds exactly one and runs it when free.
  Choose `queue` only for chores where a late run is still worth having.
- Automations due at the same minute **all start**: Herdr is a multi-agent
  runtime and nothing here serialises. `herdr-automations list` reports the
  overlaps; stagger the crons if running them at once is not what you want.
- The daemon reloads within 30 seconds; no restart needed.
- Occurrences missed while the machine slept run on wake within `catch_up_minutes`
  (default 120), otherwise they appear as `missed` in the history.
- `workspace: worktree` means the agent never touches the user's working copy.
  Only choose `root` when the task must see uncommitted local state.
- `workspace: existing` puts each run in a fresh tab of one workspace the user
  already created, instead of a new workspace per run. It needs a `workspace_id`
  from `herdr workspace list`, and like `root` it works directly in `repo` with
  no worktree. Propose it for a frequent schedule — hourly and up — where a
  workspace per run would bury the sidebar. Never invent the ID: if the user has
  not given one, ask, or write the entry with `worktree`.
- `session:` names the Herdr session runs happen in, whether triggered by cron
  or by hand. Add it only when the user asks for runs to land in a specific or
  dedicated session; `herdr session list` shows the names. The session must be
  running: the plugin never starts one, and a run aimed at a stopped one fails.
- A failed run and a broken entry raise a Herdr toast; a finished run now
  announces itself too (✓ … 完成 · 时长). A skipped or cancelled occurrence
  stays quiet. There is no event, push, or PR trigger and none is planned — if
  the user asks for one, propose a cron automation that polls instead
  (`gh pr list`, `git fetch`, …).

## Choosing the schedule

Ask only when it's genuinely ambiguous. Otherwise translate directly:
"every morning" → `0 9 * * *`, "weekdays at 9" → `0 9 * * 1-5`,
"each Monday at 9am" → `0 9 * * 1`, "nightly" → `@daily`,
"every week" → `0 9 * * 1`.

## Verify

```bash
herdr-automations list             # entry present, cron parsed, draft flagged
herdr-automations approve <name>   # a human's yes; until then it will not schedule
herdr-automations run <name>       # optional immediate test run (refused while a draft)
herdr-automations history <name>   # what happened
```

Report the next scheduled run time, and that the automation stays unscheduled
until someone approves the draft.
