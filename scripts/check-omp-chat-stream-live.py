#!/usr/bin/env python3
# [INPUT]: the locally installed OMP/Pi CLI, repo-owned extension, local fake OpenAI SSE provider.
# [OUTPUT]: assert real OMP/Pi assistant text deltas + real read tool events reach a Solco-format Unix socket.
# [POS]: repeatable SC-05/SC-06 isolated agent runtime acceptance; no Herdr pane, live user session or paid API.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

"""Run with `python3 scripts/check-omp-chat-stream-live.py`.

A real OMP child receives one prompt and executes one read of a scratch file,
but the model API is an in-process loopback SSE fixture. This checks genuine
OMP 18.x event callbacks and the companion extension's IPC frames; it does
NOT claim to prove Herdr's TUI or a live model's behavior.
"""

from __future__ import annotations

import http.server
import json
import os
from pathlib import Path
import queue
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any

PROMPT = "Read the test file, then say hello."
CHUNKS = ("Hello", " there", "!")
PANE = "solco-isolated-pane"
WAIT_SECONDS = 24


class FixtureModel(http.server.BaseHTTPRequestHandler):
    """Only answers a fixed OpenAI-compatible streaming fixture over loopback."""

    def do_POST(self) -> None:
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 150_000:
                raise ValueError("unexpected prompt payload size")
            request = json.loads(self.rfile.read(length))
        except (ValueError, json.JSONDecodeError):
            self.send_error(400)
            return
        if request.get("model") != "stream-smoke":
            self.send_error(400)
            return
        with self.server.request_lock:
            self.server.request_count += 1
            first = self.server.request_count == 1

        if first:
            delta_parts: list[dict[str, Any]] = [
                {
                    "role": "assistant",
                    "tool_calls": [{
                        "index": 0,
                        "id": "call_solco_smoke",
                        "type": "function",
                        "function": {
                            "name": "read",
                            "arguments": json.dumps({"path": str(self.server.scratch_file)}),
                        },
                    }],
                }
            ]
            finish_reason = "tool_calls"
        else:
            delta_parts = [{"role": "assistant", "content": text} for text in CHUNKS]
            finish_reason = "stop"

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for delta in delta_parts:
            self._chunk(delta, None)
            time.sleep(0.055)
        self._chunk({}, finish_reason)
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def _chunk(self, delta: dict[str, Any], finish: str | None) -> None:
        payload = {
            "id": "chatcmpl-solco",
            "object": "chat.completion.chunk",
            "created": 1,
            "model": "stream-smoke",
            "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
        }
        self.wfile.write(("data: " + json.dumps(payload) + "\n\n").encode())
        self.wfile.flush()

    def log_message(self, *args: Any) -> None:
        pass


