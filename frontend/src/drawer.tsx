import { useEffect, useRef, useState } from "react";
import { chat, type ChatBridgeSubagentView } from "./mygo";
import { Icon } from "./icons";
import { StateSlot, elapsedLabel } from "./subagents";
import { TurnRow } from "./turn";

/**
 * The sub-agent drawer (designs/subagents): a sub-agent's own transcript,
 * read-only, beside (wide window) or over (narrow window) the conversation.
 * The bridge resolves the id to a file the page never names; a running
 * sub-agent is re-read every 1.5 s, a settled one once.
 */

const POLL_MS = 1500;

// Drawer sizing bounds: the reader drags the drawer's left edge between
// them; App carries the width on the chat root's --drawer-w, so the
// beside-mode margins follow. Exported for the saved-value clamp.
export const DRAWER_MIN = 320;
export const DRAWER_MAX = 720;
export const DRAWER_DEFAULT = 420;

export function SubagentDrawer({
  id,
  onWidth,
  onWidthCommit,
  onClose,
}: {
  id: string;
  onWidth: (width: number) => void;
  onWidthCommit: (width: number) => void;
  onClose: () => void;
}) {
  const [view, setView] = useState<ChatBridgeSubagentView | null>(null);
  const [failed, setFailed] = useState(false);
  const [dragging, setDragging] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  useEffect(() => {
    let alive = true;
    let timer = 0;
    setView(null);
    setFailed(false);
    stick.current = true;
    const load = () => {
      chat
        .openSubagent(id)
        .then((next) => {
          if (!alive) return;
          setView(next);
          setFailed(false);
          if (next.subagent.status === "running") timer = window.setTimeout(load, POLL_MS);
        })
        .catch(() => {
          if (alive) setFailed(true);
        });
    };
    load();
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [id]);

  // Drag the drawer's left edge: the drawer is pinned to the chat column's
  // right edge, so the pointer's distance from it IS the width. Committed
  // (local storage, App) on release; the body cursor and selection are
  // held for the duration.
  useEffect(() => {
    if (!dragging) return;
    const widthAt = (clientX: number) => {
      const host = document.querySelector(".chat");
      const right = host instanceof HTMLElement ? host.getBoundingClientRect().right : window.innerWidth;
      return Math.max(DRAWER_MIN, Math.min(DRAWER_MAX, Math.round(right - clientX)));
    };
    const move = (moveEvent: PointerEvent) => onWidth(widthAt(moveEvent.clientX));
    const up = (upEvent: PointerEvent) => {
      onWidthCommit(widthAt(upEvent.clientX));
      setDragging(false);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
    document.body.classList.add("resizing-drawer");
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
      document.body.classList.remove("resizing-drawer");
    };
  }, [dragging, onWidth, onWidthCommit]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.isComposing) {
        event.preventDefault();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Follow the tail of a running sub-agent until the reader scrolls away.
  useEffect(() => {
    const body = bodyRef.current;
    if (body && stick.current) body.scrollTop = body.scrollHeight;
  }, [view]);

  const sub = view?.subagent;
  const turns = view?.turns ?? [];
  const now = Date.now();
  return (
    <aside className="drawer" aria-label="Sub-agent conversation">
      <div
        className={dragging ? "drawer-grip dragging" : "drawer-grip"}
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize drawer"
        onPointerDown={(downEvent) => {
          downEvent.preventDefault();
          setDragging(true);
        }}
      />
      <header className="dhead">
        {sub ? <StateSlot status={sub.status} /> : null}
        <div className="dtitle">
          <span className="sub-kind">{sub?.type || "Sub-agent"}</span>
          <span className="dsub">{sub?.description}</span>
        </div>
        <span className="pill">Read-only</span>
        <button type="button" className="ib" aria-label="Close" title="Close (Esc)" onClick={onClose}>
          <Icon name="x" size={14} />
        </button>
      </header>
      {sub ? (
        <div className="dmeta">
          {[elapsedLabel(sub, now), `${sub.toolCalls} tool call${sub.toolCalls === 1 ? "" : "s"}`, sub.provider].filter(Boolean).join(" · ")}
        </div>
      ) : null}
      <div
        className="dbody"
        ref={bodyRef}
        onScroll={(event) => {
          const el = event.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {failed ? <div className="chat-note">Can't read this sub-agent.</div> : null}
        {view?.error ? <div className="chat-note">{view.error}</div> : null}
        {!view && !failed ? <div className="chat-note">Loading…</div> : null}
        {turns.map((turn, index) => (
          <TurnRow
            key={index}
            turn={sub?.status === "running" && index === turns.length - 1 && !turn.user ? { ...turn, live: true } : turn}
            canReview={false}
            onReview={() => {}}
            isLast={index === turns.length - 1}
          />
        ))}
      </div>
    </aside>
  );
}
