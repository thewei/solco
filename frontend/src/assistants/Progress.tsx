import type { OverviewRow } from "../mygo";

export function ago(ms: number): string {
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
  if (!row || row.tasks === 0) return null;
  const other = Math.max(0, row.tasks - row.queued - row.running - row.done);
  const parts = [
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
