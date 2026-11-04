#!/usr/bin/env python3
# [INPUT]: real Herdr CLI/daemon, OMP CLI and Solco extension; local SSE model fixture.
# [OUTPUT]: isolated Herdr Pane -> OMP TUI -> extension -> Unix Socket acceptance proof.
# [POS]: SC-05 real Herdr process boundary smoke; never attaches to or alters the user's live Herdr.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

"""Run with python3 scripts/check-herdr-omp-chat-stream.py on macOS.

Creates a *separate* Herdr daemon under short /tmp HOME (Unix socket path
limit), starts one OMP TUI in its scratch pane and sends one prompt through
Herdr. OMP calls a loopback OpenAI-compatible SSE fixture (no paid API), reads
a scratch file, and emits token/tool frames to a private local socket.

The test does not control Solco's own WebView; manual app UI verification
remains a separate acceptance gate.
"""

from __future__ import annotations

import json
import re
import os
from pathlib import Path
import runpy
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import http.server
from typing import Any

HERE = Path(__file__).resolve().parent
SMOKE = runpy.run_path(str(HERE / "check-omp-chat-stream-live.py"))
FixtureModel = SMOKE["FixtureModel"]
CHUNKS = SMOKE["CHUNKS"]
TIMEOUT = 28


def compatible_bun_version(binary: Path) -> bool:
    if not binary.is_file():
        return False
    try:
        result = subprocess.run([str(binary), "--version"], capture_output=True,
                                text=True, timeout=4)
    except (OSError, subprocess.TimeoutExpired):
        return False
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)(?:[-+].*)?", result.stdout.strip())
    return result.returncode == 0 and match is not None and \
        tuple(map(int, match.groups())) >= (1, 4, 4)


def run_cli(cmd: list[str], *, env: dict[str, str], cwd: Path, timeout: float = 12) -> dict[str, Any]:
    result = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{Path(cmd[0]).name} {cmd[1:3]} exit {result.returncode}: "
                           f"{(result.stdout + result.stderr)[:450]}")
    if cmd[0] == env["HERDR_BIN"]:
        try:
            result_obj = json.loads(result.stdout)
        except ValueError as exc:
            raise RuntimeError(f"invalid Herdr response to {cmd[1:3]}") from exc
        if "error" in result_obj:
            raise RuntimeError(f"Herdr rejected {cmd[1:3]}: {result_obj['error'].get('code')}")
        return result_obj.get("result", {})
    return {}


def setup_env(home: Path, bun: str, omp: str, herdr: str, socket_path: Path, config: Path) -> dict[str, str]:
    env = os.environ.copy()
    for name in tuple(env):
        if (name.startswith("HERDR_") or name.endswith("_API_KEY") or name.endswith("_AUTH_TOKEN")
            or name.endswith("_PROXY") or name.startswith("AWS_") or name.startswith("GOOGLE_")
            or name in ("SSH_AUTH_SOCK", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN",
                        "GIT_ASKPASS", "OPENAI_ORG_ID", "NODE_OPTIONS")):
            env.pop(name, None)

    # Herdr creates a login zsh in the Pane; it can reset PATH and select an
    # old Homebrew Bun (the installed OMP requires >=1.4.4). Only the sandbox
    # shell initialization is changed.
    # Prefer OMP's own sibling Bun over wrappers in the calling developer
    # shell: Herdr's process detector keys on the actual foreground process.
    sibling_bun = Path(omp).parent / "bun"
    selected_bun = sibling_bun if compatible_bun_version(sibling_bun) else Path(bun)
    if not compatible_bun_version(selected_bun):
        raise RuntimeError("OMP requires Bun >=1.4.4; no compatible Bun was found")
    binary_path = ":".join(dict.fromkeys((str(selected_bun.parent), str(Path(omp).parent),
                                         str(Path(herdr).parent))))
    for filename in (".zshenv", ".zshrc", ".zprofile"):
        (home / filename).write_text(f'export PATH="{binary_path}:$PATH"\n', encoding="utf-8")
    env.update({
        "HOME": str(home),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_CACHE_HOME": str(home / ".cache"),
        "XDG_DATA_HOME": str(home / ".local/share"),
        "HERDR_CONFIG_PATH": str(config),
        "HERDR_BIN": herdr,
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_CONFIG_NOSYSTEM": "1",
        "SOLCO_SOCKET_PATH": str(socket_path),
        "NO_PROXY": "127.0.0.1,localhost",
        "OPENAI_API_KEY": "local-scratch-only",
    })
    env["PATH"] = binary_path + ":" + env.get("PATH", "")
    return env


