/**
 * [INPUT]: applyChatDelta immutable suffix reducer and Go generated ChatBridgeState/ChatDelta contracts
 * [OUTPUT]: matching sequence applies, missed event and cross conversation fail closed, immutable suffix replacement
 * [POS]: Cross-platform Chat page no-IO wire reducer regression
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import { describe, expect, test } from "bun:test";
import { applyChatDelta } from "../src/chat-delta";
import type { ChatBridgeState, ChatDelta } from "../src/mygo";

const baseline = {
  seq: 7,
  conversationId: "live:omp:term-1",
  turns: [
    { user: true, text: "question" },
    { user: false, narration: "first" },
    { user: false, narration: "partial" },
  ],
} as ChatBridgeState;

function patch(overrides: Partial<ChatDelta> = {}): ChatDelta {
  return {
    baseSeq: 7, seq: 8, conversationId: "live:omp:term-1",
    start: 2, turns: [{ user: false, narration: "partial new", live: true }],
    ...overrides,
  };
}

describe("Chat View stream suffix merge", () => {
  test("updates the tail while keeping earlier turns and baseline untouched", () => {
    const merged = applyChatDelta(baseline, patch());
    expect(merged?.seq).toBe(8);
    expect(merged?.turns[0]).toBe(baseline.turns[0]);
    expect(merged?.turns.at(-1)?.narration).toBe("partial new");
    expect(baseline.turns.at(-1)?.narration).toBe("partial");
  });

  test("rejects missing or cross-conversation baseline", () => {
    expect(applyChatDelta(null, patch())).toBeNull();
    expect(applyChatDelta(baseline, patch({ baseSeq: 6 }))).toBeNull();
    expect(applyChatDelta(baseline, patch({ conversationId: "another" }))).toBeNull();
    expect(applyChatDelta(baseline, patch({ start: 4 }))).toBeNull();
    expect(applyChatDelta(baseline, patch({ seq: 7 }))).toBeNull();
  });

  test("appends a new streamed row without losing committed history", () => {
    const next = applyChatDelta(baseline, patch({
      start: 3, turns: [{ user: false, narration: "typing", live: true }],
    }));
    expect(next?.turns).toHaveLength(4);
    expect(next?.turns[2].narration).toBe("partial");
  });
});
