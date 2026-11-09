import { useVirtualizer, type VirtualItem } from "@tanstack/react-virtual";
import { useCallback, type RefObject } from "react";
import type { ChatTurn } from "./mygo";

/**
 * [INPUT]: the chat scroller (`.chat-log`), the turns, a stable key per turn
 * [OUTPUT]: useTurnList — the one place that knows whether turns are windowed
 * [POS]: long conversations render only the turns near the viewport
 *        (@tanstack/react-virtual, measured rows); short ones stay plain DOM.
 *        Turn navigation (⌘↑/⌘↓, the outline rail, find) asks this hook for a
 *        turn's top and for scrolling, never the DOM, so both modes behave alike.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

/** Conversations with fewer turns render every row (no windowing). */
export const VIRTUAL_MIN = 40;
/** On phones, window earlier to keep DOM lightweight and avoid rendering dozens of rich turns. */
export const PHONE_VIRTUAL_MIN = 15;

export interface TurnList {
  /** Whether rows are windowed. */
  virtual: boolean;
  /** The rows to render in windowed mode (empty otherwise). */
  rows: VirtualItem[];
  /** Height of the windowed list. */
  total: number;
  /** Row ref: measures the row's real height. */
  measure: (element: Element | null) => void;
  /** Top of turn `index` in the scroller's content coordinates, if known. */
  top: (index: number) => number | null;
  /** Brings turn `index` to the top of the viewport. */
  scrollTo: (index: number, smooth: boolean) => void;
}

export function useTurnList(
  logRef: RefObject<HTMLDivElement | null>,
  count: number,
  keyOf: (index: number) => string,
  margin: number,
): TurnList {
  const phone = typeof document !== "undefined" && document.documentElement.dataset.host === "phone";
  const threshold = phone ? PHONE_VIRTUAL_MIN : VIRTUAL_MIN;
  const virtual = count >= threshold;
  const list = useVirtualizer({
    count: virtual ? count : 0,
    getScrollElement: () => logRef.current,
    estimateSize: () => (phone ? 120 : 160),
    overscan: phone ? 3 : 8,
    scrollMargin: margin,
    getItemKey: keyOf,
  });

  const top = useCallback(
    (index: number): number | null => {
      const cached = virtual ? list.measurementsCache[index]?.start : null;
      if (typeof cached === "number") return cached;
      const log = logRef.current;
      const node = log?.querySelector<HTMLElement>(`[data-turn="${index}"]`);
      if (!log || !node) return null;
      return node.getBoundingClientRect().top - log.getBoundingClientRect().top + log.scrollTop;
    },
    [virtual, list, logRef],
  );

  const scrollTo = useCallback(
    (index: number, smooth: boolean) => {
      if (virtual) {
        // Windowed rows only know estimates until measured; scrollToIndex
        // keeps correcting as the target row lands.
        list.scrollToIndex(index, { align: "start" });
        return;
      }
      const log = logRef.current;
      const at = top(index);
      if (log && at !== null) log.scrollTo({ top: at, behavior: smooth ? "smooth" : "auto" });
    },
    [virtual, list, logRef, top],
  );

  return {
    virtual,
    rows: virtual ? list.getVirtualItems() : [],
    total: virtual ? list.getTotalSize() : 0,
    measure: list.measureElement,
    top,
    scrollTo,
  };
}

/** The prose a turn shows, for find to search turns that are not rendered. */
export function turnText(turn: ChatTurn): string {
  return [turn.text, turn.narration, turn.thinking].filter(Boolean).join("\n");
}
