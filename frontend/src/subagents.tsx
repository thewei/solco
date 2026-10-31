import { createContext, useContext, useEffect, useState } from "react";
import type { ChatBridgeSubagent } from "./mygo";
import { Icon } from "./icons";

/**
 * Sub-agents in the conversation (designs/subagents, 2026-10-31): the tool
 * call that spawned them becomes a card (1-2) or a roster (3+) with the same
 * three states everywhere — running spinner, done ring, failed dot. The
 * bridge lists only sub-agents a tool call provably spawned; everything here
 * is presentation of that read-only list.
 */

export interface SubagentContextValue {
  byId: Map<string, ChatBridgeSubagent>;
  open: (id: string) => void;
  openId?: string;
}

export const SubagentContext = createContext<SubagentContextValue>({ byId: new Map(), open: () => {} });

/** A ticking clock only while something runs (elapsed counters). */
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);
  return now;
}

export function elapsedLabel(sub: ChatBridgeSubagent, now: number): string {
  if (!sub.startedMs) return "";
  const end = sub.status === "running" ? now : (sub.updatedMs ?? now);
  const seconds = Math.max(0, Math.round((end - sub.startedMs) / 1000));
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

export function StateSlot({ status }: { status: string }) {
  if (status === "running") return <span className="spin sub-slot" aria-label="Running" />;
  return <span className={`sub-slot ${status === "failed" ? "dot" : "ring"}`} role="img" aria-label={status === "failed" ? "Failed" : "Done"} />;
}

function kindLabel(sub: ChatBridgeSubagent): string {
  return sub.type || "Sub-agent";
}

function toolCallsLabel(sub: ChatBridgeSubagent): string {
  return `${sub.toolCalls} tool call${sub.toolCalls === 1 ? "" : "s"}`;
}

/** The agents of one turn: cards up to two, a roster from three. */
export function SubagentBlock({ ids }: { ids: string[] }) {
  const { byId } = useContext(SubagentContext);
  const subs = ids.map((id) => byId.get(id)).filter((sub): sub is ChatBridgeSubagent => !!sub);
  const running = subs.some((sub) => sub.status === "running");
  const now = useNow(running);
  if (subs.length === 0) return null;
  if (subs.length >= 3) return <Roster subs={subs} now={now} />;
  return (
    <div className="subs">
      {subs.map((sub) => (
        <Card key={sub.id} sub={sub} now={now} />
      ))}
    </div>
  );
}

function Card({ sub, now }: { sub: ChatBridgeSubagent; now: number }) {
  const { open, openId } = useContext(SubagentContext);
  const [showPrompt, setShowPrompt] = useState(false);
  return (
    <div className={`sub-card ${sub.status}${openId === sub.id ? " on" : ""}`}>
      <button type="button" className="sub-head" onClick={() => open(sub.id)} title="Open the sub-agent's conversation">
        <span className="sub-kind">{kindLabel(sub)}</span>
        <span className="sub-desc">{sub.description}</span>
        <span className="sub-time">{elapsedLabel(sub, now)}</span>
        <StateSlot status={sub.status} />
      </button>
      {sub.status === "running" && sub.tail ? <div className="sub-tail">{sub.tail}</div> : null}
      {sub.status !== "running" && sub.result ? <div className="sub-result">{sub.result}</div> : null}
      <div className="sub-foot">
        <span>
          {sub.status === "failed" ? "Failed · " : ""}
          {toolCallsLabel(sub)}
        </span>
        {sub.prompt ? (
          <button type="button" className="sub-link" aria-expanded={showPrompt} onClick={() => setShowPrompt(!showPrompt)}>
            Prompt
          </button>
        ) : null}
        <button type="button" className="sub-link" onClick={() => open(sub.id)}>
          Open
          <Icon name="chev" size={12} />
        </button>
      </div>
      {showPrompt && sub.prompt ? <div className="sub-prompt">{sub.prompt}</div> : null}
    </div>
  );
}

function Roster({ subs, now }: { subs: ChatBridgeSubagent[]; now: number }) {
  const { open, openId } = useContext(SubagentContext);
  const running = subs.filter((sub) => sub.status === "running").length;
  const failed = subs.filter((sub) => sub.status === "failed").length;
  return (
    <div className="sub-roster">
      <div className="sub-rhead">
        <Icon name="agent" size={13} />
        <span>{subs.length} sub-agents</span>
        <span className="o">
          {running > 0 ? `${running} running` : failed > 0 ? `${failed} failed` : "all done"}
        </span>
      </div>
      {subs.map((sub) => (
        <button
          key={sub.id}
          type="button"
          className={`sub-rrow ${sub.status}${openId === sub.id ? " on" : ""}`}
          onClick={() => open(sub.id)}
          title="Open the sub-agent's conversation"
        >
          <StateSlot status={sub.status} />
          <span className="sub-kind">{kindLabel(sub)}</span>
          <span className="sub-desc">{sub.status === "running" && sub.tail ? sub.tail : sub.description}</span>
          <span className="sub-time">{elapsedLabel(sub, now)}</span>
        </button>
      ))}
    </div>
  );
}
