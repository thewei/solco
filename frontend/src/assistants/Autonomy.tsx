import { useCallback, useEffect, useState } from "react";
import { assistants, type AssistantsAutonomy, type AssistantsAutosave, type AssistantsHookKey } from "../mygo";

/**
 * Autonomy (Gaps A05/A06/A09): what can wake this Assistant from outside and
 * which of its own task events start follow-up work. Both are the owner's:
 * a webhook key is yours to make and revoke, a trigger fires only after you
 * approved it (the card in Needs you) and can be paused or withdrawn here.
 *
 * "Accepted" on a delivery means recorded — never that anyone has looked at
 * it. The Assistant wakes only if its heartbeat rules, approval, quiet hours
 * and daily budget allow; the routing chip says what happened to each one.
 * The sender's own text is never shown or given to the model.
 */

type Act = (call: () => Promise<unknown>) => Promise<string>;

const rid = () => crypto.randomUUID();

function when(ms?: number): string {
  return ms ? new Date(ms).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
}

const ROUTING: Record<string, string> = {
  heartbeat: "Handed to its heartbeat",
  held: "Held (quiet hours or budget)",
  recorded: "Recorded only",
};

export function Autonomy({ id, act }: { id: string; act: Act }) {
  const [view, setView] = useState<AssistantsAutonomy | null>(null);
  const [failed, setFailed] = useState("");
  const [made, setMade] = useState<AssistantsHookKey | null>(null);
  const load = useCallback(() => {
    assistants
      .autonomy(id)
      .then((v) => {
        setView(v);
        setFailed("");
      })
      .catch((e) => setFailed(e instanceof Error ? e.message.replace(/^[a-z_]+: /, "") : String(e)));
  }, [id]);
  useEffect(() => {
    setView(null);
    setMade(null);
    load();
    const timer = window.setInterval(() => !document.hidden && load(), 5000);
    return () => window.clearInterval(timer);
  }, [load]);
  const run: Act = useCallback(async (call) => act(call).then((refusal) => (load(), refusal)), [act, load]);
  if (!view) return <p className="as-meta">{failed || "Loading…"}</p>;
  const live = view.keys.filter((k) => k.status === "active");
  return (
    <>
      <Autosave id={id} act={act} />
      <Webhook id={id} view={view} live={live.length} made={made} setMade={setMade} run={run} />
      <Triggers id={id} view={view} run={run} />
    </>
  );
}