def run(agent: str = "omp") -> None:
    if agent not in ("omp", "pi"):
        raise RuntimeError("unsupported stream smoke agent")
    executable = shutil.which(agent)
    if not executable:
        raise RuntimeError(f"{agent} binary is missing")
    extension = Path(__file__).with_name("omp-chat-stream.ts").resolve()
    with tempfile.TemporaryDirectory(prefix=f"sc-{agent}-") as root:
        if agent == "pi":
            # The same audited source: only the explicit agent identifier is
            # compiled for Pi. Neither user extensions folder is touched.
            source = extension.read_text(encoding="utf-8")
            anchor = 'const streamAgent = "omp";'
            if source.count(anchor) != 1:
                raise RuntimeError("canonical extension source identifier drifted")
            extension = Path(root) / "solco-pi-chat-stream.ts"
            extension.write_text(source.replace(anchor, 'const streamAgent = "pi";'), encoding="utf-8")
        home = Path(root)
        scratch = home / "fixture.txt"
        scratch.write_text("The read tool worked.\n", encoding="utf-8")
        model = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FixtureModel)
        model.request_lock = threading.Lock()
        model.request_count = 0
        model.scratch_file = scratch
        model_thread = threading.Thread(target=model.serve_forever, daemon=True)
        model_thread.start()

        agent_dir = home / f".{agent}" / "agent"
        agent_dir.mkdir(parents=True)
        (agent_dir / "models.json").write_text(json.dumps({
            "providers": {
                "smoke": {
                    "baseUrl": f"http://127.0.0.1:{model.server_address[1]}/v1",
                    "api": "openai-completions",
                    "apiKey": "fake-local-only",
                    "models": [{
                        "id": "stream-smoke",
                        "name": "Stream Smoke",
                        "contextWindow": 8192,
                        "maxTokens": 512,
                        "input": ["text"],
                        "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
                    }],
                },
            },
        }), encoding="utf-8")

        sock_path = home / "events.sock"
        socket_server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        socket_server.bind(str(sock_path))
        socket_server.listen(16)
        socket_server.settimeout(0.2)
        frames: list[dict[str, Any]] = []
        frames_lock = threading.Lock()
        quit_event = threading.Event()

        def consume_frames() -> None:
            while not quit_event.is_set():
                try:
                    client, _ = socket_server.accept()
                except socket.timeout:
                    continue
                except OSError:
                    return
                with client:
                    with client.makefile("rb") as lines:
                        for line in lines:
                            try:
                                frame = json.loads(line)
                            except json.JSONDecodeError:
                                continue
                            with frames_lock:
                                frames.append(frame)

        frame_thread = threading.Thread(target=consume_frames, daemon=True)
        frame_thread.start()

        env = os.environ.copy()
        env.update({
            "HOME": str(home),
            "XDG_CONFIG_HOME": str(home / ".config"),
            "XDG_CACHE_HOME": str(home / ".cache"),
            "XDG_DATA_HOME": str(home / ".local" / "share"),
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "HERDR_ENV": "1",
            "HERDR_PANE_ID": PANE,
            "SOLCO_SOCKET_PATH": str(sock_path),
            "NO_PROXY": "127.0.0.1,localhost",
        })
        # Do not allow an inherited real provider key or proxy to leak into
        # an isolated test. All model traffic goes to our loopback endpoint.
        for key in tuple(env):
            if key.endswith("_API_KEY") or key.endswith("_AUTH_TOKEN") or key.endswith("_PROXY") or \
                    key.startswith("AWS_") or key.startswith("GOOGLE_APPLICATION_CREDENTIALS") or \
                    key in ("SSH_AUTH_SOCK", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "OMPCODE",
                            "HERDR_SOCKET_PATH", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "GIT_ASKPASS"):
                env.pop(key, None)
        env["NO_PROXY"] = "127.0.0.1,localhost"
        env["OPENAI_API_KEY"] = "fake-local-only"

        if agent == "omp":
            command = [
                executable, "--mode=rpc", "--no-ui", "--no-skills", "--no-rules",
                "--no-context-files", "--no-pty", "--no-title", "--tools=read",
                "--no-extensions", "--model=smoke/stream-smoke",
                "-e", str(extension), "--cwd", str(home),
            ]
        else:
            # Pi's CLI rejects --mode=x; Pi RPC also sends no 'ready' record.
            command = [
                executable, "--mode", "rpc", "--no-skills", "--no-context-files",
                "--tools", "read", "--no-extensions", "--model", "smoke/stream-smoke",
                "-e", str(extension),
            ]
        output: queue.Queue[dict[str, Any]] = queue.Queue()
        process: subprocess.Popen[str] | None = None
        errors: list[str] = []
        try:
            process = subprocess.Popen(
                command, env=env, stdin=subprocess.PIPE,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, bufsize=1,
            )
            assert process.stdout is not None
            assert process.stdin is not None
            assert process.stderr is not None

            def read_stdout() -> None:
                for line in process.stdout:
                    try:
                        data = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if isinstance(data, dict):
                        output.put(data)

            stderr_output: list[str] = []
            def read_stderr() -> None:
                for line in process.stderr:
                    stderr_output.append(line[:300])

            threading.Thread(target=read_stdout, daemon=True).start()
            threading.Thread(target=read_stderr, daemon=True).start()
            deadline = time.monotonic() + WAIT_SECONDS

            def next_event() -> dict[str, Any]:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise RuntimeError("OMP fixture timed out")
                try:
                    return output.get(timeout=remaining)
                except queue.Empty as exc:
                    raise RuntimeError("OMP fixture timed out waiting for events") from exc

            if agent == "omp":
                while next_event().get("type") != "ready":
                    if process.poll() is not None:
                        raise RuntimeError(f"OMP exited before ready: {stderr_output[:2]}")
            process.stdin.write(json.dumps({"id": "smoke", "type": "prompt", "message": PROMPT}) + "\n")
            process.stdin.flush()

            completed = False
            while time.monotonic() < deadline:
                event = next_event()
                if event.get("type") == "agent_end":
                    completed = True
                    break
                if event.get("type") == "response" and event.get("command") == "prompt" and not event.get("success"):
                    raise RuntimeError(f"OMP rejected the fixture prompt: {event.get('error')}")
            if not completed:
                raise RuntimeError("OMP never emitted agent_end")
            # Let the non-blocking companion flush its final queued frame.
            while time.monotonic() < deadline:
                with frames_lock:
                    if sum(f.get("params", {}).get("kind") == "message_end" for f in frames) >= 2 and \
                        sum(f.get("params", {}).get("kind") == "text_delta" for f in frames) >= len(CHUNKS) and \
                        sum(f.get("params", {}).get("kind") == "agent_end" for f in frames) >= 1:
                        break
                time.sleep(0.02)
            with frames_lock:
                events = [f.get("params", {}) for f in frames if f.get("method") == "chat.stream"]

            kinds = [e.get("kind") for e in events]
            expected_kinds = [
                "message_start", "message_end", "tool_start", "tool_end",
                "message_start", "text_delta", "text_delta", "text_delta", "message_end", "agent_end",
            ]
            if kinds != expected_kinds:
                raise RuntimeError(f"unexpected real OMP event lifecycle: {kinds} (want {expected_kinds})")
            text_deltas = [e.get("delta") for e in events if e.get("kind") == "text_delta"]
            if text_deltas != list(CHUNKS):
                raise RuntimeError("OMP real text deltas differ from loopback model fixture")
            if model.request_count != 2:
                raise RuntimeError(f"OMP did not finish its read-tool cycle: {model.request_count} calls")
            tool_start, tool_end = events[2], events[3]
            if (tool_start.get("tool_call_id") != "call_solco_smoke"
                or tool_end.get("tool_call_id") != tool_start.get("tool_call_id")
                or tool_start.get("tool_name") != "read"
                or tool_end.get("tool_name") != "read"
                or tool_end.get("tool_error") is True):
                raise RuntimeError("real OMP tool lifecycle lost its identity or success state")
            if (events[0].get("message_id") != tool_start.get("message_id")
                or events[1].get("message_id") != tool_end.get("message_id")
                or events[4].get("message_id") == events[0].get("message_id")
                or any(e.get("message_id") != events[4].get("message_id") for e in events[5:])):
                raise RuntimeError("OMP tool and reply events crossed assistant message boundaries")
            if len({e.get("epoch") for e in events}) != 1:
                raise RuntimeError("OMP stream epoch changed within one process")
            if any(e.get("pane_id") != PANE or e.get("agent") != agent for e in events):
                raise RuntimeError("OMP extension leaked frames into a different Pane/Agent")
            if [e.get("seq") for e in events] != list(range(1, len(events) + 1)):
                raise RuntimeError("OMP extension dropped or reordered its own frames")
            if len(set(e.get("session_path") for e in events)) != 1:
                raise RuntimeError("OMP extension changed session identity within one turn")
            if any(not Path(e["session_path"]).is_file() for e in events):
                raise RuntimeError("OMP session path does not point to a real source file")
            if any(not str(e["session_path"]).startswith(str(home)) for e in events):
                raise RuntimeError("OMP test accessed a session outside isolated HOME")
            print(f"{agent.upper()} live extension smoke: PASS ({len(events)} frames, read tool + {len(text_deltas)} text deltas)")
        except Exception as exc:
            errors.append(str(exc))
        finally:
            if process is not None:
                if process.poll() is None:
                    process.terminate()
                try:
                    process.communicate(timeout=3)
                except (subprocess.TimeoutExpired, ValueError):
                    process.kill()
                    process.wait(timeout=3)
            quit_event.set()
            socket_server.close()
            model.shutdown()
            model.server_close()
            frame_thread.join(timeout=1)
            model_thread.join(timeout=1)
        if errors:
            raise RuntimeError(errors[0])


if __name__ == "__main__":
    try:
        run()
    except Exception as exc:
        print(f"OMP live extension smoke: FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
