#!/usr/bin/env python3
# [INPUT]: Canonical OMP Extension source, real Pi 0.80+ RPC child and loopback-only fake model.
# [OUTPUT]: Pi-owned source copy passes read tool and token lifecycle over Solco Unix Socket.
# [POS]: SC-06 Pi streaming process acceptance; no live Herdr or provider credentials.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
"""Check Pi with the same deterministic fixture as OMP, in a private HOME."""
from __future__ import annotations
from pathlib import Path
import runpy
import sys

if __name__ == "__main__":
    try:
        runpy.run_path(str(Path(__file__).with_name("check-omp-chat-stream-live.py")))["run"]("pi")
    except Exception as exc:
        print(f"Pi live extension smoke: FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
