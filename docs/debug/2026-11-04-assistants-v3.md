# 2026-11-04 Assistants 3.1 — evidence log

Progress and acceptance live in `docs/assistants-v3-implementation-tracker.md`; this file keeps the live-environment findings.

## U2 live check (isolated `HERDR_PLUGIN_STATE_DIR`, real HOME MCP files, real `claude`)

- Setup: rebuilt `plugins/solco-automations/bin/solco-automations`, restarted the daemon with Python `start_new_session=True` (a plain background start dies when the shell call ends).
- Create Assistant → default instructions rev 1. `persona mcp-grant sequential-thinking`, `persona set-runtime` effort `low`, `coordinator start`.
- Process args: `claude --effort low --strict-mcp-config --mcp-config <state>/assistants-mcp/<id>.json` (file 0600, only the granted server).
- Pane `/mcp`: exactly one server, `sequential-thinking` connected, although 12 servers are configured on this Mac.
- Finding: a Coordinator folder outside an already trusted folder stops Claude at the "trust this folder" dialog (pane `blocked`) even though the Coordinator row reads `ready`. The group delivery pump already refuses to type into a startup dialog (`paneDialog`); the desktop Chat shows its Open Terminal card. Not auto-answered (outside the 2026-11-03 trust decision's scope). Revisit if first-run Coordinators hit it.
- Lesson: shell state persists between tool calls — an exported `HERDR_PLUGIN_STATE_DIR` silently points the CLI *and* a daemon started from that shell at the isolated directory; check `echo $HERDR_PLUGIN_STATE_DIR` before concluding which DB a test touched.

## U3 live check (isolated `HERDR_PLUGIN_STATE_DIR`, rebuilt plugin binary, temp git repo `/tmp/u3repo`)

- `go build -o bin/solco-automations .` (the plugin main package is at the module root, not `cmd/`), daemon restarted with `start_new_session`.
- A coding milestone was used on purpose: the Supervisor creates its task as a **Draft** (publisher-gated), so no real Agent starts and nothing costs money.
- Result after approve + 12 s: plan `waiting_user`, m1 `needs_you` with one task, m2 `pending`; facts `accepted`, `planned`, `delegated`. After a daemon restart and 15 s: still one task, same three facts (requestId `sup-<goal>-<ms>-<attempt>` + the `delegated` fact written in the same transaction).
- Lesson: a milestone with `kind: coding` needs `repoPath`; research/other kinds would be picked up by the real runner and start an Agent — use a Draft path for live checks.


## U4 live check (isolated state **and** config dirs, real daemon, temp repo `/tmp/u3repo`)

- Finding before the run: `HERDR_PLUGIN_STATE_DIR` isolates the database, not `automations.yaml` — the daemon's config path comes from `HERDR_PLUGIN_CONFIG_DIR`. The earlier U2/U3 live checks therefore read (never wrote) the real yaml with an isolated schedule state. From U4 on both variables are set before anything that writes.
- Loop observed: draft (yaml `draft: true`, 70 s of an every-minute cron → 0 tasks) → `approval decide` with the card's own hash → draft flag cleared, routine `active` → one task at the next occurrence, owner = the drafting Assistant, done in 15 s (real headless `claude -p`) → ledger `routine_done`. The old scheduler's history log has exactly one entry for it. `routine pause` stopped further runs.
- Cleanup done by hand: automation removed from the isolated yaml, temp worktree and branch removed, the Herdr workspaces this testing created (`auto: …` ones and the temp repo's) closed.

## U5 live check (isolated state + config dirs, `SOLCO_ASSISTANTS_MEMORY` temp dir, real daemon)

- The daemon projects a folder for every Assistant in the DB within one 5 s tick (the other three isolated Assistants got empty `README`/`INDEX` too) — expected, but note it when a live check starts a daemon against a populated state.
- Observed: add → `facts/<id>.md` (front matter id/assistantId/revision/kind/state/sourceRef/updatedAt); a person's edit → revision +1 (author `files`); a `.md` dropped in `notes/` → one candidate (`file:notes/idea.md`), original removed; a file edit that races a core edit (CLI correct with the old revision fence) → the core text wins, the file is restored, the file's text survives as `conflict:<id>@<rev>` candidate; a credential-looking edit is restored.
- Pitfall: staleness is judged by what the core last *projected* to that path, not by the `revision:` line in the file — changing the front matter by hand does nothing. Only an edit that crosses a core change within a sync window conflicts.
- Pitfall: CLI memory writes carry `assistantId` in the JSON body (add) and `expectedRevision` after any intervening edit; a wrong revision is refused as stale (it looked like a silent failure when the output was piped to a parser).
- Flaky golden found by repeated runs: items created in a fixed-clock test tie on `created_at`, so `ORDER BY created_at, id` followed random ids. Production never ties; the contract test now advances the clock.

