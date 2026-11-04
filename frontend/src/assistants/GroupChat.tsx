import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { assistants, type AssistantsView, type GroupFeed, type GroupMember, type GroupMessage, type GroupTaskCard } from "../mygo";
import { Markdown } from "../turn";
import { Icon } from "../icons";
import { resolveMentions } from "./mentions";
import { ChatComposerBox, ChatComposerTextarea, ChatComposerToolbar, useComposerAutoSize } from "../composer-surface";

/**
 * A Group conversation (Assistants 3.1 U1). The plugin's message log is the
 * only source: this page renders it and writes back through the bridge with a
 * requestId, so a double Send replays instead of duplicating. Authors are
 * facts — the person, an Assistant that really ran an Agent for this Group, or
 * a receipt the core wrote (a fact row or a live task card) — and only the
 * person's exact `@Name` wakes an Assistant; a message with no mention calls
 * every Group member.
 */

const rid = () => crypto.randomUUID();
const POLL_MS = 1800;

const TONES = ["#0a84ff", "#30d158", "#ff9f0a", "#ff453a", "#ff375f", "#bf5af2", "#40c8e0", "#5e5ce6"];

/** fnv32a over the stable id, the same palette index the sidebar's Go avatar uses. */
function toneFor(id: string): string {
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(id)) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return TONES[h % TONES.length];
}

function initialOf(name: string): string {
  const t = name.trim();
  const m = t.match(/[\p{L}\p{N}]/u);
  if (m) return m[0].toUpperCase();
  const first = [...t].find((c) => c.trim() !== "");
  return first ?? "?";
}

export function Avatar({ id, name, size = 28 }: { id: string; name: string; size?: number }) {
  return (
    <span className="gc-avatar" style={{ width: size, height: size, background: toneFor(id), fontSize: size * 0.46 }} aria-hidden="true">
      {initialOf(name)}
    </span>
  );
}

export function GroupStack({ members, size = 28 }: { members: GroupMember[]; size?: number }) {
  const shown = members.slice(0, 3);
  return (
    <span className="gc-stack" style={{ height: size }} aria-hidden="true">
      {shown.map((m, i) => (
        <span key={m.assistantId} style={{ marginLeft: i === 0 ? 0 : -size * 0.28, zIndex: shown.length - i }}>
          <Avatar id={m.assistantId} name={m.name} size={size * 0.78} />
        </span>
      ))}
    </span>
  );
}

