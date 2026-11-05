import { useCallback, useEffect, useState, type ReactNode } from "react";
import { assistants, type Budget, type Proactivity } from "../mygo";

/**
 * Behavior (Assistants 3.1 U5): what this Assistant may start on its own. It
 * answers you and does what you delegate whatever is chosen here. The level
 * governs only what Solco starts unasked: the next step of a Goal you
 * approved, a reminder that a Goal stalled, a heartbeat's model wake. Quiet
 * hours hold those back (they resume by themselves); the daily budget caps the
 * tasks started that way.
 */

type Act = (call: () => Promise<unknown>) => Promise<string>;

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
  const run: Act = useCallback(async (call) => act(call).then((refusal) => (load(), refusal)), [act, load]);
  if (!p) return <p className="as-meta">{failed || "Loading…"}</p>;
  return (
    <>
      <Form key={`${id}:${p.revision}`} id={id} p={p} run={run} />
      <Ledger id={id} spent={p.dispatchToday} />
    </>
  );
}

const KIND_NAME: Record<string, string> = { supervisor: "Next steps of Goals", trigger: "Event triggers", wake: "Heartbeat wakes" };
const REASON_TEXT: Record<string, string> = {
  budget: "today's budget was used up",
  level: "its level is set to not start things",
  quiet: "it was quiet hours",
  plan_tasks: "the Goal reached its own task budget",
  wake_budget: "the approved wake allowance was used up",
  trigger_cap: "the trigger reached its daily cap",
};

function at(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/**
 * Today's ledger (Gap A05): what the system started without being asked and
 * what it held back, with the reason — so a spent budget is something you can
 * see rather than a silent stop. Model tokens and money are not known to
 * Solco (the Agents run in their own panes), so they are shown as unmeasured
 * and never estimated.
 */
function Ledger({ id, spent }: { id: string; spent: number }) {
  const [b, setB] = useState<Budget | null>(null);
  useEffect(() => {
    let live = true;
    assistants
      .budget(id)
      .then((v) => live && setB(v))
      .catch(() => live && setB(null));
    return () => {
      live = false;
    };
  }, [id, spent]);
  if (!b) return null;
  const kinds = Object.entries(b.spent);
  return (
    <section className="as-card quiet">
      <div className="as-row">
        <strong>Today's ledger</strong>
        <span className="as-grow" />
        <span className="as-meta">
          {b.spentToday} of {b.dailyBudget} started on its own · {b.remaining} left
        </span>
      </div>
      {kinds.length === 0 && b.held.length === 0 ? <p className="as-meta">Nothing started on its own today, and nothing held back.</p> : null}
      {kinds.map(([k, n]) => (
        <span key={k} className="as-meta">
          {KIND_NAME[k] ?? k}: {n} started
        </span>
      ))}
      {b.held.map((h) => (
        <span key={`${h.kind}:${h.reason}`} className="as-meta">
          {KIND_NAME[h.kind] ?? h.kind}: held back{h.count > 1 ? ` ${h.count}×` : ""} — {REASON_TEXT[h.reason] ?? h.reason}
        </span>
      ))}
      {b.wake.approved ? (
        <span className="as-meta">
          Heartbeat wakes: {b.wake.usedToday} of {b.wake.budget} used
        </span>
      ) : null}
      {b.recent.length > 0 ? (
        <>
          <h3 className="as-sub">Latest</h3>
          {b.recent.map((e, i) => (
            <span key={`${e.at}:${i}`} className="as-meta">
              {at(e.at)} · {KIND_NAME[e.kind] ?? e.kind} · {e.outcome === "spent" ? "started" : `held (${REASON_TEXT[e.reason ?? ""] ?? e.reason})`}
            </span>
          ))}
        </>
      ) : null}
      <span className="as-meta">Model tokens and cost are not measured by Solco; nothing here is an estimate.</span>
    </section>
  );
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