function Webhook({ id, view, live, made, setMade, run }: { id: string; view: AssistantsAutonomy; live: number; made: AssistantsHookKey | null; setMade: (k: AssistantsHookKey | null) => void; run: Act }) {
  const [sources, setSources] = useState("");
  const names = sources
    .split(/[\s,]+/)
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  const url = view.endpoint ? view.endpoint + view.hookPath : "";
  const copy = (text: string) => void navigator.clipboard.writeText(text).catch(() => undefined);
  return (
    <>
      <section className="as-card">
        <div className="as-row">
          <strong>Webhook</strong>
          <span className="as-chip">{live} active key{live === 1 ? "" : "s"}</span>
        </div>
        <p className="as-meta">
          An outside service (CI, a repository, a script) can ask this Assistant to take a look at one of its own tasks or goals. A delivery is only recorded: the Assistant wakes just if its heartbeat, approval, quiet hours and daily budget allow. Whatever the sender writes is treated as data.
        </p>
        {url ? (
          <div className="as-row">
            <code className="as-text">POST {url}</code>
            <span className="as-grow" />
            <button type="button" className="as-link" onClick={() => copy(url)}>
              Copy address
            </button>
          </div>
        ) : (
          <p className="as-meta">Remote Access is off, so nothing can reach this Mac. Turn it on in Settings → Remote Access first (the path will be {view.hookPath}).</p>
        )}
        <p className="as-meta">
          Sign each request with the key's secret: header <code>X-Solco-Hook-Key</code> (key id), <code>X-Solco-Timestamp</code>, <code>X-Solco-Delivery</code> (unique per delivery) and <code>X-Solco-Signature</code> = sha256= HMAC over method, path, timestamp, delivery and the SHA-256 of the body.
        </p>
        {made ? (
          <div className="as-card quiet">
            <strong>Key made — copy the secret now</strong>
            <p className="as-meta">It is shown only this once and cannot be listed again.</p>
            <div className="as-row">
              <code className="as-text">{made.secret || "(already shown earlier)"}</code>
              <span className="as-grow" />
              {made.secret ? (
                <button type="button" className="as-btn" onClick={() => copy(made.secret)}>
                  Copy secret
                </button>
              ) : null}
              <button type="button" className="as-link" onClick={() => setMade(null)}>
                Done
              </button>
            </div>
            <span className="as-meta">Key id {made.key.id}</span>
          </div>
        ) : null}
        <div className="as-row">
          <input className="as-input" value={sources} placeholder="Sources it may speak for, e.g. github, ci" onChange={(e) => setSources(e.target.value)} />
          <button
            type="button"
            className="as-btn primary"
            disabled={names.length === 0 || names.length > 8 || live >= 3}
            title={live >= 3 ? "Three active keys at most — revoke one first" : undefined}
            onClick={() =>
              run(async () => {
                setMade(await assistants.createHookKey(rid(), id, names));
              }).then((refusal) => refusal === "" && setSources(""))
            }
          >
            Create key
          </button>
        </div>
      </section>
      {view.keys.map((k) => (
        <section key={k.id} className="as-card quiet">
          <div className="as-row">
            <strong>{k.sources.join(", ")}</strong>
            <span className={`as-chip ${k.status === "active" ? "active" : ""}`}>{k.status}</span>
            <span className="as-grow" />
            {k.status === "active" ? (
              <button type="button" className="as-link" onClick={() => run(() => assistants.revokeHookKey(rid(), id, k.id))}>
                Revoke
              </button>
            ) : null}
          </div>
          <span className="as-meta">
            {k.id} · made {when(k.createdAt)}
            {k.lastUsedAt ? ` · last used ${when(k.lastUsedAt)}` : " · never used"}
            {k.revokedAt ? ` · revoked ${when(k.revokedAt)}` : ""}
          </span>
        </section>
      ))}
      <h3 className="as-sub">Recent deliveries</h3>
      {view.events.length === 0 ? <p className="as-meta">Nothing received yet.</p> : null}
      {view.events.map((e) => (
        <section key={e.id} className="as-card quiet">
          <div className="as-row">
            <strong>{e.source}</strong>
            <span className="as-chip">{ROUTING[e.routing] ?? e.routing}</span>
            <span className="as-grow" />
            <span className="as-meta">{when(e.receivedAt)}</span>
          </div>
          {e.reason ? <p className="as-text">{e.reason}</p> : null}
          <span className="as-meta">{e.resourceKind ? `About ${e.resourceKind} ${e.resourceId}` : "No resource named"}</span>
        </section>
      ))}
    </>
  );
}

function Triggers({ id, view, run }: { id: string; view: AssistantsAutonomy; run: Act }) {
  return (
    <>
      <h3 className="as-sub">Event triggers · {view.triggers.filter((t) => t.state === "active").length} active</h3>
      <p className="as-meta">A trigger turns one of this Assistant's own task events into one follow-up task. The Assistant drafts it; it starts only after you approve it (Needs you), runs at most five times a day and obeys proactivity, quiet hours and the daily budget.</p>
      {view.triggers.length === 0 ? <p className="as-meta">No triggers.</p> : null}
      {view.triggers.map((t) => (
        <section key={t.id} className="as-card quiet">
          <div className="as-row">
            <strong>{t.name}</strong>
            <span className={`as-chip ${t.state === "active" ? "active" : ""}`}>{t.state}</span>
            <span className="as-grow" />
            {t.state === "active" ? (
              <button type="button" className="as-link" onClick={() => run(() => assistants.triggerAction(rid(), "pause", id, t.id, t.revision))}>
                Pause
              </button>
            ) : null}
            {t.state === "paused" ? (
              <button type="button" className="as-btn" onClick={() => run(() => assistants.triggerAction(rid(), "resume", id, t.id, t.revision))}>
                Resume
              </button>
            ) : null}
            {t.state !== "withdrawn" ? (
              <button type="button" className="as-link" onClick={() => run(() => assistants.triggerAction(rid(), "cancel", id, t.id, t.revision))}>
                Withdraw
              </button>
            ) : null}
          </div>
          <p className="as-text">
            When {t.taskId ? `task ${t.taskId}` : `a task titled “${t.titleContains}”`} {t.on.replace("task.", "is ")}, start “{t.title}”.
          </p>
          <span className="as-meta">
            {t.kind} · fired {t.firesTotal} time{t.firesTotal === 1 ? "" : "s"}
            {t.state === "draft" ? " · waiting for your approval in Needs you" : ""}
          </span>
        </section>
      ))}
    </>
  );
}

