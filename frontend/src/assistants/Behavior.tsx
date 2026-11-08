import { useCallback, useEffect, useState, type ReactNode } from "react";
import { assistants, type Proactivity } from "../mygo";

/**
 * Behavior, reduced to what a person decides (2026-11-07): one switch — may
 * this Assistant carry on by itself — and Quiet Hours. It always answers you
 * and does what you hand it. The switch sets the core's level (on = the
 * current level, or "follow up" when it was "only when I ask"; off = "only when
 * I ask"); reminder spacing and the daily budget keep the values they have, and
 * a routine you approved still runs on its schedule.
 */

type Act = (call: () => Promise<unknown>) => Promise<string>;

const rid = () => crypto.randomUUID();

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
  const run: Act = useCallback(async (call) => act(call).then((refusal) => (load(), refusal)), [act, load]);
  if (!p) return <p className="as-meta">{failed || "Loading…"}</p>;
  return <Form key={`${id}:${p.revision}`} id={id} p={p} run={run} />;
}

function Form({ id, p, run }: { id: string; p: Proactivity; run: Act }) {
  const [on, setOn] = useState(p.level !== "respond");
  const [quiet, setQuiet] = useState(p.quietStart !== "");
  const [start, setStart] = useState(p.quietStart || "22:00");
  const [end, setEnd] = useState(p.quietEnd || "08:00");
  const zone = p.tz || Intl.DateTimeFormat().resolvedOptions().timeZone;
  const next = {
    level: on ? (p.level === "respond" ? "follow_up" : p.level) : "respond",
    quietStart: quiet ? start : "",
    quietEnd: quiet ? end : "",
    tz: quiet ? zone : "",
    reportEveryMin: p.reportEveryMin,
    dailyBudget: p.dailyBudget,
  };
  const dirty = next.level !== p.level || next.quietStart !== p.quietStart || next.quietEnd !== p.quietEnd;
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>Work on its own</strong>
        {p.quietNow ? <span className="as-chip">quiet now</span> : null}
      </div>
      <label className="as-row">
        <input type="checkbox" checked={on} onChange={(e) => setOn(e.target.checked)} />
        <span>
          Carry on by itself
          <span className="as-meta"> — move approved goals forward and remind me when one stalls. Off: it only acts when I ask.</span>
        </span>
      </label>
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
      <div className="as-row end">
        <button type="button" className="as-btn primary" disabled={!dirty} onClick={() => run(() => assistants.saveBehavior(rid(), id, next, p.revision))}>
          Save
        </button>
      </div>
      <p className="as-meta">It always answers you and does what you hand it. A routine you approved still runs on its schedule.</p>
    </section>
  );
}
