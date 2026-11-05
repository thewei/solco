/**
 * [INPUT]: OMP/Pi ExtensionAPI event observer, Herdr pane identity from process.env and the existing Solco Unix socket.
 * [OUTPUT]: Non-blocking, bounded chat.stream JSON-RPC notifications for the same OMP or Pi TUI process.
 * [POS]: Read-only companion extension; never edits Herdr's own extension, transcript, prompts, or lifecycle.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
// @ts-nocheck
// Install independently: cp scripts/omp-chat-stream.ts ~/.omp/agent/extensions/solco-chat-stream.ts
// Managed by Solco, NOT by Herdr. Reload/restart agent after installation.
// This is the sole canonical source. The Pi installer replaces only the
// following constant in its separately owned ~/.pi extension copy.

import net from "node:net";
import path from "node:path";
import { randomUUID } from "node:crypto";

const streamAgent = "omp";
const paneID = process.env.HERDR_PANE_ID;
const enabled = process.env.HERDR_ENV === "1" && !!paneID && process.env.OMPCODE !== "1";
const socketPath = process.env.SOLCO_SOCKET_PATH
  || path.join(process.env.HOME || "", ".solco", "run", "agent-status.sock");
// A sortable process-start prefix fences late frames from a prior OMP process.
const epoch = `${String(Date.now()).padStart(13, "0")}-${randomUUID()}`;
let seq = 0;
let currentMessage = "";
let currentSession = "";
let messageCounter = 0;

const queue: string[] = [];
const MAX_FRAMES = 128;
let timer: ReturnType<typeof setTimeout> | undefined;
let sending = false;

function sessionPath(ctx: any): string {
  try {
    const file = ctx?.sessionManager?.getSessionFile?.();
    return typeof file === "string" && path.isAbsolute(file) ? file : "";
  } catch {
    return "";
  }
}

// Queue only, never await network writes from an Agent callback.
function publish(kind: string, ctx: any, fields: Record<string, unknown> = {}) {
  if (!enabled || !socketPath || !paneID) return;
  const file = sessionPath(ctx);
  if (!file || !currentMessage) return;
  if (currentSession !== file) {
    currentSession = file;
    queue.length = 0;
  }
  const wire = JSON.stringify({
    method: "chat.stream",
    params: { agent: streamAgent, pane_id: paneID, session_path: file, epoch,
      seq: ++seq, kind, message_id: currentMessage, ...fields },
  }) + "\n";
  // A burst that outpaces the UI is disposable; JSONL remains authoritative.
  if (Buffer.byteLength(wire) > 12000) return;
  if (queue.length >= MAX_FRAMES) queue.shift();
  queue.push(wire);
  if (!timer && !sending) timer = setTimeout(flush, 40);
}

function flush() {
  timer = undefined;
  if (sending || queue.length === 0) return;
  sending = true;
  const frames = queue.splice(0);
  const socket = net.createConnection(socketPath);
  let finished = false;
  const done = () => {
    if (finished) return;
    finished = true;
    socket.destroy();
    sending = false;
    if (queue.length && !timer) timer = setTimeout(flush, 40);
  };
  const timeout = setTimeout(done, 400);
  timeout.unref?.();
  socket.on("error", done);
  socket.on("connect", () => {
    socket.end(frames.join(""));
  });
  socket.on("close", () => {
    clearTimeout(timeout);
    done();
  });
}

export default function (pi: any) {
  if (!enabled) return;

  pi.on("message_start", (event: any, ctx: any) => {
    if (event?.message?.role !== "assistant") return;
    const file = sessionPath(ctx);
    if (!file) return;
    currentMessage = `m${++messageCounter}`;
    // publish() clears queued frames when this OMP session file changes.
    publish("message_start", ctx);
  });

  pi.on("message_update", (event: any, ctx: any) => {
    if (event?.message?.role !== "assistant") return;
    const delta = event.assistantMessageEvent;
    if (delta?.type !== "text_delta" && delta?.type !== "thinking_delta") return;
    if (typeof delta.delta !== "string" || !delta.delta) return;
    // Split at Unicode code points, never in the middle of an emoji's
    // surrogate pair. A 512-code-point chunk stays under the Go 4096B cap.
    const chars = Array.from(delta.delta);
    for (let i = 0; i < chars.length; i += 512) {
      publish(delta.type, ctx, { delta: chars.slice(i, i + 512).join("") });
    }
  });

  pi.on("message_end", (event: any, ctx: any) => {
    if (event?.message?.role === "assistant") publish("message_end", ctx);
  });

  pi.on("tool_execution_start", (event: any, ctx: any) => {
    if (typeof event?.toolCallId !== "string" || typeof event?.toolName !== "string") return;
    publish("tool_start", ctx, { tool_call_id: event.toolCallId, tool_name: event.toolName });
  });
  pi.on("tool_execution_end", (event: any, ctx: any) => {
    if (typeof event?.toolCallId !== "string" || typeof event?.toolName !== "string") return;
    publish("tool_end", ctx, { tool_call_id: event.toolCallId, tool_name: event.toolName, tool_error: !!event.isError });
  });
}
