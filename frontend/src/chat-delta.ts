/**
 * [INPUT]: Go ChatDelta suffix-patch event and the last accepted ChatBridgeState
 * [OUTPUT]: applyChatDelta — strictly ordered, same-conversation immutable patch or null for snapshot repair
 * [POS]: The one cross-platform Chat page reducer for streamed turns; desktop+mobile share this source
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import type { ChatBridgeState, ChatDelta } from "./mygo";

/** Only a matching full baseline may be patched. Never invent missed rows. */
export function applyChatDelta(
  previous: ChatBridgeState | null,
  delta: ChatDelta,
): ChatBridgeState | null {
  if (
    !previous ||
    previous.seq !== delta.baseSeq ||
    previous.conversationId !== delta.conversationId ||
    delta.seq <= delta.baseSeq ||
    !Number.isSafeInteger(delta.start) ||
    delta.start < 0 ||
    delta.start > previous.turns.length ||
    delta.turns.length > 3
  ) return null;

  return {
    ...previous,
    seq: delta.seq,
    turns: [...previous.turns.slice(0, delta.start), ...delta.turns],
  };
}
