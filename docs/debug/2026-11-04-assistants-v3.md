# 2026-11-04 Assistants 3.1 — evidence log

Progress and acceptance live in `docs/assistants-v3-implementation-tracker.md`; this file keeps the live-environment findings.

## U2 live check (isolated `HERDR_PLUGIN_STATE_DIR`, real HOME MCP files, real `claude`)

- Setup: rebuilt `plugins/solco-automations/bin/solco-automations`, restarted the daemon with Python `start_new_session=True` (a plain background start dies when the shell call ends).
- Create Assistant → default instructions rev 1. `persona mcp-grant sequential-thinking`, `persona set-runtime` effort `low`, `coordinator start`.
- Process args: `claude --effort low --strict-mcp-config --mcp-config <state>/assistants-mcp/<id>.json` (file 0600, only the granted server).
- Pane `/mcp`: exactly one server, `sequential-thinking` connected, although 12 servers are configured on this Mac.
- Finding: a Coordinator folder outside an already trusted folder stops Claude at the "trust this folder" dialog (pane `blocked`) even though the Coordinator row reads `ready`. The group delivery pump already refuses to type into a startup dialog (`paneDialog`); the desktop Chat shows its Open Terminal card. Not auto-answered (outside the 2026-11-03 trust decision's scope). Revisit if first-run Coordinators hit it.
- Lesson: shell state persists between tool calls — an exported `HERDR_PLUGIN_STATE_DIR` silently points the CLI *and* a daemon started from that shell at the isolated directory; check `echo $HERDR_PLUGIN_STATE_DIR` before concluding which DB a test touched.
