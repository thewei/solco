import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Icon } from "../icons";
import { feedback, type Answer, type Ask, type FeedbackView, type Question, type Thread, type ThreadSummary } from "../mygo";
import { ConnectDialog } from "./Connect";
import "./feedback.css";

/**
 * The Feedback inbox (decision 2026-11-09, design: designs/feedback-inbox, direction A): the questions
 * Agents outside Solco put to the user through the feedback MCP tool. Threads on the left (the broker groups
 * one Agent's asks in one project), one thread's timeline on the right with the answer form as its last
 * item. The page reads one bridge snapshot; the broker owns every thread and Ask, nothing is cached or
 * recomputed here. On a narrow screen (the phone) the same page shows the list, then the thread.
 */

type Lens = "needs" | "all";
type Draft = { selected: string[]; custom: string };
type Drafts = Record<string, Record<string, Draft>>;

/** Drafts outlive polls and thread switches; they are page memory only and never leave the WebView. */
const drafts: Drafts = {};
const GAP_MIN_MS = 10 * 60_000;

const POLL_MS = 1500;
const narrowQuery = window.matchMedia("(max-width: 700px)");
/** The phone has no Mac path to hand out, so it never draws "Connect an Agent". */
const isPhone = document.documentElement.dataset.host === "phone";

type ProjectCount = { name: string; count: number; waiting: number };

function useFeedback(selected: string) {
  const [view, setView] = useState<FeedbackView | null>(null);
  const [error, setError] = useState("");
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const [focus, setFocus] = useState<{ thread: string; ask: string; n: number } | null>(null);
  const seq = useRef(0);
  const pull = useCallback(() => {
    feedback
      .snapshot(selectedRef.current)
      .then((v) => {
        setView(v);
        setError("");
        if (v.focus) setFocus({ ...v.focus, n: ++seq.current });
      })
      .catch((e) => setError(msg(e)));
  }, []);
  useEffect(() => {
    pull();
    const timer = window.setInterval(() => !document.hidden && pull(), POLL_MS);
    const show = () => !document.hidden && pull();
    document.addEventListener("visibilitychange", show);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", show);
    };
  }, [pull]);
  useEffect(() => {
    if (view?.theme) document.documentElement.dataset.theme = view.theme;
  }, [view?.theme]);
  return { view, error, setError, pull, focus };
}

function msg(e: unknown): string {
  const text = e instanceof Error ? e.message : String(e);
  return text.replace(/^[a-z_]+: /, "");
}

function useNarrow() {
  const [narrow, setNarrow] = useState(narrowQuery.matches);
  useEffect(() => {
    const on = () => setNarrow(narrowQuery.matches);
    narrowQuery.addEventListener("change", on);
    return () => narrowQuery.removeEventListener("change", on);
  }, []);
  return narrow;
}

