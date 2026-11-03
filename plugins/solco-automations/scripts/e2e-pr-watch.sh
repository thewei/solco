#!/usr/bin/env bash
# Real-Herdr acceptance for PR Watch (stage B2): a local stand-in for the
# GitHub API (the daemon is pointed at it with SOLCO_ASSISTANTS_GITHUB_API),
# an isolated Herdr session and a real Agent. Asserts: unchanged PR -> 0
# tasks; a change -> exactly one task that a real Agent runs; Needs You set.
#
#   scripts/e2e-pr-watch.sh [agent] [model]
set -euo pipefail
# bash 3.2 (macOS) does not abort on a failing [[ ]]; assert says it out loud.
assert() { if ! eval "$1"; then echo "ASSERT FAILED: $1"; exit 1; fi; }
AGENT="${1:-pi}"
MODEL="${2:-magpie/zhipu/glm-5.3-flash}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"
SB="${E2E_DIR:-$HOME/.cache/solco-assistants-e2e}"
SESSION="solco-e2e"
BIN="$SB/solco-automations"
PORT="${E2E_GH_PORT:-$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')}"

rm -rf "$SB"; mkdir -p "$SB/state" "$SB/config" "$SB/work" "$SB/gh"
(cd "$HERE" && GOTOOLCHAIN=auto go build -o "$BIN" .)
git init -q "$SB/work/demo" && git -C "$SB/work/demo" -c user.name=e2e -c user.email=e2e@x commit -q --allow-empty -m init
echo '{"state":"open","merged":false,"title":"Add feature","head":{"sha":"aaa111"}}' > "$SB/gh/pr.json"
cat > "$SB/gh/server.py" <<PY
import http.server, os, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = open(os.path.join("$SB/gh", "pr.json"), "rb").read()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", $PORT), H).serve_forever()
PY
python3 "$SB/gh/server.py" & GH=$!

export SOLCO_ASSISTANTS_DB="$SB/assistants.sqlite" SOLCO_ASSISTANTS_WORKDIR="$SB/personal-work"
export SOLCO_ASSISTANTS_HERDR_SESSION="$SESSION" SOLCO_ASSISTANTS_GITHUB_API="http://127.0.0.1:$PORT"
export HERDR_PLUGIN_STATE_DIR="$SB/state" HERDR_PLUGIN_CONFIG_DIR="$SB/config"

if ! herdr --session "$SESSION" workspace list >/dev/null 2>&1; then
  nohup herdr --session "$SESSION" server >"$SB/herdr.log" 2>&1 &
  for _ in $(seq 1 20); do herdr --session "$SESSION" workspace list >/dev/null 2>&1 && break; sleep 0.5; done
fi
nohup "$BIN" daemon >"$SB/daemon.log" 2>&1 &
DAEMON=$!
cleanup() { kill "$DAEMON" "$GH" 2>/dev/null || true; herdr session stop "$SESSION" >/dev/null 2>&1 || true; }
trap cleanup EXIT
sleep 2
j() { python3 -c "import json,sys;d=json.load(sys.stdin);print($1)"; }

A=$(echo '{"requestId":"w-a","name":"PR Assistant"}' | "$BIN" assistant create | j 'd["assistant"]["id"]')
SPEC=$(python3 -c "import json;print(json.dumps(json.dumps({'repo':'o/r','number':12,'intervalSeconds':10,'agent':'$AGENT','model':'$MODEL','repoPath':'$SB/work/demo'})))")
G=$(echo "{\"requestId\":\"w-g\",\"assistantId\":\"$A\",\"title\":\"Watch PR 12\",\"commitments\":[{\"kind\":\"pr_watch\",\"spec\":$SPEC}]}" | "$BIN" assistant goal create | j 'd["goal"]["id"]')
echo '{"requestId":"w-ap","expectedRevision":1}' | "$BIN" assistant goal approve "$G" >/dev/null
echo "goal $G approved; baseline + 2 unchanged checks (~25s)"
sleep 25
N=$("$BIN" assistant task list --assistant "$A" | j 'len(d["tasks"])')
echo "tasks while unchanged: $N"; assert '[[ "$N" == 0 ]]'
CHK=$("$BIN" assistant goal show "$G" | j 'd["goal"]["commitments"][0]["lastCheckedAt"]')
echo "lastCheckedAt=$CHK"; assert '[[ "$CHK" != 0 ]]'

echo "--- the PR changes"
echo '{"state":"open","merged":false,"title":"Add feature","head":{"sha":"bbb222"}}' > "$SB/gh/pr.json"
for i in $(seq 1 60); do
  T=$("$BIN" assistant task list --assistant "$A" | j 'd["tasks"][0]["id"] if d["tasks"] else ""')
  [[ -n "$T" ]] && break; sleep 2
done
assert '[[ -n "$T" ]]'; echo "task: $T"
for i in $(seq 1 120); do
  STATE=$("$BIN" assistant task show "$T" | j 'd["task"]["state"]')
  echo "[$i] $STATE"
  case "$STATE" in done|failed|blocked|cancelled|uncertain) break;; esac
  sleep 3
done
sleep 12   # more watch ticks at the same revision must not add tasks
"$BIN" assistant task show "$T" | j '"needsYou=%s state=%s" % (d["task"]["needsYou"], d["task"]["state"])'
N=$("$BIN" assistant task list --assistant "$A" | j 'len(d["tasks"])')
NY=$("$BIN" assistant task show "$T" | j 'd["task"]["needsYou"]')
echo "tasks=$N needsYou=$NY"
assert '[[ "$STATE" == done && "$N" == 1 && "$NY" == True ]]'
echo PASS
