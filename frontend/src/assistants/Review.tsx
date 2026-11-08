import { useState } from "react";
import { assistants, type AssistantsTask } from "../mygo";

type Act = (call: () => Promise<unknown>) => Promise<string>;

const rid = () => crypto.randomUUID();

const STATE: Record<string, string> = {
  ready: "ready for your decision",
  checks_failed: "checks failed",
  awaiting_approval: "merge waiting for your yes",
  merging: "merging",
  merged: "merged",
  merge_failed: "merge failed",
  stale: "changed since review",
  changes_requested: "changes requested",
  rejected: "rejected",
};

/** States in which the person can still answer the evidence. */
const OPEN = new Set(["ready", "checks_failed", "awaiting_approval", "stale", "merge_failed"]);

/**
 * A coding task's evidence and the person's three answers (Gap Q02): Accept (a
 * merge card for exactly this head and diff, then Merge), Request changes (the
 * task goes back to its own worktree with the words) and Reject (the task
 * closes, nothing merges, the work stays). Every state and refusal is the
 * core's; the box only draws it.
 */
export function ReviewBox({ task, act }: { task: AssistantsTask; act: Act }) {
  const rv = task.review;
  const [mode, setMode] = useState<"" | "changes" | "reject">("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  if (task.kind !== "coding") return null;
  const run = async (call: () => Promise<unknown>) => {
    setBusy(true);
    const refusal = await act(call);
    setBusy(false);
    return refusal;
  };
  const needsStart = task.state === "review_required" && (!rv || rv.state === "changes_requested" || rv.state === "stale" || rv.state === "merge_failed");
  if (!rv && !needsStart) return null;

  const send = async () => {
    if (!rv) return;
    const refusal = await run(() => (mode === "changes" ? assistants.resumeReview(rid(), rv.id, text) : assistants.rejectReview(rid(), rv.id, text)));
    if (!refusal) {
      setMode("");
      setText("");
    }
  };

  return (
    <div className="as-review">
      {rv ? (
        <>
          <div className="as-row">
            <strong>Changes</strong>
            <span className={`as-chip ${rv.state}`}>{STATE[rv.state] ?? rv.state}</span>
          </div>
          <p className="as-meta">
            {rv.branch} · {rv.head.slice(0, 7)} → {rv.targetBranch} · {rv.files} {rv.files === 1 ? "file" : "files"} +{rv.insertions} −{rv.deletions}
          </p>
          {rv.paths.slice(0, 6).map((p) => (
            <p className="as-meta as-path" key={p}>
              {p}
            </p>
          ))}
          {rv.paths.length > 6 ? <p className="as-meta">and {rv.paths.length - 6} more</p> : null}
          {rv.checks.length === 0 ? <p className="as-meta">No checks are approved for this Assistant.</p> : null}
          {rv.checks.map((c) => (
            <p className="as-meta" key={c.command}>
              <span className={`as-dot ${c.ok ? "done" : "failed"}`} /> {c.command} {c.ok ? "passed" : `failed (exit ${c.exitCode})`}
            </p>
          ))}
          {rv.error ? <p className="as-text">{rv.error}</p> : null}
        </>
      ) : (
        <p className="as-meta">The agent finished. Nothing has been checked yet.</p>
      )}
      {mode ? (
        <div className="as-field">
          <textarea
            className="as-input"
            rows={3}
            value={text}
            maxLength={2000}
            autoFocus
            placeholder={mode === "changes" ? "What should change? The agent continues on the same branch." : "Why? (optional) The work is kept; nothing merges."}
            onChange={(e) => setText(e.target.value)}
          />
          <div className="as-row">
            <button type="button" className={`as-btn ${mode === "reject" ? "danger" : "primary"}`} disabled={busy || (mode === "changes" && !text.trim())} onClick={() => void send()}>
              {mode === "changes" ? "Send back" : "Reject"}
            </button>
            <button type="button" className="as-btn" disabled={busy} onClick={() => setMode("")}>
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <div className="as-row">
          {needsStart ? (
            <button type="button" className="as-btn primary" disabled={busy} onClick={() => void run(() => assistants.startReview(rid(), task.id))}>
              {busy ? "Checking…" : rv ? "Review again" : "Review changes"}
            </button>
          ) : null}
          {rv?.state === "ready" ? (
            <button type="button" className="as-btn primary" disabled={busy} onClick={() => void run(() => assistants.acceptReview(rid(), rv.id))}>
              Accept…
            </button>
          ) : null}
          {rv?.state === "awaiting_approval" && rv.approvalState === "approved" ? (
            <button type="button" className="as-btn primary" disabled={busy} onClick={() => void run(() => assistants.mergeReview(rv.id))}>
              {busy ? "Merging…" : "Merge"}
            </button>
          ) : null}
          {rv?.state === "awaiting_approval" && rv.approvalState === "pending" ? <span className="as-meta">Answer the merge card in Needs you.</span> : null}
          {rv && OPEN.has(rv.state) && task.state === "review_required" ? (
            <>
              <button type="button" className="as-btn" disabled={busy} onClick={() => setMode("changes")}>
                Request changes
              </button>
              <button type="button" className="as-btn danger" disabled={busy} onClick={() => setMode("reject")}>
                Reject
              </button>
            </>
          ) : null}
        </div>
      )}
    </div>
  );
}
