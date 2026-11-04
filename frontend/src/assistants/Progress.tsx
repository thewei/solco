import { useState } from "react";
import type { Fact, GoalPlan, MilestoneView, OverviewRow } from "../mygo";

/**
 * Assistants 3.1 U3 — what the core knows about a Goal's progress.
 *
 * Everything here is the system's own record: facts are sentences the core
 * wrote about committed state, milestone states are derived from the real task
 * behind each one. None of it is an Agent's reply, and the strip says so
 * ("Solco") instead of dressing up as a message from the Assistant.
 */

const KIND_LABEL: Record<string, string> = {
  accepted: "Accepted",
  planned: "Planned",
  replanned: "Re-planned",
  delegated: "Delegated",
  verifying: "Verifying",
  verified: "Verified",
  blocked: "Blocked",
  ready: "Needs you",
  done: "Done",
  stale: "No progress",
  held: "Held",
  task_added: "Task filed",
  run_finished: "Run settled",
  pulse: "Progress",
  routine: "Routine",
  routine_done: "Routine ran",
  routine_failed: "Routine failed",
};

const TONE: Record<string, string> = {
  verified: "ok",
  done: "ok",
  blocked: "bad",
  held: "bad",
  stale: "warn",
  ready: "warn",
  verifying: "work",
  delegated: "work",
  task_added: "work",
  run_finished: "work",
  pulse: "work",
  routine_done: "ok",
  routine_failed: "bad",
};

function ago(ms: number): string {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

/**
 * Live multi-task counters from the plugin's authoritative Overview. A
 * Coordinator and Group Agent are not worker tasks in this projection.
 * Statuses are not inferred from conversation prose, and "finished" never
 * implies a coding task passed review.
 */
export function WorkPulse({ row, onTasks }: { row?: OverviewRow; onTasks: () => void }) {
  if (!row || (row.tasks === 0 && row.goals === 0)) return null;
  const other = Math.max(0, row.tasks - row.queued - row.running - row.done);
  const parts = [
    row.goals > 0 ? String(row.goals) + (row.goals === 1 ? " goal" : " goals") : "",
    row.running > 0 ? String(row.running) + " running" : "",
    row.queued > 0 ? String(row.queued) + " queued" : "",
    row.needsYou > 0 ? String(row.needsYou) + " need review" : "",
    row.done > 0 ? String(row.done) + " finished" : "",
    other > 0 ? String(other) + " other statuses" : "",
  ].filter(Boolean);
  return (
    <section className="as-workpulse" aria-label="Assistant task progress">
      <span className="as-workpulse-title">Work</span>
      <span className="as-workpulse-stats">{parts.join(" · ") || "Preparing work"}</span>
      <button type="button" className="as-link" onClick={onTasks}>View tasks</button>
    </section>
  );
}

/** The newest facts above the chat: one line, unfolding into the last few.
 * Navigate by authoritative task/goal IDs, never by a title string.
 */
export function FactStrip({ facts, onOpenTask, onOpenGoal }: { facts: Fact[]; onOpenTask: () => void; onOpenGoal: () => void }) {
  const [open, setOpen] = useState(false);
  if (facts.length === 0) return null;
  const [latest] = facts;
  return (
    <section className="as-facts" aria-label="Progress">
      <div className="as-facts-top">
        <span className="as-facts-by">Solco</span>
        <span className={`as-fact-kind ${TONE[latest.kind] ?? ""}`}>{KIND_LABEL[latest.kind] ?? latest.kind}</span>
        <span className="as-ellipsis as-facts-text">{latest.text}</span>
        <span className="as-meta">{ago(latest.createdAt)}</span>
        {latest.taskId ? <button type="button" className="as-link" onClick={onOpenTask}>View task</button> : latest.goalId ? <button type="button" className="as-link" onClick={onOpenGoal}>View goal</button> : null}
        {facts.length > 1 ? (
          <button type="button" className="as-link" onClick={() => setOpen((v) => !v)}>
            {open ? "Hide" : `${facts.length - 1} earlier`}
          </button>
        ) : null}
      </div>
      {open
        ? facts.slice(1).map((f) => (
            <div key={f.id} className="as-facts-top as-facts-more">
              <span className="as-facts-by" />
              <span className={`as-fact-kind ${TONE[f.kind] ?? ""}`}>{KIND_LABEL[f.kind] ?? f.kind}</span>
              <span className="as-ellipsis as-facts-text">{f.text}</span>
              <span className="as-meta">{ago(f.createdAt)}</span>
              {f.taskId ? <button type="button" className="as-link" onClick={onOpenTask}>View task</button> : f.goalId ? <button type="button" className="as-link" onClick={onOpenGoal}>View goal</button> : null}
            </div>
          ))
        : null}
    </section>
  );
}

const STATE_LABEL: Record<string, string> = {
  pending: "Waiting",
  working: "Working",
  verifying: "In review",
  verified: "Verified",
  blocked: "Blocked",
  needs_you: "Needs you",
};

const STATE_TONE: Record<string, string> = { verified: "ok", blocked: "bad", needs_you: "warn", verifying: "work", working: "work" };

function Milestone({ m }: { m: MilestoneView }) {
  return (
    <li className="as-ms">
      <span className={`as-fact-kind ${STATE_TONE[m.state] ?? ""}`}>{STATE_LABEL[m.state] ?? m.state}</span>
      <span className="as-ms-title">{m.title}</span>
      {m.evidence ? <span className="as-meta as-ms-ev">{m.evidence}</span> : null}
    </li>
  );
}

/** A Goal's plan: criteria, milestones with their real states, the latest facts. */
export function PlanCard({ plan, facts }: { plan: GoalPlan; facts: Fact[] }) {
  return (
    <div className="as-plan">
      <p className="as-meta">
        {plan.total === 0 ? "Plan has no milestones" : `${plan.verified} of ${plan.total} milestone${plan.total === 1 ? "" : "s"} verified`}
        {" · "}
        {plan.state.replace("_", " ")}
        {plan.replans > 0 ? ` · revised ${plan.replans}×` : ""}
      </p>
      {plan.successCriteria ? <p className="as-text">Done when: {plan.successCriteria}</p> : null}
      <ul className="as-ms-list">
        {plan.milestones.map((m) => (
          <Milestone key={m.id} m={m} />
        ))}
      </ul>
      {plan.nextAction ? <p className="as-meta">Next: {plan.nextAction}</p> : null}
      {facts.slice(0, 3).map((f) => (
        <p key={f.id} className="as-meta as-ellipsis">
          <span className={`as-fact-kind ${TONE[f.kind] ?? ""}`}>{KIND_LABEL[f.kind] ?? f.kind}</span> {f.text} · {ago(f.createdAt)}
        </p>
      ))}
    </div>
  );
}
