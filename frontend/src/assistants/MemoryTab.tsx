import { useCallback, useEffect, useState } from "react";
import { assistants, type NoteFile, type Notes } from "../mygo";

/**
 * Memory is two plain Markdown notes the Assistant reads together: yours
 * (USER.md) and its own (ASSISTANT.md — it writes down what it learns and tidies
 * the file, folding in what you wrote). There is no approval step and no editor
 * here: "Open in editor" hands the file to the person's own text editor (the
 * phone, which has none, edits in its app), and what they save shows up here at
 * the next read. The core refuses a stale write, so an edit in the editor and the
 * Assistant's tidy-up cannot overwrite each other unseen.
 */

/** Resolves with the refusal's words ("" when the write went through). */
type Act = (call: () => Promise<unknown>) => Promise<string>;

export function MemoryTab({ id, act }: { id: string; act: Act }) {
  const [notes, setNotes] = useState<Notes | null>(null);
  const [failed, setFailed] = useState("");
  const load = useCallback(() => {
    assistants
      .notes(id)
      .then((v) => {
        setNotes(v);
        setFailed("");
      })
      .catch((e) => setFailed(e instanceof Error ? e.message.replace(/^[a-z_]+: /, "") : String(e)));
  }, [id]);
  useEffect(() => {
    setNotes(null);
    load();
    const timer = window.setInterval(() => !document.hidden && load(), 5000);
    return () => window.clearInterval(timer);
  }, [load]);
  const run: Act = useCallback(async (call) => act(call).then((refusal) => (load(), refusal)), [act, load]);
  if (!notes) return <p className="as-meta">{failed || "Loading…"}</p>;
  return (
    <>
      <section className="as-card">
        <div className="as-row">
          <strong>Memory</strong>
          <span className="as-grow" />
          <button type="button" className="as-link" onClick={() => run(() => assistants.openMemoryFolder(id))}>
            Open folder
          </button>
        </div>
        <p className="as-meta">Two notes this Assistant reads together. It writes down what it learns on its own — no approval needed. Edit them in your own editor; keep passwords out.</p>
      </section>
      <NoteView id={id} which="user" title="Your notes" hint="Written by you. The Assistant reads them and never changes them." file={notes.user} run={run} />
      <NoteView id={id} which="assistant" title="Assistant's notes" hint="Written by the Assistant as it learns; you can edit them too." file={notes.assistant} run={run} />
    </>
  );
}

function NoteView({ id, which, title, hint, file, run }: { id: string; which: "user" | "assistant"; title: string; hint: string; file: NoteFile; run: Act }) {
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>{title}</strong>
        <span className="as-grow" />
        <button type="button" className="as-btn" onClick={() => run(() => assistants.openMemoryFile(id, which))}>
          Open in editor
        </button>
      </div>
      <p className="as-meta">{hint}</p>
      {file.text.trim() ? <p className="as-text as-note-preview">{file.text}</p> : <p className="as-meta">Nothing written yet.</p>}
    </section>
  );
}