const two = (n: number) => String(n).padStart(2, "0");
function clock(ms: number): string {
  const d = new Date(ms);
  const now = new Date();
  const time = `${two(d.getHours())}:${two(d.getMinutes())}`;
  if (d.toDateString() === now.toDateString()) return time;
  return `${d.toLocaleDateString(undefined, { month: "short", day: "numeric" })}, ${time}`;
}
function dayLabel(ms: number): string {
  const d = new Date(ms);
  const today = new Date();
  const yesterday = new Date(today.getTime() - 86_400_000);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
function span(ms: number): string {
  const min = Math.round(ms / 60_000);
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  return min % 60 ? `${h} h ${min % 60} min` : `${h} h`;
}
function took(ask: Ask): string {
  if (!ask.resolvedAt) return "";
  const s = Math.max(1, Math.round((ask.resolvedAt - ask.createdAt) / 1000));
  return s < 90 ? `${s} s` : span(ask.resolvedAt - ask.createdAt);
}
/** md renders the one inline markup a question uses: `code`. */
function md(text: string) {
  return text.split(/(`[^`]+`)/g).map((part, i) =>
    part.startsWith("`") && part.endsWith("`") && part.length > 2 ? <code key={i}>{part.slice(1, -1)}</code> : part,
  );
}
function answerText(a?: Answer): string {
  if (!a) return "";
  return [...(a.selected ?? []), a.custom ?? ""].filter(Boolean).join(", ");
}
function glyphOf(client?: string): string {
  const name = (client ?? "").trim();
  if (!name) return "A";
  const words = name.split(/\s+/);
  return (words.length > 1 ? words[0][0] + words[1][0] : name.slice(0, 2)).replace(/^./, (c) => c.toUpperCase());
}

export function FeedbackPage() {
  const [selected, setSelected] = useState("");
  const [lens, setLens] = useState<Lens | null>(null);
  const [project, setProject] = useState("");
  const [screen, setScreen] = useState<"list" | "thread">("list");
  const [connect, setConnect] = useState(false);
  const narrow = useNarrow();
  const { view, error, setError, pull, focus } = useFeedback(selected);
  const [flash, setFlash] = useState<{ ask: string; n: number } | null>(null);

  // A notification or link landing: show that thread, in All (the asked-for item may be answered), and scroll to it.
  useEffect(() => {
    if (!focus) return;
    setSelected(focus.thread);
    setLens("all");
    setProject("");
    setScreen("thread");
    setFlash({ ask: focus.ask, n: focus.n });
  }, [focus]);

  const waiting = view?.waiting ?? 0;
  // The default lens follows the inbox: Needs you while something waits, else everything.
  const activeLens: Lens = lens ?? (waiting > 0 ? "needs" : "all");
  const threads = view?.threads ?? [];
  // Projects of the threads this lens shows, most waiting first, then by name: the filter's menu can be long.
  const projects = useMemo(() => {
    const byName = new Map<string, ProjectCount>();
    for (const t of threads) {
      if (activeLens === "needs" && t.waiting === 0) continue;
      const p = byName.get(t.project) ?? { name: t.project, count: 0, waiting: 0 };
      p.count++;
      p.waiting += t.waiting;
      byName.set(t.project, p);
    }
    return Array.from(byName.values()).sort((a, b) => b.waiting - a.waiting || a.name.localeCompare(b.name));
  }, [threads, activeLens]);
  const visible = threads.filter((t) => (activeLens === "all" || t.waiting > 0) && (!project || t.project === project));
  const current = view?.selected && view.detail ? view.detail : null;
  // With nothing chosen yet, the first visible thread is the one shown.
  useEffect(() => {
    if (!view?.online || selected || !visible.length || narrow) return;
    setSelected(visible[0].id);
  }, [view?.online, selected, visible, narrow]);

  const open = (id: string) => {
    setSelected(id);
    setScreen("thread");
    setFlash(null);
  };

  if (!view) return <div className="fb-boot">{error && <p className="fb-note">{error}</p>}</div>;

  const list = (
    <section className="fb-list">
      <div className="fb-lens">
        <div className="fb-seg" role="tablist">
          <button role="tab" aria-selected={activeLens === "needs"} className={activeLens === "needs" ? "on" : ""} onClick={() => setLens("needs")}>
            Needs you {waiting > 0 && <b>{waiting}</b>}
          </button>
          <button role="tab" aria-selected={activeLens === "all"} className={activeLens === "all" ? "on" : ""} onClick={() => setLens("all")}>
            All
          </button>
        </div>
      </div>
      {(projects.length > 1 || project) && <ProjectFilter projects={projects} value={project} onChange={setProject} />}
      <div className="fb-rows">
        {visible.length === 0 && (
          <p className="fb-quiet">{activeLens === "needs" ? "Nothing is waiting. Switch to All to read earlier threads." : "No threads yet."}</p>
        )}
        <ThreadRows threads={visible} selected={narrow ? "" : selected} onOpen={open} />
      </div>
      {!isPhone && (
        <div className="fb-foot">
          <button className="fb-connect" onClick={() => setConnect(true)}>
            <Icon name="plus" size={14} />
            Connect an Agent
          </button>
        </div>
      )}
    </section>
  );

  const detail = current ? (
    <ThreadView
      key={current.id}
      thread={current}
      online={view.online}
      canOpenGit={view.canOpenGit}
      flash={flash}
      onBack={narrow ? () => setScreen("list") : undefined}
      onChanged={pull}
      onError={setError}
    />
  ) : (
    <section className="fb-detail">
      <div className="fb-empty">
        <h2>{threads.length ? "Pick a thread" : "No feedback yet"}</h2>
        {!threads.length && <p>Agents that call the <code>interactive_feedback</code> MCP tool show up here, one thread per project and run.</p>}
      </div>
    </section>
  );

  return (
    <div className={`fb${narrow ? " narrow" : ""}`}>
      {!view.online && <Offline onRetry={pull} />}
      {error && (
        <div className="fb-banner warn">
          <span>{error}</span>
          <button onClick={() => setError("")}>Dismiss</button>
        </div>
      )}
      {view.online && threads.length === 0 ? (
        <Empty onConnect={isPhone ? undefined : () => setConnect(true)} />
      ) : narrow ? (
        screen === "thread" && current ? detail : list
      ) : (
        <div className="fb-wrap">
          {list}
          {detail}
        </div>
      )}
      {connect && <ConnectDialog bin={view.mcpBin ?? ""} onClose={() => setConnect(false)} />}
    </div>
  );
}

/** ProjectFilter narrows the list to one project: a menu rather than chips, since the projects can be many. */
function ProjectFilter({ projects, value, onChange }: { projects: ProjectCount[]; value: string; onChange: (p: string) => void }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const root = useRef<HTMLDivElement>(null);
  const searchable = projects.length > 8;

  useEffect(() => {
    if (!open) return;
    const down = (e: MouseEvent) => root.current && !root.current.contains(e.target as Node) && setOpen(false);
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", down);
    window.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key);
    };
  }, [open]);

  const shown = q.trim() ? projects.filter((p) => p.name.toLowerCase().includes(q.trim().toLowerCase())) : projects;
  const pick = (name: string) => {
    onChange(name);
    setOpen(false);
    setQ("");
  };
  // A chosen project the lens no longer lists (it has nothing waiting) stays in the menu so it can be cleared.
  const current = value ? projects.find((p) => p.name === value) : undefined;

  return (
    <div className="fb-filter" ref={root}>
      <button className={`fb-fbtn${value ? " on" : ""}`} aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <span className="fb-fl">{value || "All projects"}</span>
        {value ? <span className="fb-n">{current?.count ?? 0}</span> : <span className="fb-n">{projects.length}</span>}
        <span className="fb-down">
          <Icon name="chev" size={14} />
        </span>
      </button>
      {open && (
        <div className="fb-pop" role="listbox">
          {searchable && <input className="fb-fsearch" autoFocus placeholder="Find a project" value={q} onChange={(e) => setQ(e.target.value)} />}
          <div className="fb-plist">
            {!q.trim() && (
              <button role="option" aria-selected={!value} className={`fb-pitem${!value ? " on" : ""}`} onClick={() => pick("")}>
                <span className="fb-fl">All projects</span>
                {!value && <Icon name="check" size={14} />}
              </button>
            )}
            {value && !current && !q.trim() && (
              <button role="option" aria-selected className="fb-pitem on" onClick={() => pick(value)}>
                <span className="fb-fl">{value}</span>
                <Icon name="check" size={14} />
              </button>
            )}
            {shown.map((p) => (
              <button key={p.name} role="option" aria-selected={value === p.name} className={`fb-pitem${value === p.name ? " on" : ""}`} onClick={() => pick(p.name)}>
                <span className="fb-fl">{p.name}</span>
                {p.waiting > 0 && <span className="fb-dot" aria-label="waiting" />}
                <span className="fb-n">{p.count}</span>
                {value === p.name && <Icon name="check" size={14} />}
              </button>
            ))}
            {shown.length === 0 && <p className="fb-quiet">No project matches.</p>}
          </div>
        </div>
      )}
    </div>
  );
}

function ThreadRows({ threads, selected, onOpen }: { threads: ThreadSummary[]; selected: string; onOpen: (id: string) => void }) {
  let day = "";
  return (
    <>
      {threads.map((t) => {
        const label = dayLabel(t.lastAt);
        const head = label !== day ? <div className="fb-gh">{label}</div> : null;
        day = label;
        return (
          <div key={t.id}>
            {head}
            <button className={`fb-row${selected === t.id ? " on" : ""}`} onClick={() => onOpen(t.id)}>
              <span className="fb-glyph">{glyphOf(t.client)}</span>
              <span className="fb-mid">
                <span className="fb-l1">
                  <span className="fb-t">{(t.waiting > 0 && t.preview ? t.preview : t.title).replace(/`/g, "")}</span>
                  <span className="fb-time">{clock(t.lastAt).replace(/^.*, /, "")}</span>
                </span>
                <span className="fb-l2">
                  <span className="fb-s">
                    {t.project}
                    {t.branch ? `, ${t.branch}` : ""}
                  </span>
                  {t.waiting > 0 ? <span className="fb-dot" aria-label="waiting" /> : <span className="fb-n">{t.askCount === 1 ? "1 ask" : `${t.askCount} asks`}</span>}
                </span>
              </span>
            </button>
          </div>
        );
      })}
    </>
  );
}