const KIND_LABEL: Record<string, string> = { fact: "Facts", preference: "Preferences" };

/**
 * Memory autosave (Gap A04): a standing permission, sealed by Solco's own owner
 * key, to keep this Assistant's proposed memories of chosen kinds without
 * asking each time. It is the only thing a grant does today. Everything the
 * Assistant could say is still screened for credentials, edits that come from
 * files and every other kind still wait for your yes, and each use leaves a
 * receipt. A grant expires and can be revoked at once.
 */
function Autosave({ id, act }: { id: string; act: Act }) {
  const [view, setView] = useState<AssistantsAutosave | null>(null);
  const [failed, setFailed] = useState("");
  const [kinds, setKinds] = useState<string[]>(["preference"]);
  const [perDay, setPerDay] = useState(5);
  const [days, setDays] = useState(30);
  const [note, setNote] = useState("");
  const load = useCallback(() => {
    assistants
      .autosave(id)
      .then((v) => {
        setView(v);
        setFailed("");
      })
      .catch((e) => setFailed(e instanceof Error ? e.message.replace(/^[a-z_]+: /, "") : String(e)));
  }, [id]);
  useEffect(() => {
    setView(null);
    load();
    const timer = window.setInterval(() => !document.hidden && load(), 5000);
    return () => window.clearInterval(timer);
  }, [load]);
  if (!view) return failed ? <p className="as-meta">{failed}</p> : null;
  const live = view.grants.filter((g) => g.status === "active");
  const toggle = (k: string) => setKinds((cur) => (cur.includes(k) ? cur.filter((x) => x !== k) : [...cur, k]));
  return (
    <>
      <section className="as-card">
        <div className="as-row">
          <strong>Keep memories without asking</strong>
          <span className="as-chip">{live.length} active</span>
          <span className="as-grow" />
          <span className="as-meta">Owner key {view.ownerKey === "ready" ? `…${view.fingerprint.slice(-6)}` : view.ownerKey === "none" ? "made on first use" : "not Solco's"}</span>
        </div>
        <p className="as-meta">
          A standing permission signed by Solco: memories this Assistant proposes in the kinds you choose are kept at once. Credentials are still refused, memory edited from files and any other kind still wait for you, every use leaves a receipt, and the permission expires.
        </p>
        {view.ownerKey === "foreign" ? (
          <p className="as-meta">Another owner key is registered for these Assistants, so Solco cannot sign a permission for them.</p>
        ) : (
          <div className="as-row">
            {view.kinds.map((k) => (
              <label key={k} className="as-row">
                <input type="checkbox" checked={kinds.includes(k)} onChange={() => toggle(k)} />
                <span>{KIND_LABEL[k] ?? k}</span>
              </label>
            ))}
            <select className="as-input" value={perDay} onChange={(e) => setPerDay(Number(e.target.value))} aria-label="Daily limit">
              {[1, 5, 20, 0].map((n) => (
                <option key={n} value={n}>
                  {n === 0 ? "No daily limit" : `${n} a day`}
                </option>
              ))}
            </select>
            <select className="as-input" value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label="Lasts">
              {[7, 30, 90].map((n) => (
                <option key={n} value={n}>
                  {n} days
                </option>
              ))}
            </select>
            <button
              type="button"
              className="as-btn primary"
              disabled={kinds.length === 0}
              onClick={() =>
                act(() => assistants.enableAutosave(rid(), id, kinds, perDay, days)).then((refusal) => {
                  setNote(refusal);
                  load();
                })
              }
            >
              Allow
            </button>
          </div>
        )}
        {note ? <p className="as-meta">{note}</p> : null}
      </section>
      {view.grants.map((g) => (
        <section key={g.id} className="as-card quiet">
          <div className="as-row">
            <strong>{(g.scope.kind ?? []).map((k) => KIND_LABEL[k] ?? k).join(", ")}</strong>
            <span className={`as-chip ${g.status === "active" ? "active" : ""}`}>{g.status}</span>
            <span className="as-grow" />
            {g.status === "active" ? (
              <button type="button" className="as-link" onClick={() => act(() => assistants.revokeAutosave(rid(), id, g.id)).then(() => load())}>
                Revoke
              </button>
            ) : null}
          </div>
          <span className="as-meta">
            {g.maxPerDay ? `${g.maxPerDay} a day` : "no daily limit"} · until {when(g.expiresAt)}
            {g.status === "active" && !g.verified ? " · not verified by the core — nothing is saved automatically" : ""}
          </span>
        </section>
      ))}
    </>
  );
}
