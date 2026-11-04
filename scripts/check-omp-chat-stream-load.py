#!/usr/bin/env python3
# [INPUT]: installed omp executable and repo-owned OMP companion extension.
# [OUTPUT]: isolated official OMP v18.x extension loader smoke test; no prompt or model request.
# [POS]: reproducible SC-05 load-only check, not a real Herdr/TUI token-stream acceptance.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def main() -> int:
    omp = shutil.which("omp")
    if not omp:
        print("OMP not installed", file=sys.stderr)
        return 2
    extension = Path(__file__).with_name("omp-chat-stream.ts").resolve()
    with tempfile.TemporaryDirectory(prefix="solco-omp-extension-") as home:
        env = os.environ.copy()
        env.update(
            {
                "HOME": home,
                "HERDR_ENV": "1",
                "HERDR_PANE_ID": "solco-extension-probe",
                "SOLCO_SOCKET_PATH": str(Path(home) / "unreachable.sock"),
                # A dummy token only satisfies CLI startup validation. There
                # is no stdin request, so no provider interaction is started.
                "OPENAI_API_KEY": "solco-omp-no-model-request",
            }
        )
        cmd = [
            omp, "--mode=rpc", "--no-ui", "--no-session",
            "--no-extensions", "--no-skills", "--no-rules",
            "--no-context-files", "--model", "openai/gpt-5.2",
            "-e", str(extension), "--cwd", home,
        ]
        try:
            result = subprocess.run(
                cmd, input="", text=True, capture_output=True, env=env,
                timeout=12, check=False,
            )
        except subprocess.TimeoutExpired:
            print("OMP extension loader did not exit after stdin EOF", file=sys.stderr)
            return 1
        if result.returncode != 0:
            print(f"OMP extension loader exited {result.returncode}: {result.stderr[:600]}", file=sys.stderr)
            return 1
        messages = []
        for line in result.stdout.splitlines():
            try:
                messages.append(json.loads(line))
            except json.JSONDecodeError:
                continue
        if not any(isinstance(item, dict) and item.get("type") == "ready" for item in messages):
            print("OMP did not report RPC ready after loading the extension", file=sys.stderr)
            return 1
    print("OMP Extension load smoke: PASS (isolated, no model request)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
