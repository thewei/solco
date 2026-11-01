# Cursor Agent + Antigravity CLI in Chat — real-device matrix

Status: P1 (Chat read/send/queue/interrupt) and P2 (Sessions index, Continue) are built (AGENTS.md, 2026-11-01).
This file is the real-device matrix that gates the rest: the `/` `$` catalog, models, and P3 structured interactions.
Probed 2026-11-01 on Herdr 0.9.5, `cursor-agent` 2026.10.19-6bbf2c4 (Composer 2.5), `agy` (Gemini 3.8 Flash), in a scratch workspace `/tmp/matrix-probe` driven only through `herdr agent …` / `herdr pane …`.

## Cursor Agent — verified

| Question | Result | Consequence |
|---|---|---|
| `agent_session` shape | `{kind:"id", source:"herdr:cursor", value:<chatId>}`, present as soon as the first prompt is accepted | `ResolveLiveSource` id path is the one used; no path form to handle |
| Workspace trust (first run in a directory) | Full-screen "Trust this workspace" `[a]/[q]`; Herdr reports `idle`; a Herdr prompt sent meanwhile is swallowed | Chat send can silently vanish in an untrusted directory → needs a Blocked card with `Open Terminal` (P3) |
| Shell permission (Allowlist mode) | Herdr `blocked` (rule `approval_prompt`); screen: `Run (once) (y)` / `Add Shell(x) to allowlist? (tab)` / `Run Everything (shift+tab)` / `Skip & tell the agent … (esc or n)` | Structurable: 4 fixed options with fixed keys; `y`, `tab`, `shift+tab`, `n` delivered through `agent.send_keys` worked |
| Skip (`n` / `esc`) | Opens a free-text step "Tell the agent what to do instead"; Enter (empty) sends the skip and the agent retries → a second prompt; `esc` in that step returns to the menu | A Skip answer is two keys (`n`, `enter`); the card must stay for the retry |
| Approval modes | Config `approvalMode`: `allowlist` / `auto-review` / `unrestricted` ("Run Everything"); the user's own is `unrestricted`, and `--auto-review` on the command line loses to the config | In the user's normal setup no permission prompt ever appears; the card only matters for allowlist users |
| `AskQuestion` | Full-screen dialog (single and multi select, `Other` free text, `↑/↓ · ←/→ question · Space · Enter · Esc`); **Herdr stays `idle`/`done`, never `blocked`**; the store holds only the user turn until the dialog is answered, then the tool-call (`args.questions[{id,prompt,options[{id,label}],allow_multiple}]`) and the result (`answers[{questionId,selectedOptionIds}]`) appear together | A question card cannot come from the transcript while pending and `reconcileBlockedInteractions` (needs `blocked`) never fires → P3 must detect it from the screen while the agent is idle, or fall back to `Open Terminal`; answered ones already render as the AskQuestion row |
| Interrupt | One `escape` stops a running turn (tool shows `Cancelled`, status `done`); a cancelled tool call is **not** written to the store | `chat.Stop` (Escape) works unchanged; a stopped turn shows the user row with no answer |
| Rejected tool | Store row result `Rejected: `, `highLevelToolCallResult.isError:true` | already mapped to a failed call |
| Timestamps | Not per message, but every user message starts with `<timestamp>Sunday, Nov 1, 2026, 1:42 PM (UTC+8)</timestamp>` (minute precision, English locale) | Used: `history.cursorMessageTime` parses it into the user row's time and the session's start/last-active (rows the source already held when Solco looked have no other time) |
| Queue/steering | `Steering enabled` in config, but Herdr's busy refusal applies first; unchanged delivery path | none |

### Cursor — catalog rows (cursor-agent 2026.10.22)

- **Built-ins** (`/` list, 70+ entries incl. models): `/model [filter]`, `/goal`, `/add-dir`, `/plan [prompt]`, `/ask`, `/debug [prompt]`, `/run-everything`, `/auto-review`, `/max-mode`, `/fast`, `/rename <name>`, `/clear`, `/resume`, `/fork`, `/summarize`, `/rewind`, `/context`, `/usage`, `/skills`, `/rule`, `/command`, `/changes`, `/commit`, `/jobs`, `/btw <q>`, `/open`, `/copy-request-id`, `/copy-conversation-id`, `/about`, `/help`, `/logout`, … Skills and custom commands appear in the same list (skills = `/name`).
- **Model list**: every model is also its own slash command `/<slug>` (`/composer-2-5`, `/claude-sonnet-5-5`, …) — 46 models at probe time, per-account; `cursor-agent --list-models` is the non-TUI source.
- **Typed model switch works**: `/model <filter>` + Enter selects the first match immediately, no picker. **It rewrites the user's global default** (`~/.cursor/cli-config.json` `model`, `selectedModel`, `modelSelectionHistory`, `modelParameters`) — unlike Claude's session-only `s`. Chat must say so in the confirmation text, or leave model switching to Terminal.
- `/model` alone opens a filterable list (`Type to filter • Enter • Tab to edit`); Herdr stays `idle` while it is open, so no `blocked` card is possible for it.
- Mode cycling: `shift+tab` = Plan → Debug → Ask → Agent (a footer label, no Herdr state); `/plan`, `/ask`, `/debug` are the typed equivalents.
- The CLI auto-updates between runs (2026.10.19 → .22 during the probe); the permission and AskQuestion screens above were the same on both.

## Antigravity CLI — verified (agy 1.4.12, Gemini 3.8 Flash)

