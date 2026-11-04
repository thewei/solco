import { useCallback, useEffect, useState, type ReactNode } from "react";
import { assistants, type MemoryItem, type MemoryReview, type MemoryVersion } from "../mygo";

/**
 * Memory (Assistants 3.1 U5). Only approved items reach the Assistant; a
 * candidate waits for your answer. You may write a fact down yourself (it is
 * approved at once), edit any item (every change is a version), forget it
 * (its text goes everywhere, versions and file included) and open the folder:
 * one Markdown file per item, editable in any editor — Solco checks an edit
 * before keeping it, and an edit made on an outdated file is kept as a
 * separate candidate beside the newer text, never over it.
 */

/** Resolves with the refusal's words ("" when the write went through): a draft is cleared only then. */
type Act = (call: () => Promise<unknown>) => Promise<string>;

const rid = () => crypto.randomUUID();

function when(ms: number): string {
  return ms ? new Date(ms).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
}

const AUTHOR: Record<string, string> = { user: "You", agent: "Assistant", files: "Folder edit", system: "Before history" };

export function MemoryTab({ id, act }: { id: string; act: Act }) {
  const [review, setReview] = useState<MemoryReview | null>(null);
  const [failed, setFailed] = useState("");
  const load = useCallback(() => {
    assistants
      .memory(id)
      .then((v) => {
        setReview(v);
        setFailed("");
      })
      .catch((e) => setFailed(e instanceof Error ? e.message.replace(/^[a-z_]+: /, "") : String(e)));
  }, [id]);
  useEffect(() => {
    setReview(null);
    load();
    const timer = window.setInterval(() => !document.hidden && load(), 5000);
    return () => window.clearInterval(timer);
  }, [load]);
  const run: Act = useCallback(async (call) => act(call).then((refusal) => (load(), refusal)), [act, load]);
  if (!review) return <p className="as-meta">{failed || "Loading…"}</p>;

  const candidates = review.items.filter((m) => m.state === "candidate");
  const approved = review.items.filter((m) => m.state === "approved");
  const dupIds = new Set(review.duplicates.flat());
  const stale = new Set(review.staleCandidates);
  return (
    <>
      <section className="as-card">
        <div className="as-row">
          <strong>Memory</strong>
          <span className="as-chip">
            {review.approved} of {review.max}
          </span>
          <span className="as-grow" />
          <button type="button" className="as-link" onClick={() => run(() => assistants.openMemoryFolder(id))}>
            Open folder
          </button>
        </div>
        <p className="as-meta">What this Assistant remembers about you and your work. Only what you approve is used. Files in the folder are plain text — keep passwords out.</p>
        <Add id={id} run={run} />
      </section>
      {review.conflicts.length > 0 ? (
        <p className="as-meta">
          {review.conflicts.length} edit{review.conflicts.length === 1 ? " was" : "s were"} made on an outdated file. Your text is kept below as a candidate next to the newer one — approve it, edit the other, or forget it.
        </p>
      ) : null}
      {candidates.length > 0 ? <h3 className="as-sub">Waiting for you · {candidates.length}</h3> : null}
      {candidates.map((m) => (
        <Item key={m.id} m={m} run={run} note={m.kind === "conflict" ? "Edit conflict" : stale.has(m.id) ? "Waiting a while" : ""} />
      ))}
      {approved.length > 0 ? <h3 className="as-sub">Remembered</h3> : null}
      {approved.map((m) => (
        <Item key={m.id} m={m} run={run} note={dupIds.has(m.id) ? "Looks like a duplicate" : ""} />
      ))}
      {review.items.length === 0 ? <p className="as-meta">Nothing remembered yet. Write something down, or ask the Assistant to remember it.</p> : null}
    </>
  );
}

function Add({ id, run }: { id: string; run: Act }) {
  const [text, setText] = useState("");
  return (
    <>
      <textarea className="as-input" rows={2} value={text} maxLength={8192} placeholder="Write something to remember…" onChange={(e) => setText(e.target.value)} />
      <div className="as-row end">
        <button
          type="button"
          className="as-btn primary"
          disabled={text.trim() === ""}
          onClick={() => run(() => assistants.addMemory(rid(), id, text.trim())).then((refusal) => refusal === "" && setText(""))}
        >
          Remember
        </button>
      </div>
    </>
  );
}

function Item({ m, run, note }: { m: MemoryItem; run: Act; note: string }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(m.content);
  const [versions, setVersions] = useState<MemoryVersion[] | null>(null);
  const [open, setOpen] = useState(false);
  const toggleHistory = () => {
    const next = !open;
    setOpen(next);
    if (next) void assistants.memoryHistory(m.id).then(setVersions).catch(() => setVersions([]));
  };
  useEffect(() => {
    if (open) void assistants.memoryHistory(m.id).then(setVersions).catch(() => undefined);
  }, [m.revision]); // eslint-disable-line react-hooks/exhaustive-deps
  const body: ReactNode = editing ? (
    <>
      <textarea className="as-input" rows={3} value={text} maxLength={8192} onChange={(e) => setText(e.target.value)} />
      <div className="as-row end">
        <button type="button" className="as-link" onClick={() => (setEditing(false), setText(m.content))}>
          Cancel
        </button>
        <button
          type="button"
          className="as-btn primary"
          disabled={text.trim() === "" || text.trim() === m.content.trim()}
          onClick={() => run(() => assistants.editMemory(rid(), m.id, m.revision, text.trim())).then((refusal) => refusal === "" && setEditing(false))}
        >
          Save
        </button>
      </div>
    </>
  ) : (
    <p className="as-text">{m.content}</p>
  );
  return (
    <section className="as-card quiet">
      {body}
      <div className="as-row">
        <span className="as-meta">
          {m.kind}
          {m.sourceRef ? ` · ${m.sourceRef}` : ""}
          {note ? ` · ${note}` : ""}
        </span>
        <span className="as-grow" />
        {m.state === "candidate" ? (
          <button type="button" className="as-btn" onClick={() => run(() => assistants.approveMemory(rid(), m.id, m.revision))}>
            Approve
          </button>
        ) : null}
        {!editing ? (
          <button type="button" className="as-link" onClick={() => setEditing(true)}>
            Edit
          </button>
        ) : null}
        <button type="button" className="as-link" onClick={toggleHistory}>
          History
        </button>
        <button type="button" className="as-link" onClick={() => run(() => assistants.forgetMemory(rid(), m.id, m.revision))}>
          Forget
        </button>
      </div>
      {open ? (
        <div>
          {(versions ?? []).map((v) => (
            <div key={v.revision} className="as-row" title={v.content}>
              <span className="as-meta">
                rev {v.revision} · {AUTHOR[v.author] ?? v.author} · {when(v.at)}
              </span>
              <span className="as-grow" />
              <span className="as-meta as-clip">{v.content}</span>
            </div>
          ))}
          {versions === null ? <p className="as-meta">Loading…</p> : null}
        </div>
      ) : null}
    </section>
  );
}
