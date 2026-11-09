package agenthooks

/**
 * [INPUT]: 内嵌的通用 Agent hook 脚本（v10：支持 Antigravity CLI 完整生命周期 SessionStart/PreInvocation/PreToolUse/PostToolUse/Stop 与 JSON stdout 应答，支持 Cursor camelCase 与 Codex 子线程闸；支持基于 PTY/TTY 交互进程拓扑与 Herdr 权威源双层解析 Codex 跨 pane daemon）
 * [OUTPUT]: 对外提供 hookScriptTemplate、hookScriptMarker、hookScriptVersion
 * [POS]: agenthooks 的脚本供给源；install 原子写入 ~/.solco/hooks/ 下的 provider 副本
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// hookScriptVersion is the version this build provisions; the marker line
// inside the template must match.
const hookScriptVersion = 10

// hookScriptMarker identifies files installed by Solco; foreign files
// at our install paths are never overwritten.
const hookScriptMarker = "installed by Solco"

// hookScriptTemplate is the universal direct-connect hook script shared by
// every provider copy. It reads stdin hook payloads, normalizes the event
// into a status, then (1) reports to the Solco apphook socket when
// HERDR_PANE_ID/TMUX_PANE identify a managed pane, (2) reports session
// identity to Herdr's pane.report_agent_session, and (3) emits an OSC 1337
// status escape for inline sniffing. It never logs content and fails open.
const hookScriptTemplate = `#!/bin/sh
# installed by Solco
# managed by Solco; reinstalling or updating the hook overwrites this file.
# SOLCO_HOOK_VERSION=10

set -eu

AGENT="${1:-unknown}"
EVENT_ARG="${2:-}"

# Antigravity CLI 等要求 hook 命令退出时 stdout 为合法 JSON；兜底保证有 JSON 输出
if [ "$AGENT" = "agy" ] || [ "$AGENT" = "antigravity" ]; then
    trap 'exit_code=$?; [ $exit_code -ne 0 ] && printf "{}\n"; exit $exit_code' EXIT
fi

PANE_ID="${HERDR_PANE_ID:-${TMUX_PANE:-}}"
if [ -z "$PANE_ID" ]; then
    if [ "$AGENT" = "agy" ] || [ "$AGENT" = "antigravity" ]; then
        if [ "$EVENT_ARG" = "PreToolUse" ]; then
            printf '{"decision":"allow"}\n'
        else
            printf '{}\n'
        fi
        exit 0
    fi
    # Cursor GUI sessions and Codex app-server daemon sessions fall through:
    # python reports to the status socket and resolves pane from cwd/session.
    if [ "$AGENT" != "cursor" ] && [ "$AGENT" != "codex" ]; then
        exit 0
    fi
fi

HOOK_INPUT=""
if [ ! -t 0 ]; then
        HOOK_INPUT="$(cat 2>/dev/null || true)"
fi

python3 - <<'PY' "$AGENT" "$PANE_ID" "$HOOK_INPUT" "$EVENT_ARG"
import sys, os, json, socket, time

agent = sys.argv[1] if len(sys.argv) > 1 else "unknown"
pane_id = sys.argv[2] if len(sys.argv) > 2 else ""
raw_input = sys.argv[3] if len(sys.argv) > 3 else ""
event_arg = sys.argv[4] if len(sys.argv) > 4 else ""

hook_data = {}
if raw_input.strip():
    try:
        hook_data = json.loads(raw_input)
    except Exception:
        pass

# Cursor 会把 ~/.claude/settings.json 里的 Claude hook 一并执行，载荷是 Cursor
# 原生的（带 cursor_version）。那一份不是 Claude：Claude 副本静默，由 Cursor
# 自己的 hook 副本报状态与会话身份。
if agent == "claude" and hook_data.get("cursor_version"):
    sys.exit(0)

event_name = (
    hook_data.get("hook_event_name")
    or hook_data.get("event")
    or hook_data.get("type")
    or event_arg
    or ""
)

# 常见 prompt 载荷字段（Claude UserPromptSubmit.prompt / agy PreInvocation.lastUserInput 等）：
# 作为语义文本随报告下发，journal 侧才可能有正文。
prompt_text = (
    hook_data.get("prompt")
    or hook_data.get("user_query")
    or hook_data.get("message")
    or hook_data.get("text")
    or hook_data.get("lastUserInput")
    or ""
)

session_id = (
    hook_data.get("session_id")
    or hook_data.get("conversationId")
    or hook_data.get("conversation_id")
)
if not session_id:
    # agy 1.2.7 的 PreInvocation 载荷实测不带会话 id（herdr 官方 hook
    # 因此静默失败）。从祖先进程参数恢复：链路是 agy -> hook.sh ->
    # python，直接父进程是 hook.sh 自身，必须向上走到 agy 那一级
    # 才能看到 --conversation <uuid>（2026-09-19 实测修复）。
    try:
        import re as _re
        import subprocess as _subprocess
        _pid = str(os.getppid())
        for _ in range(4):
            _args = _subprocess.run(
                ["ps", "-p", _pid, "-o", "args="],
                capture_output=True, text=True, timeout=2,
            ).stdout
            _match = _re.search(r"--conversation[= ]([0-9a-fA-F-]{8,})", _args)
            if _match:
                session_id = _match.group(1)
                break
            _pid = _subprocess.run(
                ["ps", "-p", _pid, "-o", "ppid="],
                capture_output=True, text=True, timeout=2,
            ).stdout.strip()
            if not _pid or _pid == "0":
                break
    except Exception:
        pass
# Codex 子进程（在父会话 pane 里再起的 codex）继承父的 CODEX_THREAD_ID，
# 与自己的 session_id 不同——它的事件不是这个 pane 的会话（与 herdr 官方
# codex 脚本同一道闸）。
if agent == "codex":
    _inherited = os.environ.get("CODEX_THREAD_ID")
    if _inherited and session_id and _inherited != session_id:
        sys.exit(0)

cwd = (
    hook_data.get("cwd")
    or hook_data.get("project_path")
    or os.getcwd()
)

# Codex runs hooks inside the background app-server daemon (tty=??), which
# inherits the environment of whichever pane first spawned it (or no pane at all).
# Scheme A (PTY/TTY Process Topology): Discover active interactive codex processes
# with a real controlling terminal (tty != "??") and read their direct HERDR_PANE_ID.
if agent == "codex":
    _found_pane = None
    try:
        import subprocess as _sp
        import re as _re
        _ps_out = _sp.run(["ps", "-eo", "pid,tty,command"], capture_output=True, text=True, timeout=1).stdout
        _candidates = []
        for _line in _ps_out.splitlines():
            _parts = _line.strip().split()
            if len(_parts) >= 3 and _parts[1] != "??" and _parts[2].endswith("codex"):
                _candidates.append(_parts[0])
        for _cpid in _candidates:
            _env_out = _sp.run(["ps", "ewww", "-p", _cpid], capture_output=True, text=True, timeout=1).stdout
            _m_pane = _re.search(r'HERDR_PANE_ID=([^\s]+)', _env_out)
            _m_pwd = _re.search(r'PWD=([^\s]+)', _env_out)
            if _m_pane:
                _c_pane = _m_pane.group(1)
                _c_pwd = _m_pwd.group(1) if _m_pwd else ""
                if cwd and _c_pwd and _c_pwd == cwd:
                    _found_pane = _c_pane
                    break
                elif len(_candidates) == 1:
                    _found_pane = _c_pane
    except Exception:
        pass

    if _found_pane:
        pane_id = _found_pane
    else:
        # Fallback to Herdr agent.list over socket if process inspection was inconclusive
        _herdr_sock = os.environ.get("HERDR_SOCKET_PATH") or os.path.expanduser("~/.config/herdr/herdr.sock")
        if os.path.exists(_herdr_sock):
            try:
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as _h:
                    _h.settimeout(0.3)
                    _h.connect(_herdr_sock)
                    _h.sendall(b'{"id":"chk","method":"agent.list","params":{}}\n')
                    _res = json.loads(_h.recv(65536).decode())
                    _agents = _res.get("result", {}).get("agents", [])
                    _matched = None
                    for _a in _agents:
                        if _a.get("agent") == "codex" and _a.get("cwd") == cwd:
                            _matched = _a.get("pane_id")
                            break
                    if _matched:
                        pane_id = _matched
                    elif pane_id:
                        _valid = any(_a.get("pane_id") == pane_id and _a.get("agent") == "codex" for _a in _agents)
                        if not _valid and _agents:
                            for _a in _agents:
                                if _a.get("agent") == "codex":
                                    pane_id = _a.get("pane_id")
                                    break
            except Exception:
                pass

status = "working"

# Cursor Agent 的事件名是 camelCase（sessionStart / beforeSubmitPrompt /
# preToolUse / postToolUse / stop / sessionEnd）；Antigravity PreInvocation 标志模型调用开始（working）。
if event_name in ("SessionStart", "session_start", "sessionStart"):
    status = "idle"
elif event_name in ("PreInvocation", "pre_invocation", "UserPromptSubmit", "user_prompt_submit", "beforeSubmitPrompt"):
    status = "working"
elif event_name in ("PreToolUse", "pre_tool_use", "preToolUse", "PostToolUse", "post_tool_use", "postToolUse"):
    status = "working"
elif event_name in ("PermissionRequest", "permission_request", "AskUserQuestion", "ask_user_question"):
    status = "blocked"
elif event_name in ("Stop", "stop", "session_idle"):
    status = "idle"
elif event_name in ("SessionEnd", "session_end", "session_shutdown", "sessionEnd"):
    status = "released"

# 1. Report to Solco local Unix socket if available (managed panes only)
if not (os.environ.get("HERDR_PANE_ID") or os.environ.get("TMUX_PANE") or pane_id):
    if agent in ("agy", "antigravity"):
        if event_name in ("PreToolUse", "pre_tool_use", "preToolUse"):
            sys.stdout.write('{"decision":"allow"}\n')
        else:
            sys.stdout.write('{}\n')
        sys.stdout.flush()
    sys.exit(0)

sock_path = os.environ.get("SOLCO_SOCKET_PATH") or os.path.expanduser("~/.solco/run/agent-status.sock")
if os.path.exists(sock_path):
    try:
        payload = {
            "agent": agent,
            "status": status,
            "pane_id": pane_id,
            "tmux_pane": os.environ.get("TMUX_PANE"),
            "herdr_pane": pane_id or os.environ.get("HERDR_PANE_ID"),
            # 缺失时发 null（不是空串）：空串曾绕过 ingest 的会话锚定
            # 校验，把全部会话写进同一个 journal 文件（审计 adv2-F1）。
            "session_id": session_id or None,
            "cwd": cwd,
            "event": event_name,
            "text": prompt_text,
        }
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
            s.settimeout(0.5)
            s.connect(sock_path)
            s.sendall((json.dumps(payload) + "\n").encode("utf-8"))
    except Exception:
        pass

# 2. 会话身份接管（v3；v6 补齐字段；v7 支持 Antigravity 路径正规化；v9 权威 source）：
# 载荷/父进程参数拿到会话 id 后，直接向 Herdr socket 报 pane.report_agent_session
# 使用各 Agent 对应的权威 source (herdr:<agent>)。
_herdr_sock = os.environ.get("HERDR_SOCKET_PATH") or os.path.expanduser("~/.config/herdr/herdr.sock")
if session_id and pane_id and os.path.exists(_herdr_sock):
    try:
        _seq = time.time_ns()
        _source = f"herdr:{agent}" if agent in ("codex", "cursor", "omp", "pi", "claude") else ("herdr:antigravity_cli" if agent in ("agy", "antigravity") else "solco")
        _params = {
            "pane_id": pane_id,
            "source": _source,
            "agent": agent,
            "seq": _seq,
            "agent_session_id": session_id,
        }
        _transcript = (
            hook_data.get("transcript_path")
            or hook_data.get("transcriptPath")
        )
        if isinstance(_transcript, str) and _transcript:
            if os.path.basename(_transcript) == "transcript.jsonl":
                _full = os.path.join(os.path.dirname(_transcript), "transcript_full.jsonl")
                if os.path.exists(_full):
                    _transcript = _full
            _params["agent_session_path"] = _transcript
        if event_name in ("SessionStart", "session_start", "sessionStart"):
            _start_source = hook_data.get("source")
            if isinstance(_start_source, str) and _start_source:
                _params["session_start_source"] = _start_source
        _req = json.dumps({
            "id": f"{_source}:{_seq}",
            "method": "pane.report_agent_session",
            "params": _params,
        })
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as h:
            h.settimeout(0.5)
            h.connect(_herdr_sock)
            h.sendall((_req + "\n").encode("utf-8"))
    except Exception:
        pass

# 3. Emit OSC 1337 escape sequence to /dev/tty for remote/tmux inline sniffing
try:
    with open("/dev/tty", "w") as tty:
        tty.write(f"\x1b]1337;AgentStatus={status};agent={agent};pane={pane_id}\x07")
        tty.flush()
except Exception:
    pass

# 4. Antigravity CLI 等要求 stdout 必须输出合法 JSON
if agent in ("agy", "antigravity"):
    if event_name in ("PreToolUse", "pre_tool_use", "preToolUse"):
        sys.stdout.write('{"decision":"allow"}\n')
    else:
        sys.stdout.write('{}\n')
    sys.stdout.flush()
PY
`
