#!/usr/bin/env python3
# [INPUT]: solco-hostd command, temp HOME/config, private device token and Herdr daemon.
# [OUTPUT]: real isolated resident Host TLS/bootstrap + AppHook Socket ownership + shutdown acceptance.
# [POS]: SC-06 standalone resident-process smoke; no launchd registration or real user session.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
"""Validate a *real* solco-hostd binary and TLS API without touching live services.

Builds the resident executable into /tmp, starts private Herdr and remote
access on 127.0.0.1, and authenticates a one-time scratch token. Certificates,
tokens, sockets and settings are deleted on exit. No launchd job is modified.
"""
from __future__ import annotations
import datetime
import hashlib
import http.server
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def main() -> None:
    herdr = shutil.which("herdr")
    if not herdr:
        raise RuntimeError("Herdr CLI not installed")
    repo = Path(__file__).resolve().parent.parent
    with tempfile.TemporaryDirectory(prefix="sh-hostd-", dir="/tmp") as temp:
        root = Path(temp)
        home = root / "home"
        home.mkdir()
        data = root / "data"
        data.mkdir()
        remote = data / "RemoteHost"
        remote.mkdir()
        cfg_dir = home / ".config/herdr"
        cfg_dir.mkdir(parents=True)
        cfg = cfg_dir / "config.toml"
        cfg.write_text(f'data_dir = "{home / ".herdr-data"}"\n')
        tcp_port = port()
        (data / "settings.json").write_text(json.dumps({
            "schema_version": 1,
            "remote_access": {"mode": "loopback", "port": tcp_port}
        }))
        token = secrets.token_urlsafe(32)
        date = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
        (remote / "devices.json").write_text(json.dumps({"devices":[{
            "id":"dev_1234567890abcdef","name":"Pi-Sandbox",
            "tokenHash":hashlib.sha256(token.encode()).hexdigest(),
            "createdAt":date,"lastSeenAt":date
        }]}))
        os.chmod(remote / "devices.json", 0o600)

        exe = root / "solco-hostd"
        subprocess.run(["go", "build", "-o", str(exe), "./cmd/solco-hostd"],
                       cwd=repo, capture_output=True, check=True, timeout=90)
        env = os.environ.copy()
        for k in tuple(env):
            if k.startswith("HERDR_") or k.endswith("_PROXY") or k in ("SSH_AUTH_SOCK",):
                env.pop(k, None)
        env.update(HOME=str(home),HERDR_CONFIG_PATH=str(cfg),
                   XDG_CONFIG_HOME=str(home / ".config"),
                   SOLCO_SOCKET_PATH=str(home / "stream.sock"),
                   NO_PROXY="127.0.0.1,localhost")

        server: subprocess.Popen[str] | None = None
        hostd: subprocess.Popen[str] | None = None
        try:
            log = (root / "herdr.log").open("w")
            server = subprocess.Popen([herdr, "server"], env=env, stdout=log,
                                      stderr=subprocess.STDOUT, start_new_session=True, text=True)
            log.close()
            herdr_sock = cfg_dir / "herdr.sock"
            deadline = time.monotonic() + 9
            while not herdr_sock.exists() and time.monotonic()<deadline and server.poll() is None:
                time.sleep(.1)
            if not herdr_sock.exists():
                raise RuntimeError("private Herdr daemon failed to start")

            log = (root / "hostd.log").open("w")
            hostd = subprocess.Popen([str(exe), "run", "-data", str(data)], env=env,
                                     stdout=log, stderr=subprocess.STDOUT, start_new_session=True, text=True)
            log.close()
            url = f"https://127.0.0.1:{tcp_port}/api/v3/bootstrap"
            # Only the local disposable self-signed smoke cert is trusted here.
            tls = ssl._create_unverified_context()
            deadline = time.monotonic() + 12
            response: dict | None = None
            while time.monotonic() < deadline:
                if hostd.poll() is not None:
                    raise RuntimeError("resident Host process exited before listening")
                try:
                    request = urllib.request.Request(url, headers={"Authorization":"Bearer "+token})
                    with urllib.request.urlopen(request, context=tls, timeout=1) as result:
                        response=json.load(result)
                    break
                except (OSError,urllib.error.URLError, TimeoutError):
                    time.sleep(.1)
            if response is None:
                raise RuntimeError("resident Host did not serve authorized bootstrap")
            if response.get("capabilities", {}).get("chat") is not True:
                raise RuntimeError("resident Host did not advertise its read-only Chat")
            sock = home / "stream.sock"
            if not sock.exists():
                raise RuntimeError("resident Host did not bind private AppHook socket")

            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as peer:
                peer.settimeout(2)
                peer.connect(str(sock))
                event = {"method":"chat.stream","id":"smoke","params":{
                    "agent":"pi","pane_id":"scratch-p","session_path":str(root/"pi.jsonl"),
                    "epoch":"00001-scratch","seq":1,"kind":"message_start","message_id":"m1"
                }}
                peer.sendall((json.dumps(event)+"\n").encode())
                answer = peer.makefile("rb").readline()
            if json.loads(answer).get("result",{}).get("ok") is not True:
                raise RuntimeError("resident AppHook refused valid Pi stream event")

            print("Resident Host process: PASS (isolated Herdr, TLS auth/bootstrap, "
                  "Pi AppHook IPC, one ownership socket)")
        finally:
            if hostd is not None and hostd.poll() is None:
                hostd.terminate()
                try: hostd.wait(timeout=6)
                except subprocess.TimeoutExpired:
                    hostd.kill()
                    hostd.wait(timeout=3)
            if server is not None and server.poll() is None:
                subprocess.run([herdr,"server","stop"],env=env,capture_output=True,timeout=5)
                try: server.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=3)
            if (home / "stream.sock").exists():
                raise RuntimeError("resident process left its private AppHook socket behind")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"Resident Host process: FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