function ThreadView(props: {
  thread: Thread;
  online: boolean;
  canOpenGit: boolean;
  flash: { ask: string; n: number } | null;
  onBack?: () => void;
  onChanged: () => void;
  onError: (m: string) => void;
}) {
  const { thread, online, canOpenGit, flash, onBack, onChanged, onError } = props;
  const asks = (thread.asks ?? []).filter((a): a is Ask => !!a);
  const scroller = useRef<HTMLDivElement>(null);
  const [menu, setMenu] = useState(false);
  const lastPending = [...asks].reverse().find((a) => a.state === "pending")?.id ?? "";

  // Land on the asked-for item, else on the newest end of the timeline, once per thread / landing.
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const target = flash?.ask ? el.querySelector<HTMLElement>(`[data-ask="${CSS.escape(flash.ask)}"]`) : null;
    if (target) target.scrollIntoView({ block: "center" });
    else el.scrollTop = el.scrollHeight;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [thread.id, flash?.n]);

  const openProject = (target: "git" | "finder") => {
    setMenu(false);
    feedback.openProject(thread.id, target).catch((e) => onError(msg(e)));
  };

  return (
    <section className="fb-detail">
      <header className="fb-head">
        <div className="fb-headrow">
          {onBack && (
            <button className="fb-back" aria-label="Back to threads" onClick={onBack}>
              <span className="fb-flip">
                <Icon name="chev" size={16} />
              </span>
            </button>
          )}
          <div className="fb-title">{thread.title}</div>
          {thread.projectRoot && (
            <div className="fb-menuwrap">
              <button className="fb-ghost" onClick={() => setMenu((m) => !m)} aria-haspopup="menu" aria-expanded={menu}>
                Open project
              </button>
              {menu && (
                <div className="fb-menu" role="menu" onMouseLeave={() => setMenu(false)}>
                  {canOpenGit && (
                    <button role="menuitem" onClick={() => openProject("git")}>
                      Open in Solco Git
                    </button>
                  )}
                  <button role="menuitem" onClick={() => openProject("finder")}>
                    Reveal in Finder
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
        <div className="fb-meta">
          <span className="fb-chip">{thread.project}</span>
          {thread.branch && (
            <span className="fb-chip">
              {thread.branch}
              {thread.worktree ? ", worktree" : ""}
            </span>
          )}
          {thread.client && <span className="fb-chip">{thread.client}</span>}
          <span className="fb-chip">{asks.length} asks</span>
        </div>
      </header>
      <div className="fb-scroll" ref={scroller}>
        <div className="fb-tl">
          {asks.map((ask, n) => {
            const prev = n ? asks[n - 1] : null;
            const gap = prev ? ask.createdAt - (prev.resolvedAt || prev.createdAt) : 0;
            return (
              <div className="fb-it" key={ask.id} data-ask={ask.id}>
                {gap > GAP_MIN_MS && <div className="fb-gap">Quiet for {span(gap)}</div>}
                <AskItem
                  ask={ask}
                  thread={thread}
                  online={online}
                  primary={ask.id === lastPending}
                  onChanged={onChanged}
                  onError={onError}
                />
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}

function AskItem(props: { ask: Ask; thread: Thread; online: boolean; primary: boolean; onChanged: () => void; onError: (m: string) => void }) {
  const { ask, thread, online, primary, onChanged, onError } = props;
  const [late, setLate] = useState(false);
  const who = thread.client || "Agent";
  const asked = (
    <div className="fb-ask">
      <div className="fb-who">
        <span className="fb-glyph sm">{glyphOf(thread.client)}</span>
        <span>
          <b>{who}</b> · {clock(ask.createdAt)}
        </span>
      </div>
      {ask.description && <div className="fb-desc">{md(ask.description)}</div>}
      {ask.state !== "pending" && ask.questions.map((q) => <div className="fb-qt" key={q.id}>{md(q.text)}</div>)}
    </div>
  );
  if (ask.state === "pending") {
    return (
      <div className="fb-form-wrap">
        <AnswerForm ask={ask} thread={thread} online={online} primary={primary} onChanged={onChanged} onError={onError} />
      </div>
    );
  }
  let reply;
  switch (ask.state) {
    case "answered":
      reply = (
        <div className="fb-reply">
          <div className="fb-bub">
            {ask.questions
              .map((q) => answerText(ask.answers?.[q.id]))
              .filter(Boolean)
              .join("\n")}
          </div>
          <small>
            {ask.late ? "Late answer" : took(ask)}
            {ask.late && !ask.lateDelivered ? ", reaches the agent with its next question" : ""}
            {ask.attachmentCount ? `, ${ask.attachmentCount} image${ask.attachmentCount > 1 ? "s" : ""}` : ""}
          </small>
        </div>
      );
      break;
    case "ended":
      reply = (
        <div className="fb-reply">
          <div className="fb-bub none">Ended without an answer</div>
        </div>
      );
      break;
    default:
      reply = (
        <div className="fb-reply">
          <div className="fb-bub none">
            {ask.message || "No answer, the agent stopped waiting"}
            {!late && (
              <button className="fb-link" onClick={() => setLate(true)}>
                Answer anyway
              </button>
            )}
          </div>
        </div>
      );
  }
  return (
    <>
      {asked}
      {reply}
      {ask.state === "expired" && late && (
        <div className="fb-form-wrap">
          <AnswerForm ask={ask} thread={thread} online={online} primary={false} lateMode onChanged={onChanged} onError={onError} />
        </div>
      )}
    </>
  );
}

function AnswerForm(props: {
  ask: Ask;
  thread: Thread;
  online: boolean;
  primary: boolean;
  lateMode?: boolean;
  onChanged: () => void;
  onError: (m: string) => void;
}) {
  const { ask, thread, online, primary, lateMode, onChanged, onError } = props;
  const [, bump] = useState(0);
  const [sending, setSending] = useState(false);
  const draft = (drafts[ask.id] ||= {});
  const get = (q: Question): Draft => (draft[q.id] ||= { selected: [], custom: "" });
  const ready = ask.questions.every((q) => !q.required || get(q).selected.length > 0 || get(q).custom.trim() !== "");
  const set = (fn: () => void) => {
    fn();
    bump((n) => n + 1);
  };
  const pick = (q: Question, option: string) =>
    set(() => {
      const d = get(q);
      d.custom = "";
      if (q.type === "multiple") d.selected = d.selected.includes(option) ? d.selected.filter((o) => o !== option) : [...d.selected, option];
      else d.selected = [option];
    });

  const send = useCallback(() => {
    if (sending || !online || !ready) return;
    setSending(true);
    const answers: Record<string, Answer> = {};
    for (const q of ask.questions) {
      const d = drafts[ask.id]?.[q.id];
      if (d && (d.selected.length || d.custom.trim())) answers[q.id] = { selected: d.selected, custom: d.custom.trim() };
    }
    feedback
      .answer(ask.id, answers, [])
      .then(() => {
        delete drafts[ask.id];
        onChanged();
      })
      .catch((e) => onError(msg(e)))
      .finally(() => setSending(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ask.id, sending, online, ready, onChanged, onError]);

  // Keys, for the newest waiting form only: 1-9 pick an option of the first choice question, ⌘↵ sends.
  useEffect(() => {
    if (!primary) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        send();
        return;
      }
      const el = e.target as HTMLElement;
      if (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || e.metaKey || e.ctrlKey || e.altKey) return;
      const n = Number(e.key);
      if (!(n >= 1 && n <= 9)) return;
      const q = ask.questions.find((x) => x.type !== "text" && (x.options?.length ?? 0) >= n);
      if (q?.options) {
        e.preventDefault();
        pick(q, q.options[n - 1]);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [primary, ask.id, send]);

  const hint = !online
    ? "Feedback service is offline."
    : lateMode
      ? "The agent stopped waiting. This reaches it with its next question."
      : ask.disconnected
        ? "The agent isn't listening right now. Your answer is kept for when it asks again."
        : "Goes straight back to the agent.";

  return (
    <div className="fb-form" data-form={ask.id}>
      <div className="fb-nh">
        <span className="fb-glyph sm">{glyphOf(thread.client)}</span>
        <span>
          <b>{thread.client || "Agent"}</b> · {clock(ask.createdAt)} · {lateMode ? "expired" : "waiting for you"}
        </span>
      </div>
      {ask.description && <div className="fb-desc">{md(ask.description)}</div>}
      {ask.questions.map((q) => {
        const d = get(q);
        return (
          <div key={q.id}>
            <div className="fb-qt">{md(q.text)}</div>
            {q.type === "text" ? (
              <textarea
                aria-label={q.text}
                placeholder={q.placeholder || "Type an answer"}
                value={d.custom}
                onChange={(e) => set(() => (d.custom = e.target.value))}
              />
            ) : (
              <div className="fb-opts">
                {(q.options ?? []).map((o, k) => (
                  <button key={o} className={`fb-opt${d.selected.includes(o) ? " on" : ""}`} aria-pressed={d.selected.includes(o)} onClick={() => pick(q, o)}>
                    <kbd>{k + 1}</kbd>
                    <span>{o}</span>
                  </button>
                ))}
                {q.allowCustom && (
                  <input
                    className="fb-other"
                    aria-label={`${q.text} (something else)`}
                    placeholder="Something else…"
                    value={d.custom}
                    onChange={(e) =>
                      set(() => {
                        d.custom = e.target.value;
                        if (q.type !== "multiple" && e.target.value) d.selected = [];
                      })
                    }
                  />
                )}
              </div>
            )}
          </div>
        );
      })}
      <div className="fb-send">
        <span className="fb-hint">{hint}</span>
        {!lateMode && (
          <button className="fb-ghost" disabled={!online} onClick={() => feedback.end(ask.id).then(onChanged).catch((e) => onError(msg(e)))}>
            Dismiss
          </button>
        )}
        <button className="fb-primary" disabled={!ready || !online || sending} onClick={send}>
          Send
        </button>
      </div>
    </div>
  );
}

function Offline({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="fb-banner warn">
      <span>Feedback service isn't running. Waiting asks can't be answered until it is back.</span>
      <button onClick={onRetry}>Retry</button>
    </div>
  );
}

function Empty({ onConnect }: { onConnect?: () => void }) {
  return (
    <div className="fb-empty">
      <span className="fb-glyph big">
        <Icon name="agent" size={20} />
      </span>
      <h2>No feedback yet</h2>
      <p>
        Agents that call the <code>interactive_feedback</code> MCP tool show up here, one thread per project and run.
      </p>
      {onConnect && (
        <button className="fb-primary" onClick={onConnect}>
          Connect an Agent
        </button>
      )}
    </div>
  );
}
