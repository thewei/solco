/**
 * [INPUT]: omp-chat-stream.ts 的非阻塞 Extension observer、测试专用 Unix socket 和伪 OMP sessionManager
 * [OUTPUT]: TDD 证明 SDK 事件 → IPC 行协议的顺序、内容及工具状态
 * [POS]: OMP Extension 进程级 smoke test；不调用真实 Agent、不访问用户的 live Herdr
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import { test, expect } from "bun:test";
import net from "node:net";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

test("OMP events stream bounded assistant deltas to Solco socket", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "sc-"));
  const socketPath = path.join(dir, "s");
  const received: any[] = [];
  const server = net.createServer((socket) => {
    let buffer = "";
    socket.on("data", (chunk) => {
      buffer += String(chunk);
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";
      for (const line of lines) {
        if (line) received.push(JSON.parse(line));
      }
    });
  });
  await new Promise<void>((resolve) => server.listen(socketPath, resolve));
  const old = { HERDR_PANE_ID: process.env.HERDR_PANE_ID, HERDR_ENV: process.env.HERDR_ENV,
    SOLCO_SOCKET_PATH: process.env.SOLCO_SOCKET_PATH, OMPCODE: process.env.OMPCODE };
  process.env.HERDR_PANE_ID = "test-pane";
  process.env.HERDR_ENV = "1";
  process.env.SOLCO_SOCKET_PATH = socketPath;
  delete process.env.OMPCODE;
  try {
    const { default: extension } = await import("./omp-chat-stream.ts");
    const callbacks = new Map<string, (...args: any[]) => void>();
    extension({ on: (name: string, handler: (...args: any[]) => void) => callbacks.set(name, handler) });
    const ctx = { sessionManager: { getSessionFile: () => "/tmp/omp-test.jsonl" } };
    const call = (name: string, event: any) => callbacks.get(name)?.(event, ctx);
    call("message_start", { message: { role: "assistant" } });
    call("message_update", { message: { role: "assistant" }, assistantMessageEvent: { type: "text_delta", delta: "Hi " } });
    call("message_update", { message: { role: "assistant" }, assistantMessageEvent: { type: "text_delta", delta: "there" } });
    const emoji = "😀".repeat(1030);
    call("message_update", { message: { role: "assistant" }, assistantMessageEvent: { type: "text_delta", delta: emoji } });
    call("tool_execution_start", { toolCallId: "call-1", toolName: "read" });
    call("tool_execution_end", { toolCallId: "call-1", toolName: "read", isError: false });
    call("message_end", { message: { role: "assistant" } });
    await new Promise((resolve) => setTimeout(resolve, 180));
    expect(received.map((f) => f.params.kind)).toEqual([
      "message_start", "text_delta", "text_delta", "text_delta", "text_delta", "text_delta", "tool_start", "tool_end", "message_end",
    ]);
    expect(received[1].params.delta).toBe("Hi ");
    expect(received[2].params.delta).toBe("there");
    expect(received.slice(3, 6).map((f) => f.params.delta).join("")).toBe(emoji);
    expect(received[0].params.pane_id).toBe("test-pane");
    expect(received.every((f) => f.params.session_path === "/tmp/omp-test.jsonl")).toBe(true);
    expect(received.every((f, i) => f.params.seq === i + 1)).toBe(true);
  } finally {
    for (const [key, val] of Object.entries(old)) {
      if (val === undefined) delete process.env[key];
      else process.env[key] = val;
    }
    await new Promise<void>((resolve) => server.close(() => resolve()));
    rmSync(dir, { recursive: true, force: true });
  }
});
