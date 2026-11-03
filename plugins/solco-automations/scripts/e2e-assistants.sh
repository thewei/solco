#!/usr/bin/env bash
# Real-Herdr acceptance for the Assistants core (stage A/B): an isolated Herdr
# session + a scratch git repo + a throwaway database. Never touches the user's
# default session or real repositories.
#
#   scripts/e2e-assistants.sh [agent] [model]       default: pi magpie/zhipu/glm-5.3-flash
set -euo pipefail
# bash 3.2 (macOS) does not abort on a failing [[ ]]; assert says it out loud.
assert() { if ! eval "$1"; then echo "ASSERT FAILED: $1"; exit 1; fi; }
AGENT="${1:-pi}"
MODEL="${2:-magpie/zhipu/glm-5.3-flash}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"
SB="${E2E_DIR:-$HOME/.cache/solco-assistants-e2e}"
SESSION="solco-e2e"
BIN="$SB/solco-automations"

rm -rf "$SB"; mkdir -p "$SB/state" "$SB/config" "$SB/work"
(cd "$HERE" && GOTOOLCHAIN=auto go build -o "$BIN" .)
git init -q "$SB/work/demo" && git -C "$SB/work/demo" -c user.name=e2e -c user.email=e2e@x commit -q --allow-empty -m init
echo "scratch repo" > "$SB/work/demo/README.md"

export SOLCO_ASSISTANTS_DB="$SB/assistants.sqlite"
export SOLCO_ASSISTANTS_WORKDIR="$SB/personal-work"
export SOLCO_ASSISTANTS_HERDR_SESSION="$SESSION"
export HERDR_PLUGIN_STATE_DIR="$SB/state"
export HERDR_PLUGIN_CONFIG_DIR="$SB/config"

if ! herdr --session "$SESSION" workspace list >/dev/null 2>&1; then
  nohup herdr --session "$SESSION" server >"$SB/herdr.log" 2>&1 &
  for _ in $(seq 1 20); do herdr --session "$SESSION" workspace list >/dev/null 2>&1 && break; sleep 0.5; done
fi

nohup "$BIN" daemon >"$SB/daemon.log" 2>&1 &
DAEMON=$!
cleanup() { kill "$DAEMON" 2>/dev/null || true; herdr session stop "$SESSION" >/dev/null 2>&1 || true; }
trap cleanup EXIT
sleep 2

A=$(echo '{"requestId":"e2e-a","name":"E2E Assistant"}' | "$BIN" assistant create | python3 -c 'import json,sys;print(json.load(sys.stdin)["assistant"]["id"])')
echo "assistant: $A"
BRIEF='Read README.md in this folder and reply with one sentence saying what it contains. Do not modify any file.'
T=$(python3 - "$A" "$AGENT" "$MODEL" "$SB/work/demo" "$BRIEF" <<'PY' | "$BIN" assistant task create | python3 -c 'import json,sys;print(json.load(sys.stdin)["task"]["id"])'
import json,sys
a,agent,model,repo,brief=sys.argv[1:6]
print(json.dumps({"requestId":"e2e-t","assistantId":a,"kind":"research","title":"Read the README","brief":brief,"agent":agent,"model":model,"repoPath":repo}))
PY
)
echo "task: $T"
for i in $(seq 1 120); do
  STATE=$("$BIN" assistant task show "$T" | python3 -c 'import json,sys;print(json.load(sys.stdin)["task"]["state"])')
  echo "[$i] $STATE"
  if [[ "${E2E_RESTART:-}" == 1 && "$STATE" == running && -z "${RESTARTED:-}" ]]; then
    echo "--- killing the daemon mid-run and starting a new one"
    sleep "${E2E_RESTART_DELAY:-0}"
    kill -9 "$DAEMON"; wait "$DAEMON" 2>/dev/null || true
    nohup "$BIN" daemon >>"$SB/daemon.log" 2>&1 &
    DAEMON=$!; RESTARTED=1
    sleep 4
    echo "state after restart: $("$BIN" assistant task show "$T" | python3 -c 'import json,sys;print(json.load(sys.stdin)["task"]["state"])')"
  fi
  case "$STATE" in done|failed|blocked|cancelled|uncertain|review_required) break;; esac
  sleep 3
done
"$BIN" assistant task show "$T"
herdr --session "$SESSION" workspace list
echo "--- daemon log"; cat "$SB/daemon.log"
TASKS=$("$BIN" assistant task list | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["tasks"]))')
RUNS=$("$BIN" assistant run list | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["runs"]))')
echo "tasks=$TASKS runs=$RUNS"
assert '[[ "$STATE" == "done" && "$TASKS" == 1 && "$RUNS" == 1 ]]'