function msg(e: unknown): string {
  const text = e instanceof Error ? e.message : String(e);
  return text.replace(/^[a-z_]+: /, "");
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The body with every member mention drawn as a chip. */
function Body({ text, members, mine }: { text: string; members: GroupMember[]; mine: boolean }): ReactNode {
  if (!members.length) return <>{text}</>;
  const names = [...members].sort((a, b) => b.name.length - a.name.length).map((m) => escapeRe(m.name));
  const re = new RegExp(`(@(?:${names.join("|")})(?![\\p{L}\\p{N}_]))`, "gu");
  const parts = text.split(re);
  return (
    <>
      {parts.map((p, i) =>
        i % 2 === 1 ? (
          <span key={i} className={`gc-mention ${mine ? "mine" : ""}`}>
            {p}
          </span>
        ) : (
          <Fragment key={i}>{p}</Fragment>
        ),
      )}
    </>
  );
}

function factText(f: unknown, nameOf: (id: string) => string): string | null {
  const fact = (f ?? {}) as { event?: string; assistantId?: string; name?: string; reason?: string };
  const who = fact.name || nameOf(fact.assistantId ?? "");
  switch (fact.event) {
    case "agent_starting":
      return `${who} is starting…`;
    case "delivered":
      return null; // the chip under the message already says it
    case "delivery_failed":
      return `Couldn't reach ${who}. @ ${who} again to retry.`;
    case "delivery_uncertain":
      return `${who} may not have received that. @ ${who} again to retry.`;
    case "delivery_skipped":
      return `${who} was not reached (no longer in the group).`;
    case "assistant_disabled":
      return `${who} is paused and was not asked.`;
    case "agent_blocked":
      return fact.reason ? `${who} is waiting for you in its terminal: ${fact.reason}` : `${who} is waiting for you in its terminal.`;
    case "agent_failed":
      return `${who}'s agent stopped. @ ${who} again to start it.`;
    case "member_added":
      return `${who} joined`;
    case "member_removed":
      return `${who} left`;
  }
  return null;
}

const TASK_STATE: Record<string, string> = {
  draft: "Draft",
  waiting: "Waiting",
  queued: "Queued",
  claimed: "Starting",
  starting: "Starting",
  running: "Working",
  blocked: "Blocked",
  review_required: "Needs review",
  done: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

function useGroupFeed(id: string) {
  const [feed, setFeed] = useState<GroupFeed | null>(null);
  const [error, setError] = useState("");
  const pull = useCallback(() => {
    assistants
      .groupFeed(id, 0)
      .then((f) => {
        setFeed(f);
        setError("");
      })
      .catch((e) => setError(msg(e)));
  }, [id]);
  useEffect(() => {
    setFeed(null);
    pull();
    const timer = window.setInterval(() => !document.hidden && pull(), POLL_MS);
    const show = () => !document.hidden && pull();
    document.addEventListener("visibilitychange", show);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", show);
    };
  }, [pull]);
  return { feed, error, pull };
}

export function GroupChat({ view, id, refresh }: { view: AssistantsView; id: string; refresh: () => void }) {
  const { feed, error: feedError, pull } = useGroupFeed(id);
  const [draft, setDraft] = useState("");
  const [replyTo, setReplyTo] = useState<GroupMessage | null>(null);
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const [sheet, setSheet] = useState<"" | "rename" | "members">("");
  const [menu, setMenu] = useState(false);
  const [pop, setPop] = useState<{ query: string; index: number } | null>(null);
  // Members chosen from the popover: what tells two members of one name apart.
  const [picks, setPicks] = useState<GroupMember[]>([]);
  const request = useRef({ key: "", id: "" });
  const scroller = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLTextAreaElement>(null);
  const stick = useRef(true);
  const members = feed?.group.members ?? [];
  const nameOf = useCallback((aid: string) => members.find((m) => m.assistantId === aid)?.name ?? "An assistant", [members]);
  const byId = useMemo(() => new Map((feed?.messages ?? []).map((m) => [m.id, m])), [feed?.messages]);
  const tasks = useMemo(() => new Map((feed?.tasks ?? []).map((t) => [t.taskId, t])), [feed?.tasks]);
  const deliveries = useMemo(() => {
    const by = new Map<string, NonNullable<GroupFeed["deliveries"]>>();
    for (const d of feed?.deliveries ?? []) by.set(d.messageId, [...(by.get(d.messageId) ?? []), d]);
    return by;
  }, [feed?.deliveries]);

  useComposerAutoSize(field, draft);

  // Keep the end in view while the person is at the end; a reader who scrolled up is left alone.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [feed?.seq, feed?.agents]);
  const onScroll = () => {
    const el = scroller.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
  };
  useEffect(() => {
    setDraft("");
    setReplyTo(null);
    setError("");
    setPicks([]);
    stick.current = true;
  }, [id]);

  const candidates = useMemo(() => {
    if (!pop) return [];
    const q = pop.query.toLowerCase();
    return members.filter((m) => m.name.toLowerCase().includes(q));
  }, [pop, members]);

  const readPop = (text: string, caret: number) => {
    const m = text.slice(0, caret).match(/(?:^|[^\p{L}\p{N}_@])@([^\s@]*)$/u);
    setPop(m ? { query: m[1], index: 0 } : null);
  };
  const pick = (m: GroupMember) => {
    const el = field.current;
    if (!el) return;
    const caret = el.selectionStart;
    const before = draft.slice(0, caret).replace(/@[^\s@]*$/u, `@${m.name} `);
    const next = before + draft.slice(caret);
    setDraft(next);
    setPicks((all) => [...all, m]);
    setPop(null);
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(before.length, before.length);
    });
  };

  const send = async () => {
    const text = draft.trim();
    if (!text || sending) return;
    const { ids: mentions, ambiguous } = resolveMentions(text, members, picks);
    if (ambiguous.length) {
      setError(`More than one member is called ${ambiguous[0]} — pick which one from the list after typing @.`);
      return;
    }
    const key = `${text}\n${replyTo?.id ?? ""}\n${mentions.join(",")}`;
    if (request.current.key !== key) request.current = { key, id: rid() };
    setSending(true);
    setError("");
    try {
      await assistants.postGroupMessage(request.current.id, id, text, mentions, replyTo?.id ?? "");
      setDraft("");
      setPicks([]);
      setReplyTo(null);
      stick.current = true;
      pull();
      refresh();
    } catch (e) {
      setError(msg(e));
    }
    setSending(false);
  };

  const onKey = (e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (pop && candidates.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const d = e.key === "ArrowDown" ? 1 : -1;
        setPop({ ...pop, index: (pop.index + d + candidates.length) % candidates.length });
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        pick(candidates[pop.index]);
        return;
      }
    }
    if (e.key === "Escape") {
      if (pop) setPop(null);
      else if (replyTo) setReplyTo(null);
      return;
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    }
  };

  if (!feed) {
    return feedError ? (
      <div className="as-empty">
        <h2>This group can't be read</h2>
        <p>{feedError}</p>
      </div>
    ) : (
      <div className="as-boot" />
    );
  }
  const g = feed.group;
  const starting = feed.agents.filter((a) => a.state === "starting").map((a) => nameOf(a.assistantId));
  const blocked = feed.agents.filter((a) => a.state === "blocked");
  const waiting = (feed.deliveries ?? []).filter((d) => d.state === "queued" || d.state === "delivering").length;
  return (
    <div className="as gc">
      <header className="as-head">
        <GroupStack members={g.members} size={30} />
        <h1>{g.title}</h1>
        <span className="as-meta">{g.members.map((m) => m.name).join(", ")}</span>
        <span className="as-grow" />
        <div className="as-menu-wrap">
          <button type="button" className={`as-ib ${menu ? "on" : ""}`} aria-label="Actions" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((v) => !v)}>
            ⋯
          </button>
          {menu ? (
            <>
              <div className="as-menu-scrim" onMouseDown={() => setMenu(false)} />
              <div className="as-menu" role="menu">
                <button type="button" role="menuitem" onClick={() => (setMenu(false), setSheet("members"))}>
                  Members…
                </button>
                <button type="button" role="menuitem" onClick={() => (setMenu(false), setSheet("rename"))}>
                  Rename…
                </button>
              </div>
            </>
          ) : null}
        </div>
      </header>
      <div className="gc-list" ref={scroller} onScroll={onScroll}>
        {feed.messages.length === 0 ? (
          <div className="gc-hint">
            <p>
              Say something to the group. Type <kbd>@</kbd> and a name to ask one of them — only the people you @ are woken, and nobody is asked when you don't.
            </p>
          </div>
        ) : null}
        {feed.messages.map((m) => (
          <Row key={m.id} m={m} members={g.members} nameOf={nameOf} byId={byId} task={m.taskId ? tasks.get(m.taskId) : undefined} deliveries={deliveries.get(m.id)} onReply={() => (setReplyTo(m), field.current?.focus())} />
        ))}
        {blocked.length ? (
          <p className="gc-status">
            {blocked.map((a) => nameOf(a.assistantId)).join(", ")} {blocked.length === 1 ? "is" : "are"} waiting for you in {blocked.length === 1 ? "its" : "their"} terminal.{" "}
            {blocked[0].paneId ? (
              <button type="button" className="gc-link" onClick={() => void assistants.openPane(blocked[0].paneId ?? "")}>
                Open Terminal
              </button>
            ) : null}
          </p>
        ) : null}
        {starting.length ? <p className="gc-status">{starting.join(", ")} {starting.length === 1 ? "is" : "are"} starting…</p> : null}
        {!starting.length && waiting > 0 ? <p className="gc-status">Waiting for {waiting === 1 ? "a reply to be handed over" : `${waiting} messages to be handed over`}…</p> : null}
      </div>
      {feedError ? <p className="gc-warn">{feedError}</p> : null}
      <div className="composer">
       <div className="col">
        {replyTo ? (
          <div className="gc-replying">
            <span>
              Replying to <strong>{replyTo.authorKind === "user" ? "yourself" : nameOf(replyTo.authorId ?? "")}</strong>: {snippet(replyTo)}
            </span>
            <button type="button" className="as-ib" aria-label="Cancel reply" onClick={() => setReplyTo(null)}>
              ✕
            </button>
          </div>
        ) : null}
        {error ? (
          <p className="gc-error" role="alert">
            {error}
          </p>
        ) : null}
        <ChatComposerBox className="gc-cmp">
          {pop && candidates.length ? (
            <ul className="gc-pop" role="listbox" aria-label="Members">
              {candidates.map((m, i) => (
                <li key={m.assistantId} role="option" aria-selected={i === pop.index} className={i === pop.index ? "on" : ""} onMouseDown={(e) => (e.preventDefault(), pick(m))}>
                  <Avatar id={m.assistantId} name={m.name} size={18} />
                  <span>{m.name}</span>
                </li>
              ))}
            </ul>
          ) : null}
          <ChatComposerTextarea
            ref={field}
            value={draft}
            rows={1}
            placeholder={`Message ${g.title} — @ to ask someone`}
            onChange={(e) => {
              setDraft(e.target.value);
              readPop(e.target.value, e.target.selectionStart);
            }}
            onKeyDown={onKey}
            onClick={(e) => readPop(draft, e.currentTarget.selectionStart)}
            onBlur={() => setPop(null)}
          />
          <ChatComposerToolbar>
            <span className="sp" />
            <button type="button" className="send" title="Send" aria-label="Send" disabled={!draft.trim() || sending} onClick={() => void send()}>
              <Icon name="up" size={16} stroke={2.2} />
            </button>
          </ChatComposerToolbar>
        </ChatComposerBox>
       </div>
      </div>
      {sheet === "rename" ? <RenameGroup group={g} onClose={() => setSheet("")} done={() => (pull(), refresh())} /> : null}
      {sheet === "members" ? <Members group={g} all={view.assistants.map((a) => ({ assistantId: a.id, name: a.name, status: a.status }))} onClose={() => setSheet("")} done={() => (pull(), refresh())} /> : null}
    </div>
  );
}

