import { useState } from "react";
import { assistants, type AssistantsResult } from "../mygo";
import { ago } from "./Progress";

/**
 * Settled work as cards above the composer. Each card is the system's own
 * record of a task that finished, failed or is waiting for review — made by the
 * core from committed task state, never from the Coordinator's words — with the
 * one thing that is worth doing next (Open Pane, or the Tasks list for review).
 * Dismissing only moves a watermark kept in this page's local storage.
 */

const SHOWN = 2;
const LABEL: Record<string, string> = { done: "Finished", failed: "Failed", review_required: "Ready for review" };
const seenKey = (assistantId: string) => `as-results-seen:${assistantId}`;

export function ResultCards({
  assistantId,
  results,
  skip,
  onTasks,
  setError,
}: {
  assistantId: string;
  results: AssistantsResult[];
  /** Tasks the Needs-you strip already carries, so a card never says it twice. */
  skip: ReadonlySet<string>;
  onTasks: (filter: "open" | "done") => void;
  setError: (e: string) => void;
}) {
  const [seen, setSeen] = useState(() => Number(localStorage.getItem(seenKey(assistantId))) || 0);
  const cards = results.filter((r) => r.at > seen && !skip.has(r.taskId));
  if (cards.length === 0) return null;
  const dismiss = (at: number) => {
    localStorage.setItem(seenKey(assistantId), String(at));
    setSeen(at);
  };
  const shown = cards.slice(0, SHOWN);
  const hidden = cards.length - shown.length;
  return (
    <section className="as-results" aria-label="Finished work">
      {shown.map((r) => (
        <div key={r.taskId} className={`as-result ${r.state === "failed" ? "bad" : ""}`}>
          <div className="as-result-text">
            <strong>{r.title}</strong>
            <span className="as-meta">
              {LABEL[r.state] ?? r.state} · {ago(r.at)}
            </span>
          </div>
          {r.paneId ? (
            <button type="button" className="as-btn" onClick={() => void assistants.openPane(r.paneId as string).catch((e) => setError(e instanceof Error ? e.message : String(e)))}>
              Open Pane
            </button>
          ) : null}
          <button type="button" className="as-btn" onClick={() => onTasks(r.state === "review_required" ? "open" : "done")}>
            {r.state === "review_required" ? "Review" : "Details"}
          </button>
          <button type="button" className="as-ib" aria-label="Dismiss" onClick={() => dismiss(r.at)}>
            ✕
          </button>
        </div>
      ))}
      {hidden > 0 ? (
        <button type="button" className="as-link" onClick={() => onTasks("done")}>
          {hidden} more
        </button>
      ) : null}
    </section>
  );
}
