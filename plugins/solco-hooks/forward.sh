#!/bin/sh
# Solco hook forwarder: Herdr injects HERDR_PLUGIN_EVENT (the event
# name) and HERDR_PLUGIN_EVENT_JSON (the payload). The Solco app tails
# this spool; override the directory with SOLCO_HOOK_SPOOL.
SPOOL="${SOLCO_HOOK_SPOOL:-$HOME/.solco/hooks}"
mkdir -p "$SPOOL" 2>/dev/null
printf '%s\t%s\n' "${HERDR_PLUGIN_EVENT:-}" "${HERDR_PLUGIN_EVENT_JSON:-}" >> "$SPOOL/events.jsonl" 2>/dev/null
exit 0
