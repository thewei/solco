import { useState } from "react";
import { projects, type ComposePane } from "../mygo";
import { Icon } from "../icons";
import { Segmented, Sheet } from "./ui";
import { run } from "./useProjects";

type Kind = "task" | "research" | "chat";

/** The New Task sheet: title, description, kind, After, Context chips and the brief preview. */
export function Compose({ compose }: { compose: ComposePane }) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [after, setAfter] = useState("");
  const [kind, setKind] = useState<Kind>("task");
  const [delegate, setDelegate] = useState(compose.delegate);
  const [attach, setAttach] = useState<Record<string, boolean>>({});
  // Files added through "Add files…" reach the list with the next snapshot.
  const items = compose.items;

  const close = () => run(() => projects.closeCompose());
  const ready = title.trim() !== "";
  const submit = () => {
    if (!ready) return;
    run(() =>
      projects.addTask({
        title, description, after, kind, delegate,
        attach: Object.keys(attach).filter((id) => attach[id]),
      }),
    );
  };
  const addFiles = async () => {
    try {
      const ids = await projects.pickFiles("task");
      if (ids?.length) setAttach((a) => ({ ...a, ...Object.fromEntries(ids.map((id) => [id, true])) }));
    } catch {
      // the native toast already said why
    }
  };

  const always = items.filter((i) => i.mode === "always" && !attach[i.id]).map((i) => i.name);
  const attached = items.filter((i) => attach[i.id]).map((i) => i.name);
  const onDemand = items.filter((i) => i.mode === "reference" && !attach[i.id]).length;

  return (
    <Sheet title="New Task" onClose={close} wide>
      <div className="pj-form">
        <label>
          <span>Title</span>
          <input autoFocus value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Title" onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && submit()} />
        </label>
        <label>
          <span>Description</span>
          <textarea rows={3} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Description (optional)" />
        </label>
        <div className="field">
          <span>Kind</span>
          <Segmented
            label="Kind"
            value={kind}
            onChange={setKind}
            options={[{ id: "task", label: "Task" }, { id: "research", label: "Research only" }, { id: "chat", label: "Chat only" }]}
          />
          <small>
            {kind === "research" ? "Changes nothing in the repo; the agent writes a report." : kind === "chat" ? "A conversation that ends when you say." : "Works in its own worktree and opens a pull request."}
          </small>
        </div>
        <label>
          <span>After</span>
          <input value={after} onChange={(e) => setAfter(e.target.value)} placeholder="Wait for tasks, e.g. r/12, r/13 (optional)" />
        </label>
        {compose.canAttach && kind !== "chat" ? (
          <div className="field">
            <span>Context</span>
            <div className="chips">
              {items.map((i) => (
                <button key={i.id} type="button" className={`chip${attach[i.id] ? " on" : ""}`} aria-pressed={!!attach[i.id]} onClick={() => setAttach((a) => ({ ...a, [i.id]: !a[i.id] }))}>
                  <Icon name={i.kind === "image" ? "image" : "read"} size={12} />
                  {i.name}
                  <em>{i.mode}</em>
                </button>
              ))}
              <button type="button" className="chip add" onClick={() => void addFiles()}>
                <Icon name="plus" size={12} /> Add files…
              </button>
            </div>
          </div>
        ) : null}
        <div className="pj-brief">
          <strong>What the agent will get</strong>
          <p>This task: {title.trim() || "(untitled)"}{description.trim() ? " + your description" : ""}</p>
          {attached.length ? <p>Attached: {attached.join(", ")}</p> : null}
          {always.length ? <p>Always in its instructions: {always.join(", ")}</p> : null}
          {onDemand > 0 ? <p>{onDemand} more file{onDemand === 1 ? "" : "s"} it can open from INDEX.md</p> : null}
          <p>Its access follows the plugin's worker access setting (Settings → Work Board).</p>
        </div>
        <label className="check">
          <input type="checkbox" checked={delegate} onChange={(e) => setDelegate(e.target.checked)} />
          Start an agent now
        </label>
      </div>
      <div className="pj-sheet-foot">
        <button type="button" className="pj-btn" onClick={close}>Cancel</button>
        <button type="button" className="pj-btn primary" disabled={!ready} onClick={submit}>Add</button>
      </div>
    </Sheet>
  );
}

/** Replace the description through the plugin; optionally tell the agent at work. */
export function EditDescription({ id, text, tellable, onClose }: { id: string; text: string; tellable: boolean; onClose: () => void }) {
  const [value, setValue] = useState(text);
  const [tell, setTell] = useState(tellable);
  const changed = value.trim() !== "" && value.trim() !== text.trim();
  return (
    <Sheet title="Edit Description" onClose={onClose}>
      <div className="pj-form">
        <label>
          <span>Description</span>
          <textarea autoFocus rows={8} value={value} onChange={(e) => setValue(e.target.value)} />
        </label>
        {tellable ? (
          <label className="check">
            <input type="checkbox" checked={tell} onChange={(e) => setTell(e.target.checked)} />
            Also tell the agent
            <small>An agent already at work does not re-read the description; telling it sends the new text.</small>
          </label>
        ) : null}
      </div>
      <div className="pj-sheet-foot">
        <button type="button" className="pj-btn" onClick={onClose}>Cancel</button>
        <button
          type="button"
          className="pj-btn primary"
          disabled={!changed}
          onClick={() => {
            run(() => projects.saveDescription(id, value, tell && tellable));
            onClose();
          }}
        >
          Save
        </button>
      </div>
    </Sheet>
  );
}
