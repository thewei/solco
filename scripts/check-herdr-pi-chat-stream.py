#!/usr/bin/env python3
# [INPUT]: Herdr CLI, Pi 0.80+ TUI, canonical Solco extension, localhost fake model.
# [OUTPUT]: real Herdr-owned Pi TUI sends live tool and token frames over one private Socket.
# [POS]: SC-06 Pi real-TUI process acceptance; never connects to the user's active Herdr/PI.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

"""Real isolated Herdr Pi TUI -> Solco Extension smoke; no real credentials.

This is intentionally different from the Pi RPC smoke: prompts go through
Herdr's actual agent.prompt and the output must appear in the original Pane.
"""
from __future__ import annotations
import http.server
import json
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

BASE = Path(__file__).resolve().parent
COMMON = runpy.run_path(str(BASE / "check-herdr-omp-chat-stream.py"))
FixtureModel = COMMON["FixtureModel"]
CHUNKS = COMMON["CHUNKS"]
run_cli = COMMON["run_cli"]
setup_env = COMMON["setup_env"]


def main() -> None:
    herdr, pi, bun = (shutil.which(n) for n in ("herdr", "pi", "bun"))
    if not all((herdr, pi, bun)):
        raise RuntimeError("requires herdr, pi and bun on PATH")
    with tempfile.TemporaryDirectory(prefix="sh-pi-", dir="/tmp") as tmp:
        root = Path(tmp)
        home, work = root / "home", root / "demo"
        home.mkdir()
        work.mkdir()
        subprocess.run(["git", "init", "-q", str(work)], check=True)
        config_dir = home / ".config/herdr"
        config_dir.mkdir(parents=True)
        config = config_dir / "config.toml"
        config.write_text(f'data_dir = "{home / ".herdr-data"}"\n')
        socket_path = home / "events.sock"
        env = setup_env(home, bun, pi, herdr, socket_path, config)
        env["PI_OFFLINE"] = "1"

        provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FixtureModel)
        provider.request_lock = threading.Lock()
        provider.request_count = 0
        provider.scratch_file = work / "fixture.txt"
        provider.scratch_file.write_text("Pi read tool was invoked\n")
        server_thread = threading.Thread(target=provider.serve_forever, daemon=True)
        server_thread.start()
        model_dir = home / ".pi/agent"
        model_dir.mkdir(parents=True)
        (model_dir / "models.json").write_text(json.dumps({
            "providers": {"smoke": {
                "baseUrl": f"http://127.0.0.1:{provider.server_address[1]}/v1",
                "api": "openai-completions", "apiKey": "local-test-only",
                "models": [{"id": "stream-smoke", "name": "Smoke",
                            "contextWindow": 8192, "maxTokens": 512, "input": ["text"],
                            "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]
            }}
        }))

        # Same single source the App embeds; Go's Pi installer applies this
        # exact one-token transformation. No user config is read or written.
        src = (BASE / "omp-chat-stream.ts").read_text()
        anchor = 'const streamAgent = "omp";'
        if src.count(anchor) != 1:
            raise RuntimeError("Pi extension source anchor drifted")
        ext = model_dir / "extensions"
        ext.mkdir()
        (ext / "solco-chat-stream.ts").write_text(src.replace(anchor, 'const streamAgent = "pi";'))

        events: list[dict] = []
        stopping = threading.Event()
        lock = threading.Lock()
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(str(socket_path))
        listener.listen(12)
        listener.settimeout(.2)

        def receive() -> None:
            while not stopping.is_set():
                try:
                    conn, _ = listener.accept()
                except socket.timeout:
                    continue
                except OSError:
                    return
                with conn, conn.makefile("rb") as rows:
                    for row in rows:
                        try:
                            frame = json.loads(row)
                        except ValueError:
                            continue
                        with lock:
                            events.append(frame)

        receiver = threading.Thread(target=receive, daemon=True)
        receiver.start()
        daemon: subprocess.Popen[str] | None = None
        try:
            log = (root / "herdr.log").open("w")
            daemon = subprocess.Popen([herdr, "server"], cwd=work, env=env,
                                      stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
                                      text=True)
            log.close()
            sock = config_dir / "herdr.sock"
            limit = time.monotonic() + 8
            while not sock.exists() and time.monotonic() < limit and daemon.poll() is None:
                time.sleep(.1)
            if not sock.exists() or daemon.poll() is not None:
                raise RuntimeError("private Herdr daemon never became ready")
            created = run_cli([herdr, "workspace", "create", "--cwd", str(work),
                               "--env", "PATH=" + env["PATH"], "--no-focus"], env=env, cwd=work)
            pane = created["root_pane"]["pane_id"]
            started = run_cli([herdr, "agent", "start", "pi-smoke", "--kind", "pi", "--pane", pane,
                               "--timeout", "20000", "--", "--model", "smoke/stream-smoke",
                               "--no-skills", "--no-context-files", "--tools", "read",
                               "--offline"], env=env, cwd=work, timeout=24)
            if started.get("agent", {}).get("pane_id") != pane:
                raise RuntimeError("Herdr did not start one Pi TUI in the expected Pane")

            def wait_idle() -> None:
                end = time.monotonic() + 12
                while time.monotonic() < end:
                    state = run_cli([herdr, "agent", "get", pane], env=env, cwd=work)
                    agent = state.get("agent", {})
                    if agent.get("agent") == "pi" and agent.get("agent_status") == "idle" and agent.get("interactive_ready"):
                        return
                    time.sleep(.2)
                raise RuntimeError("Herdr did not confirm Pi idle")

            def prompt(message: str) -> None:
                wait_idle()
                receipt = run_cli([herdr, "agent", "prompt", pane, message], env=env, cwd=work)
                if receipt.get("type") != "agent_prompted" or receipt.get("agent", {}).get("pane_id") != pane:
                    raise RuntimeError("Herdr did not accept the Pi prompt for the current Pane")

            def wait_frames(ended: int, chunks: int) -> None:
                end = time.monotonic() + 27
                while time.monotonic() < end:
                    with lock:
                        ks = [f.get("params", {}).get("kind") for f in events if f.get("method") == "chat.stream"]
                    if ks.count("message_end") >= ended and ks.count("text_delta") >= chunks:
                        return
                    time.sleep(.04)
                raise RuntimeError(f"Pi stream incomplete: got {ks}")

            prompt("Read the test file, then say hello.")
            wait_frames(2, len(CHUNKS))
            prompt("Say hello again.")
            wait_frames(3, len(CHUNKS) * 2)

            with lock:
                frames = [f["params"] for f in events if f.get("method") == "chat.stream"]
            kinds = [f["kind"] for f in frames]
            expected = (["message_start", "message_end", "tool_start", "tool_end", "message_start"]
                        + ["text_delta"] * 3 + ["message_end", "message_start"]
                        + ["text_delta"] * 3 + ["message_end"])
            if kinds != expected:
                raise RuntimeError(f"Pi event lifecycle mismatch: {kinds}")
            if [f["delta"] for f in frames if f["kind"] == "text_delta"] != list(CHUNKS) * 2:
                raise RuntimeError("Pi event text was truncated or duplicated")
            if [f["seq"] for f in frames] != list(range(1, len(frames) + 1)):
                raise RuntimeError("Pi companion dropped an event sequence")
            if any(f["agent"] != "pi" or f["pane_id"] != pane for f in frames):
                raise RuntimeError("Pi event identity crossed a provider/Pane")
            if len({f["epoch"] for f in frames}) != 1 or len({f["session_path"] for f in frames}) != 1:
                raise RuntimeError("Pi event switched process/session identity")
            if not Path(frames[0]["session_path"]).resolve().is_relative_to(home.resolve()) or not Path(frames[0]["session_path"]).is_file():
                raise RuntimeError("Pi session is not the private real JSONL")
            if frames[2]["tool_call_id"] != frames[3]["tool_call_id"] or frames[2]["tool_name"] != "read":
                raise RuntimeError("Pi read tool was not executed")
            agents = run_cli([herdr, "agent", "list"], env=env, cwd=work).get("agents", [])
            if len(agents) != 1 or agents[0].get("pane_id") != pane:
                raise RuntimeError(f"unexpected number of Pi agents: {len(agents)}")
            rendered = subprocess.run([herdr, "pane", "read", pane, "--lines", "60"],
                                      cwd=work, env=env, capture_output=True, text=True, timeout=8)
            if rendered.returncode or "Hello there!" not in rendered.stdout:
                raise RuntimeError("original Herdr Pi TUI did not show the streamed answer")
            if provider.request_count < 3:
                raise RuntimeError("the local fake model was not called by both Pi prompts")
            print(f"Herdr Pi stream: PASS (1 Pane/Agent, 2 prompts, {len(frames)} events, "
                  f"{2*len(CHUNKS)} deltas, real read tool + original TUI)")
        finally:
            stopping.set()
            listener.close()
            receiver.join(timeout=1)
            try:
                subprocess.run([herdr, "server", "stop"], cwd=work, env=env, capture_output=True, timeout=6)
            except (OSError, subprocess.TimeoutExpired):
                pass
            if daemon is not None:
                try:
                    daemon.wait(timeout=4)
                except subprocess.TimeoutExpired:
                    daemon.kill()
                    daemon.wait(timeout=3)
            provider.shutdown()
            provider.server_close()
            server_thread.join(timeout=1)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"Herdr Pi stream: FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