function snippet(m: GroupMessage): string {
  const t = m.kind === "task" ? m.body : m.body.replace(/\s+/g, " ");
  return t.length > 80 ? `${t.slice(0, 79)}…` : t;
}

function Row({ m, members, nameOf, byId, task, deliveries, onReply }: { m: GroupMessage; members: GroupMember[]; nameOf: (id: string) => string; byId: Map<string, GroupMessage>; task?: GroupTaskCard; deliveries?: NonNullable<GroupFeed["deliveries"]>; onReply: () => void }) {
  if (m.kind === "fact") {
    const text = factText(m.fact, nameOf);
    return text ? <p className="gc-fact">{text}</p> : null;
  }
  const mine = m.authorKind === "user";
  const author = mine ? "You" : nameOf(m.authorId ?? "");
  const quoted = m.replyTo ? byId.get(m.replyTo) : undefined;
  if (m.kind === "task") {
    return (
      <div className="gc-row">
        <Avatar id={m.authorId ?? ""} name={author} />
        <div className="gc-col">
          <span className="gc-author">{author} filed a task</span>
          <div className={`gc-task ${task?.state ?? ""}`}>
            <strong>{task?.title ?? m.body}</strong>
            <span className="gc-pill">{TASK_STATE[task?.state ?? ""] ?? task?.state ?? "…"}</span>
            {task?.needsYou ? <span className="gc-pill warn">Needs you</span> : null}
          </div>
        </div>
      </div>
    );
  }
  return (
    <div className={`gc-row ${mine ? "mine" : ""}`}>
      {mine ? null : <Avatar id={m.authorId ?? ""} name={author} />}
      <div className="gc-col">
        {mine ? null : <span className="gc-author">{author}</span>}
        <div className="gc-bubble-wrap">
          <div className={`gc-bubble ${mine ? "mine" : ""}`}>
            {quoted ? (
              <div className="gc-quote">
                <strong>{quoted.authorKind === "user" ? "You" : nameOf(quoted.authorId ?? "")}</strong> {snippet(quoted)}
              </div>
            ) : null}
            {mine ? <Body text={m.body} members={members} mine /> : <Markdown text={m.body} />}
          </div>
          <button type="button" className="gc-reply" aria-label="Reply" onClick={onReply}>
            Reply
          </button>
        </div>
        {deliveries?.length ? (
          <div className="gc-deliv">
            {deliveries.map((d) => (
              <span key={d.id} className={`gc-chip ${d.state}`}>
                {deliveryMark(d.state)} {nameOf(d.assistantId)}
              </span>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function deliveryMark(state: string): string {
  switch (state) {
    case "delivered":
      return "✓";
    case "failed":
      return "✕";
    case "uncertain":
      return "?";
    case "skipped":
      return "–";
  }
  return "…";
}

function RenameGroup({ group, onClose, done }: { group: GroupFeed["group"]; onClose: () => void; done: () => void }) {
  const [title, setTitle] = useState(group.title);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const request = useRef({ key: "", id: "" });
  const submit = async () => {
    if (busy || !title.trim()) return;
    const key = title.trim();
    if (request.current.key !== key) request.current = { key, id: rid() };
    setBusy(true);
    setError("");
    try {
      await assistants.renameGroup(request.current.id, group.id, title, group.revision);
      done();
      onClose();
    } catch (e) {
      setError(msg(e));
      setBusy(false);
    }
  };
  return (
    <Dialog title="Rename group" submit="Rename" busy={busy || !title.trim()} error={error} onClose={onClose} onSubmit={() => void submit()}>
      <label className="as-field">
        <span>Title</span>
        <input autoFocus value={title} maxLength={80} onChange={(e) => setTitle(e.target.value)} />
      </label>
    </Dialog>
  );
}

function Members({ group, all, onClose, done }: { group: GroupFeed["group"]; all: GroupMember[]; onClose: () => void; done: () => void }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const inGroup = new Set(group.members.map((m) => m.assistantId));
  const run = async (aid: string, add: boolean) => {
    setBusy(true);
    setError("");
    try {
      await assistants.groupMember(rid(), group.id, aid, add);
      done();
    } catch (e) {
      setError(msg(e));
    }
    setBusy(false);
  };
  return (
    <Dialog title="Members" submit="Done" busy={false} error={error} onClose={onClose} onSubmit={onClose}>
      <ul className="gc-members">
        {all
          .filter((a) => inGroup.has(a.assistantId) || a.status === "active")
          .map((a) => (
            <li key={a.assistantId}>
              <Avatar id={a.assistantId} name={a.name} size={22} />
              <span className="gc-grow">{a.name}</span>
              {inGroup.has(a.assistantId) ? (
                <button type="button" className="as-btn" disabled={busy || group.members.length <= 2} title={group.members.length <= 2 ? "A group keeps at least two" : undefined} onClick={() => void run(a.assistantId, false)}>
                  Remove
                </button>
              ) : (
                <button type="button" className="as-btn" disabled={busy} onClick={() => void run(a.assistantId, true)}>
                  Add
                </button>
              )}
            </li>
          ))}
      </ul>
    </Dialog>
  );
}

/** The New Group sheet: choose at least two Assistants; a refusal keeps the choice and says why. */
export function NewGroup({ view, onClose, onCreated }: { view: AssistantsView; onClose: () => void; onCreated: () => void }) {
  const [title, setTitle] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const request = useRef({ key: "", id: "" });
  const usable = view.assistants.filter((a) => a.status === "active");
  const toggle = (aid: string) => setPicked((p) => (p.includes(aid) ? p.filter((x) => x !== aid) : [...p, aid]));
  const submit = async () => {
    if (busy || picked.length < 2) return;
    const key = `${title.trim()}\n${[...picked].sort().join(",")}`;
    if (request.current.key !== key) request.current = { key, id: rid() };
    setBusy(true);
    setError("");
    try {
      await assistants.createGroup(request.current.id, title, picked);
      onCreated();
      onClose();
    } catch (e) {
      setError(msg(e));
      setBusy(false);
    }
  };
  return (
    <Dialog title="New Group" submit={busy ? "Creating…" : "Create"} busy={busy || picked.length < 2} error={error} onClose={onClose} onSubmit={() => void submit()}>
      {usable.length < 2 ? (
        <p className="as-meta">A group needs at least two Assistants. Create another Assistant first.</p>
      ) : (
        <>
          <label className="as-field">
            <span>Title (optional)</span>
            <input value={title} maxLength={80} placeholder={picked.map((id) => usable.find((a) => a.id === id)?.name).join(", ")} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <div className="as-field">
            <span>Members — pick at least two</span>
            <ul className="gc-members">
              {usable.map((a) => (
                <li key={a.id}>
                  <label className="gc-pick">
                    <input type="checkbox" checked={picked.includes(a.id)} onChange={() => toggle(a.id)} />
                    <Avatar id={a.id} name={a.name} size={22} />
                    <span className="gc-grow">{a.name}</span>
                  </label>
                </li>
              ))}
            </ul>
          </div>
        </>
      )}
    </Dialog>
  );
}

function Dialog({ title, submit, busy, error, onClose, onSubmit, children }: { title: string; submit: string; busy: boolean; error?: string; onClose: () => void; onSubmit: () => void; children: ReactNode }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [onClose]);
  return (
    <div className="as-scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="as-sheet"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
      >
        <h2>{title}</h2>
        {children}
        {error ? (
          <p className="as-sheet-error" role="alert">
            {error}
          </p>
        ) : null}
        <div className="as-row end">
          <button type="button" className="as-btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="as-btn primary" disabled={busy}>
            {submit}
          </button>
        </div>
      </form>
    </div>
  );
}
