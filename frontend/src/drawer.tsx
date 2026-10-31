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

export function SubagentDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const [view, setView] = useState<ChatBridgeSubagentView | null>(null);
  const [failed, setFailed] = useState(false);
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