def check_events(frames: list[dict[str, Any]], pane: str, home: Path) -> None:
    events = [f.get("params", {}) for f in frames if f.get("method") == "chat.stream"]
    kinds = [e.get("kind") for e in events]
    expected = [
        "message_start", "message_end", "tool_start", "tool_end",
        "message_start", *("text_delta" for _ in CHUNKS), "message_end",
        "message_start", *("text_delta" for _ in CHUNKS), "message_end",
    ]
    if kinds != expected:
        raise RuntimeError(f"Herdr TUI event lifecycle mismatch: {kinds}")
    if [e.get("delta") for e in events if e.get("kind") == "text_delta"] != list(CHUNKS) * 2:
        raise RuntimeError("Herdr TUI stream lost/duplicated token deltas")
    if [e.get("seq") for e in events] != list(range(1, len(events) + 1)):
        raise RuntimeError("Herdr TUI companion sequence has gaps")
    if len({e.get("epoch") for e in events}) != 1:
        raise RuntimeError("OMP changed its extension process epoch")
    if any(e.get("agent") != "omp" or e.get("pane_id") != pane for e in events):
        raise RuntimeError("OMP event identity crossed a Pane/Agent")
    if len({e.get("session_path") for e in events}) != 1:
        raise RuntimeError("OMP event session changed during the same prompt")
    source = Path(events[0]["session_path"]).resolve()
    if not source.is_file() or not source.is_relative_to(home.resolve()):
        raise RuntimeError("OMP session file is not a real sandbox transcript")
    if (events[2].get("tool_call_id") != events[3].get("tool_call_id")
            or events[2].get("tool_name") != "read"
            or events[3].get("tool_name") != "read"
            or events[3].get("tool_error") is True):
        raise RuntimeError("OMP tool lifecycle lost its call identity")
    if len({events[i].get("message_id") for i in (0, 4, 9)}) != 3:
        raise RuntimeError("OMP did not separate the tool, reply and next-turn messages")


