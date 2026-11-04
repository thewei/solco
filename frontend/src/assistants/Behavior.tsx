import { useCallback, useEffect, useState, type ReactNode } from "react";
import { assistants, type Proactivity } from "../mygo";

/**
 * Behavior (Assistants 3.1 U5): what this Assistant may start on its own. It
 * answers you and does what you delegate whatever is chosen here. The level
 * governs only what Solco starts unasked: the next step of a Goal you
 * approved, a reminder that a Goal stalled, a heartbeat's model wake. Quiet
 * hours hold those back (they resume by themselves); the daily budget caps the
 * tasks started that way.
 */

type Act = (call: () => Promise<unknown>) => Promise<void>;

const rid = () => crypto.randomUUID();

const LEVELS: [string, string, string][] = [
  ["respond", "Only when I ask", "Starts nothing on its own and sends no reminders."],
  ["suggest", "Suggest", "Says what it would do next and when a Goal stalls, but starts nothing."],
  ["follow_up", "Follow up", "Carries your approved Goals forward and reminds you, within the limits below."],
];

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="as-field">
      <span>{label}</span>
      {children}
    </label>
  );
}

export function Behavior({ id, act }: { id: string; act: Act }) {
  const [p, setP] = useState<Proactivity | null>(null);
  const [failed, setFailed] = useState("");
  const load = useCallback(() => {
    assistants
      .behavior(id)
      .then((v) => {
        setP(v);
        setFailed("");
      })
      .catch((e) => setFailed(e instanceof Error ? e.message.replace(/^[a-z_]+: /, "") : String(e)));
  }, [id]);
  useEffect(() => {
    setP(null);
    load();
  }, [load]);
  const run: Act = useCallback(async (call) => act(call).then(load), [act, load]);
  if (!p) return <p className="as-meta">{failed || "Loading…"}</p>;
  return <Form key={`${id}:${p.revision}`} id={id} p={p} run={run} />;
}

function Form({ id, p, run }: { id: string; p: Proactivity; run: Act }) {
  const [level, setLevel] = useState(p.level);
  const [quiet, setQuiet] = useState(p.quietStart !== "");
  const [start, setStart] = useState(p.quietStart || "22:00");
  const [end, setEnd] = useState(p.quietEnd || "08:00");
  const [every, setEvery] = useState(String(p.reportEveryMin));
  const [budget, setBudget] = useState(String(p.dailyBudget));
  const zone = p.tz || Intl.DateTimeFormat().resolvedOptions().timeZone;
  const next = {
    level,
    quietStart: quiet ? start : "",
    quietEnd: quiet ? end : "",
    tz: quiet ? zone : "",
    reportEveryMin: Math.max(0, Math.min(1440, Number(every) || 0)),
    dailyBudget: Math.max(1, Math.min(100, Number(budget) || 1)),
  };
  const dirty = next.level !== p.level || next.quietStart !== p.quietStart || next.quietEnd !== p.quietEnd || next.reportEveryMin !== p.reportEveryMin || next.dailyBudget !== p.dailyBudget;
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>Proactivity</strong>
        {p.quietNow ? <span className="as-chip">quiet now</span> : null}
        <span className="as-grow" />
        <span className="as-meta">{p.dispatchToday} of {p.dailyBudget} started today</span>
      </div>
      <p className="as-meta">What this Assistant may start on its own. It always answers you and does the work you delegate.</p>
      {LEVELS.map(([v, title, hint]) => (
        <label key={v} className="as-row">
          <input type="radio" name={`level-${id}`} checked={level === v} onChange={() => setLevel(v)} />
          <span>
            <strong>{title}</strong>
            <span className="as-meta"> {hint}</span>
          </span>
        </label>
      ))}
      <label className="as-row">
        <input type="checkbox" checked={quiet} onChange={(e) => setQuiet(e.target.checked)} />
        <span>Quiet hours — nothing starts or reminds in this window</span>
      </label>
      {quiet ? (
        <div className="as-row">
          <Field label="From">
            <input type="time" value={start} onChange={(e) => setStart(e.target.value)} />
          </Field>
          <Field label="To">
            <input type="time" value={end} onChange={(e) => setEnd(e.target.value)} />
          </Field>
          <span className="as-meta">{zone}</span>
        </div>
      ) : null}
      <Field label="At most one stalled-goal reminder every (minutes, 0 = as they come due)">
        <input type="number" min={0} max={1440} value={every} onChange={(e) => setEvery(e.target.value)} />
      </Field>
      <Field label="Tasks it may start on its own per day">
        <input type="number" min={1} max={100} value={budget} onChange={(e) => setBudget(e.target.value)} />
      </Field>
      <div className="as-row end">
        <button type="button" className="as-btn primary" disabled={!dirty} onClick={() => run(() => assistants.saveBehavior(rid(), id, next, p.revision))}>
          Save
        </button>
      </div>
      <p className="as-meta">A routine you approved still runs on its schedule, and a model wake by the heartbeat still needs its own approval.</p>
    </section>
  );
}
