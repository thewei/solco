import { useState } from "react";
import { assistants, type TaskProof } from "../mygo";

const MISSING: Record<string, string> = {
  result_evidence: "No transcript message backs this result — only the agent's own note.",
  diff: "No evidence has been collected yet.",
};

/**
 * What backs a task (Gap Q04): the answer with where it was read from, its
 * sources (grounded or not), what the run did, a coding task's diff and checks
 * — or, plainly, what is missing. Read only when the person opens it.
 */
export function ProofBox({ taskId }: { taskId: string }) {
  const [proof, setProof] = useState<TaskProof | null>(null);
  const [open, setOpen] = useState(false);
  const [err, setErr] = useState("");
  const toggle = async () => {
    const next = !open;
    setOpen(next);
    if (!next) return;
    try {
      setProof(await assistants.proof(taskId));
      setErr("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <div className="as-proof">
      <button type="button" className="as-link" onClick={() => void toggle()}>
        {open ? "Hide proof" : "Show proof"}
      </button>
      {open && err ? <p className="as-meta">{err}</p> : null}
      {open && proof ? (
        <>
          <p className="as-meta">
            {proof.policy.checks.length > 0 ? `Checks from the Assistant: ${proof.policy.checks.join(" ; ")}` : "No checks are approved for this Assistant"}
            {proof.policy.requireChecks ? " · this task requires them" : ""}
            {proof.policy.mergeNeedsPerson ? " · merging needs you" : ""}
          </p>
          {proof.artifacts.length === 0 && proof.missing.length === 0 ? <p className="as-meta">Nothing to show yet.</p> : null}
          {proof.artifacts.map((a, i) => (
            <div className="as-artifact" key={`${a.kind}-${i}`}>
              <div className="as-row">
                <strong>{a.title}</strong>
                {a.state ? <span className={`as-chip ${a.state}`}>{a.state.replace("_", " ")}</span> : null}
              </div>
              {a.text ? <p className="as-text">{a.text}</p> : null}
              {(a.items ?? []).map((it) => (
                <p className="as-meta as-path" key={it.label}>
                  {it.state ? `${it.state} · ` : ""}
                  {it.label}
                </p>
              ))}
              {a.sourceRef ? (
                <p className="as-meta as-path" title={a.hash ? `sha256 ${a.hash}` : undefined}>
                  read from {a.sourceRef}
                </p>
              ) : null}
            </div>
          ))}
          {proof.missing.map((m) => (
            <p className="as-reason" key={m}>
              {MISSING[m] ?? m}
            </p>
          ))}
        </>
      ) : null}
    </div>
  );
}