First-run onboarding (colour scheme → Terms & Data Use, pre-ticked consent → folder trust) appeared although the account was already signed in. Herdr reports `done` on the first two pages and `blocked` on folder trust. The consent page was handled on the user's instruction (unticked; Done proceeds, consent is not required).

| Question | Result | Consequence |
|---|---|---|
| `agent_session` shape | `{kind:"id", source:"herdr:antigravity_cli", value:<conversationId>}` once the first prompt is accepted (None before) | id path in `ResolveLiveSource` is the live one; matches the brain folder name |
| Transcript timing | `transcript_full.jsonl` gets the PLANNER_RESPONSE row **with `tool_calls` while the call is still pending**; the result step (GENERIC/ERROR) lands later | a running tool shows as running (better than Cursor); a permission or question card can be derived from the pending call |
| Shell permission | Herdr `blocked`; screen `Requesting permission for: <cmd>` + `1. Yes, run command` / `2. … always allow in this conversation …` / `3. … always allow … (Persist to settings.json)` / `4. No, cancel`; a bare digit selects at once; `4` ends the turn (`Interrupted · What should Antigravity CLI do instead?`, status `done`), the step is `ERROR` "user denied permission…" | structurable: keys `1`/`2`/`4` (never offer `3` — it writes `~/.gemini/antigravity-cli/settings.json`) |
| `ask_question` | Herdr `blocked`; screen `Question n/m: …`, numbered options (a `(Recommended)` label prefix is only a label — **the cursor starts on row 1**, not on the recommended row), `Write-in...` appended on screen, `↑/↓ · enter · esc Skip`; multi-select rows are `[ ]`, `space` toggles, `enter` submits all; transcript holds `args.questions[{question, options[] (plain strings), is_multi_select}]` while pending; result step content `A1: Blue`. Selecting Write-in opens `Your answer:` (Herdr `done`, not blocked) and `agent.prompt <text>` there submits it (probed) | the existing Question card (like omp's `ask`, stepped one question at a time): arrow navigation, `Start=0`, a trailing `Write-in...` row for single choice only, multi-select ticked on the page (`MultiSelectKeys` unchanged); the typed answer goes through the ordinary prompt path |
| `/` list | 89 entries: ~30 built-ins (`/model`, `/permissions`, `/resume`, `/rewind`, `/fork`, `/clear`, `/config`, `/context`, `/usage`, `/skills`, `/tasks`, `/plan`, `/goal`, `/btw`, `/copy`, `/rename`, …) then every installed skill as `/skill-name` (plus `/plugin:skill`) | skills catalog = the same folders Solco already scans (`~/.agents/skills`), spelled `/name` |
| Typed model switch | `/model <display name>` + Enter sets it (`Claude Sonnet 4.6 (Thinking)`, `Gemini 3.8 Flash (High|Medium|Low)`, `GPT-OSS 120B (Medium)`, `Gemini 4 Argon`); autocomplete shows `display name  model-id`. **It persists to `settings.json` `model`** (restored after the probe) | same caveat as Cursor: a global default changes |
| `/model` alone | list + effort slider, Herdr `done` | not structurable; hand to Terminal |
| Interrupt | one `escape` mid-turn: `⎿ Interrupted · What should Antigravity CLI do instead?`, Herdr `done` (probed) | `chat.Stop` works unchanged |
| Probe side effects | folder trust for the scratch dir, model default, tool-permission nothing persisted | all reverted/removed |

## Decision (approved 2026-11-01, built)

1. **Shell-permission cards for both providers** — Herdr `blocked` triggers them; `chatsession.DetectAgentPrompt` reads the screen. Cursor: `Run (once)` → `y`, `Skip` → `n`,`enter`; agy: `Yes, run command` / `…always allow in this conversation…` / `No, cancel`, delivered as arrow navigation + `enter` through `PendingInteraction.OptionKeys`. The persistent rows (Cursor's allowlist / Run Everything, agy's `Persist to settings.json`) are never offered. Terminal agents run in permission mode.
2. **agy `ask_question` → Question card** from the pending transcript call (`chatsession.TranscriptQuestion`), on desktop and phone; `Write-in...` counts as a free-text option.
3. **Open Terminal Blocked cards** for the dialogs Herdr reports as idle/done — Cursor AskQuestion and workspace trust, agy first-run setup and folder trust (`chatsession.DetectTerminalDialog`; desktop `chat_terminal_dialog.go` reads the pane on bind / projection apply and re-checks every 2 s while the dialog is up; the phone has no such hold yet).
4. **Catalog**: skills as `/name` (Cursor: `~/.cursor/skills`, `~/.cursor/skills-cursor`, `~/.claude/skills`, `~/.agents/skills` + project; agy: `~/.gemini/skills`, `~/.gemini/antigravity-cli/skills`, `~/.claude/skills`, project), hand-verified built-ins, no persistent/cost toggles. **Models: Cursor has no model list or typed switch** — the user only allows Gemini 3.8 Flash because every other model raises the bill, and `/model <filter>` takes the first match; `/model` goes to Terminal. agy lists `agy models` (display name + id; any `max` token is dropped by `agentcatalog.IsMaxMode`), switching is a typed `/model <Display Name>` on desktop with a visible "changes your default model" note; the phone received the same switch on 2026-11-01, once the note travelled with the catalog.
5. **Cursor `<timestamp>`** seeds user-row times and the session's start/last-active.
