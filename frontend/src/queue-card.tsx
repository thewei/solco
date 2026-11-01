import React, { useRef, useState } from "react";
import { Icon } from "./icons";
import type { ChatQueuedItem } from "./mygo";

/**
 * The queue of follow-ups waiting behind a running turn (designs/chat-view,
 * 2026-11): one collapsible card above the composer stacking every queued
 * message in delivery order, with per-row cancel, drag reordering (desktop
 * hosts only) and ⌥-arrow moves. The bridge projection is the only source of
 * truth: the drag preview is local and dies at the pointer, and every commit
 * renders whatever projection the call returns.
 */

/** Rows pinned to the head: waiting for the turn boundary or already sending. */
const PINNED_STATE: Record<string, boolean> = {
  "waiting-for-turn-boundary": true,
  delivering: true,
};
/** States whose drag handle shows (and which ⌥-arrows can move). */
const MOVABLE_STATE: Record<string, boolean> = {
  queued: true,
  "failed-recoverable": true,
};
/** States drawn in the warning tone, with Detail as the tooltip. */
const ALERT_STATE: Record<string, boolean> = {
  "failed-recoverable": true,
  "delivery-uncertain": true,
};

/** The delivery order with the row at `from` re-slotted to `to` (preview only). */
function reSlotted(items: ChatQueuedItem[], from: number, to: number): ChatQueuedItem[] {
  const next = items.slice();
  const [row] = next.splice(from, 1);
  next.splice(to, 0, row);
  return next;
}

export function QueueCard({
  items,
  canReorder,
  onCancelItem,
  onReorder,
  onCancelAll,
}: {
  items?: ChatQueuedItem[];
  /** Host offers chat.ReorderQueued (desktop): handles and ⌥-arrows; the phone hides both. */
  canReorder: boolean;
  onCancelItem: (id: string) => void;
  onReorder: (id: string, index: number) => void;
  onCancelAll: () => void;
}) {
  const list = items ?? [];
  const [open, setOpen] = useState(false);
  // The drag in flight: the grabbed row, where it started (index, pointer Y,
  // row height) and the target it currently hovers over. Only the preview
  // order derives from it; nothing survives the pointer settling.
  const [drag, setDrag] = useState<{ id: string; to: number } | null>(null);
  const session = useRef<{ id: string; from: number; startY: number; rowH: number; to: number } | null>(null);
  const listRef = useRef<HTMLOListElement>(null);

  if (list.length === 0) return null;

  // The pinned prefix: leading waiting/delivering rows nothing may jump ahead of.
  const firstLoose = list.findIndex((item) => !PINNED_STATE[item.state]);
  const pinnedCount = firstLoose === -1 ? list.length : firstLoose;
  const nextIndex = list.findIndex((item) => PINNED_STATE[item.state]);
  const from = drag ? list.findIndex((item) => item.id === drag.id) : -1;
  const shown = drag && from !== -1 ? reSlotted(list, from, drag.to) : list;

  const startDrag = (id: string, index: number) => (event: React.PointerEvent<HTMLSpanElement>) => {
    if (!canReorder || event.button !== 0 || !listRef.current) return;
    const row = listRef.current.children[index] as HTMLElement | undefined;
    session.current = {
      id,
      from: index,
      startY: event.clientY,
      rowH: row?.getBoundingClientRect().height || 26,
      to: index,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    setDrag({ id, to: index });
  };

  const moveDrag = (event: React.PointerEvent<HTMLSpanElement>) => {
    const s = session.current;
    if (!s) return;
    const to = Math.max(pinnedCount, Math.min(s.from + Math.round((event.clientY - s.startY) / s.rowH), list.length - 1));
    if (to === s.to) return;
    s.to = to;
    setDrag({ id: s.id, to });
  };

  const endDrag = (commit: boolean) => {
    const s = session.current;
    session.current = null;
    setDrag(null);
    if (s && commit && s.to !== s.from) onReorder(s.id, s.to);
  };

  // ⌥ArrowUp/⌥ArrowDown on a movable row: the same move the handle performs,
  // clamped to the pinned prefix.
  const moveByKey = (id: string, movable: boolean) => (event: React.KeyboardEvent<HTMLLIElement>) => {
    if (!movable || !event.altKey || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
    const at = list.findIndex((item) => item.id === id);
    const to = Math.max(pinnedCount, Math.min(at + (event.key === "ArrowUp" ? -1 : 1), list.length - 1));
    if (to === at) return;
    event.preventDefault();
    event.stopPropagation();
    onReorder(id, to);
  };

  const latest = list[list.length - 1];
  return (
    <div className="queueCard">
      {open ? (
        <>
          <div className="qcHead">
            <button type="button" className="qcHeadBtn" aria-expanded onClick={() => setOpen(false)}>
              <span className="qcTtl">Queued · {list.length}</span>
              <span className="chev open">
                <Icon name="chev" size={13} />
              </span>
            </button>
            <button type="button" className="qcAll" onClick={onCancelAll}>
              Cancel all
            </button>
          </div>
          <ol className={`qcList${list.length > 4 ? " scroll" : ""}`} ref={listRef}>
            {shown.map((item, index) => {
              const movable = canReorder && MOVABLE_STATE[item.state];
              return (
                <li
                  key={item.id}
                  className={`qcItem${ALERT_STATE[item.state] ? " alert" : ""}${drag?.id === item.id ? " dragging" : ""}`}
                  tabIndex={0}
                  onKeyDown={moveByKey(item.id, movable)}
                >
                  <span
                    className={`qcHandle${movable ? "" : " off"}`}
                    title={movable ? "Drag to reorder" : undefined}
                    onPointerDown={movable ? startDrag(item.id, index) : undefined}
                    onPointerMove={movable ? moveDrag : undefined}
                    onPointerUp={movable ? () => endDrag(true) : undefined}
                    onPointerCancel={movable ? () => endDrag(false) : undefined}
                  />
                  <span className="qcText" title={item.detail || item.text}>
                    {item.text}
                  </span>
                  {index === nextIndex ? <span className="qcNext">next</span> : null}
                  {item.state !== "delivering" ? (
                    <button type="button" className="qcX" aria-label="Cancel queued message" onClick={() => onCancelItem(item.id)}>
                      <Icon name="x" size={12} />
                    </button>
                  ) : (
                    <span className="qcX off" aria-hidden />
                  )}
                </li>
              );
            })}
          </ol>
        </>
      ) : (
        <button type="button" className="qcRow" aria-expanded={open} onClick={() => setOpen(true)}>
          <span className="qcCnt">{list.length}</span>
          <span className="qcLbl">Queued</span>
          <span className="qcTxt">{latest.text}</span>
          <span className="chev">
            <Icon name="chev" size={13} />
          </span>
        </button>
      )}
    </div>
  );
}