def main() -> None:
    herdr = shutil.which("herdr")
    omp = shutil.which("omp")
    bun = shutil.which("bun")
    if not herdr or not omp or not bun:
        raise RuntimeError("herdr, omp and bun must already be installed on this Mac")

    # Never build a long path under $TMPDIR on macOS: sockaddr_un has a
    # short 104-byte path limit. /tmp is /private/tmp with a predictable length.
    with tempfile.TemporaryDirectory(prefix="sh-", dir="/tmp") as base:
        root = Path(base)
        home, work = root / "home", root / "demo"
        home.mkdir()
        work.mkdir()
        subprocess.run(["git", "init", "-q", str(work)], check=True, capture_output=True)

        config_dir = home / ".config/herdr"
        config_dir.mkdir(parents=True)
        config = config_dir / "config.toml"
        config.write_text(f'data_dir = "{home / ".herdr-data"}"\n', encoding="utf-8")
        event_socket = home / "events.sock"
        env = setup_env(home, bun, omp, herdr, event_socket, config)

        provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FixtureModel)
        provider.request_lock = threading.Lock()
        provider.request_count = 0
        provider.scratch_file = work / "fixture.txt"
        provider.scratch_file.write_text("Herdr and OMP are in one sandbox.\n", encoding="utf-8")
        provider_thread = threading.Thread(target=provider.serve_forever, daemon=True)
        provider_thread.start()

        agent_dir = home / ".omp/agent"
        agent_dir.mkdir(parents=True)
        (agent_dir / "models.json").write_text(json.dumps({
            "providers": {"smoke": {
                "baseUrl": f"http://127.0.0.1:{provider.server_address[1]}/v1",
                "api": "openai-completions",
                "apiKey": "local-scratch-only",
                "models": [{
                    "id": "stream-smoke", "name": "Stream Smoke",
                    "contextWindow": 8192, "maxTokens": 512, "input": ["text"],
                    "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
                }],
            }},
        }), encoding="utf-8")

        stop = threading.Event()
        frames: list[dict[str, Any]] = []
        lock = threading.Lock()
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(str(event_socket))
        listener.listen(12)
        listener.settimeout(.2)

        def receive() -> None:
            while not stop.is_set():
                try:
                    client, _ = listener.accept()
                except socket.timeout:
                    continue
                except OSError:
                    return
                with client, client.makefile("rb") as lines:
                    for line in lines:
                        try:
                            frame = json.loads(line)
                        except ValueError:
                            continue
                        with lock:
                            frames.append(frame)
        receiver = threading.Thread(target=receive, daemon=True)
        receiver.start()

        daemon: subprocess.Popen[str] | None = None
        try:
            subprocess.run([str(HERE / "install-omp-chat-stream.sh"), "--install"],
                           env=env, cwd=work, check=True, capture_output=True, timeout=6)
            # A fresh HOME otherwise opens the interactive first-run sign-in
            # wizard: agent.prompt would type into its search box, not the model.
            run_cli([omp, "config", "set", "startup.setupWizard", "false"],
                    env=env, cwd=work)

            log = (root / "herdr.log").open("w")
            daemon = subprocess.Popen([herdr, "server"], cwd=work, env=env, stdout=log,
                                      stderr=subprocess.STDOUT, start_new_session=True, text=True)
            log.close()
            sock = config_dir / "herdr.sock"
            deadline = time.monotonic() + 8
            while not sock.exists() and time.monotonic() < deadline and daemon.poll() is None:
                time.sleep(.1)
            if daemon.poll() is not None or not sock.exists():
                raise RuntimeError("sandbox Herdr server did not start")

            created = run_cli([herdr, "workspace", "create", "--cwd", str(work),
                               "--env", "PATH=" + env["PATH"], "--no-focus"],
                              env=env, cwd=work)
            pane = created["root_pane"]["pane_id"]
            try:
                started = run_cli([
                    herdr, "agent", "start", "solcosmoke", "--kind", "omp", "--pane", pane,
                    "--timeout", "18000", "--",
                    "--model=smoke/stream-smoke", "--no-skills", "--no-context-files", "--tools=read",
                ], env=env, cwd=work, timeout=21)
            except RuntimeError as exc:
                # This is our synthetic Pane, so its output is safe to cite.
                view = subprocess.run([herdr, "pane", "read", pane, "--lines", "40"],
                                      env=env, cwd=work, capture_output=True, text=True, timeout=6)
                found = subprocess.run([herdr, "agent", "list"], env=env, cwd=work,
                                       capture_output=True, text=True, timeout=6)
                raise RuntimeError(f"{exc}; sandbox Pane: {view.stdout[-420:]}; "
                                   f"Herdr Agents: {found.stdout[-400:]}") from exc
            if started.get("agent", {}).get("pane_id") != pane:
                raise RuntimeError("Herdr did not start OMP in the existing Pane")

            # Do not use --wait: a very fast local model can finish between
            # Herdr status samples, yielding agent_prompt_stalled after a
            # successful submission. The Pane and Extension evidence are
            # authoritative here.
            run_cli([herdr, "agent", "prompt", pane, "Read the test file, then say hello."],
                    env=env, cwd=work)

            def await_turns(message_ends: int, text_chunks: int) -> None:
                deadline = time.monotonic() + TIMEOUT
                while time.monotonic() < deadline:
                    with lock:
                        kinds = [f.get("params", {}).get("kind") for f in frames]
                        complete = (kinds.count("message_end") >= message_ends
                                    and kinds.count("text_delta") >= text_chunks)
                    if complete:
                        return
                    time.sleep(.04)
                raise RuntimeError(f"timed out waiting for {message_ends} assistant completions")

            await_turns(2, len(CHUNKS))
            # Reuse the same real Herdr Pane/OMP process for another prompt.
            # The extension sequence must remain contiguous and identity
            # cannot jump to a second runtime or new session file.
            run_cli([herdr, "agent", "prompt", pane, "Please answer one more time."],
                    env=env, cwd=work)
            await_turns(3, len(CHUNKS) * 2)
            with lock:
                recorded = list(frames)
            check_events(recorded, pane, home)

            # Verify the same process is still the *only* Herdr-recognized
            # agent, not a separately started RPC helper.
            agents = run_cli([herdr, "agent", "list"], env=env, cwd=work).get("agents", [])
            if len(agents) != 1 or agents[0].get("pane_id") != pane:
                raise RuntimeError(f"expected exactly one running OMP in Herdr sandbox, got {len(agents)}")
            read = subprocess.run([herdr, "pane", "read", pane, "--lines", "60"],
                                  env=env, cwd=work, capture_output=True, text=True, timeout=8)
            if read.returncode or "Hello there!" not in read.stdout:
                raise RuntimeError("the original Herdr TUI Pane did not display the streamed final answer")
            if provider.request_count < 2:
                raise RuntimeError("the real OMP process did not complete the tool round-trip")

            print(f"Herdr OMP Chat stream: PASS (1 Pane/1 Agent, 2 prompts; {len(recorded)} IPC frames; "
                  f"{len(CHUNKS) * 2} deltas; read tool + TUI answer)")
        finally:
            if daemon is not None and daemon.poll() is None:
                try:
                    subprocess.run([herdr, "server", "stop"], env=env, cwd=work,
                                   capture_output=True, timeout=5)
                except (subprocess.TimeoutExpired, OSError):
                    pass
                try:
                    daemon.wait(timeout=4)
                except subprocess.TimeoutExpired:
                    daemon.terminate()
                    try:
                        daemon.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        daemon.kill()
                        daemon.wait(timeout=3)
            stop.set()
            listener.close()
            receiver.join(timeout=1)
            provider.shutdown()
            provider.server_close()
            provider_thread.join(timeout=1)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"Herdr OMP Chat stream: FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
