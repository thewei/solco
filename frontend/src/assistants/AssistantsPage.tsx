import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { App, type LocalCommand, type OfflineChat } from "../App";
import { Icon } from "../icons";
import {
  assistants,
  chat,
  events,
  type AssistantsAgent,
  type ChatBridgeState,
  type AssistantsDetail,
  type AssistantsDrawer,
  type AssistantsRun,
  type AssistantsTask,
  type AssistantsView,
  type Approval,
  type CoordinatorChat,
  type InboxItem,
  type Routine,
} from "../mygo";
import { GroupChat, GroupsSheet, NewGroup } from "./GroupChat";
import { WorkPulse } from "./Progress";
import { WorkStyle } from "./WorkStyle";
import { MemoryTab } from "./MemoryTab";
import { ResultCards } from "./ResultCards";
import { Behavior } from "./Behavior";
import { ProofBox } from "./Proof";
import { ReviewBox } from "./Review";
import "./assistants.css";

/**
 * The Assistants page, chat-first (design: designs/assistants-chat-first/). An
 * Assistant is its conversation: the shared Chat fills the page, what needs the
 * person rises above it as one strip, and everything the old tabs held lives
 * behind the ⋯ menu and the More drawer. The page reads one light bridge
 * snapshot (the plugin's core answers; nothing is cached here); the long
 * lists are read only while the drawer is open. Every action goes back through
 * the bridge with a fresh requestId, so a double click replays instead of
 * duplicating, and a refusal shows the plugin's own sentence.
 */

type Tab = "needs" | "tasks" | "memory" | "settings";
/** The Tasks tab is one list with three views: what is open, scheduled routines, what is done. */
type TaskFilter = "open" | "routines" | "done";
type Form = "assistant" | "group" | "groups" | "task" | "rename" | null;

const rid = () => crypto.randomUUID();
const OPEN_TASK = new Set(["draft", "waiting", "queued", "claimed", "starting", "running", "blocked", "review_required", "uncertain"]);
/** A first message that has not found its Coordinator in this long is given back. */
const HOLD_LIMIT_MS = 90_000;

function useAssistants() {
  const [view, setView] = useState<AssistantsView | null>(null);
  const [error, setError] = useState("");
  const pull = useCallback(() => {
    assistants.snapshot().then(setView).catch((e) => setError(msg(e)));
  }, []);
  // The launchable-agent catalog is read in the background at window start;
  // until it lands, look again soon instead of waiting a whole poll.
  const loading = view !== null && !view.agentsReady;
  useEffect(() => {
    if (!loading) return;
    const t = window.setTimeout(pull, 700);
    return () => window.clearTimeout(t);
  }, [loading, view, pull]);
  useEffect(() => {
    pull();
    // The page polls only while it is visible: a hidden window costs no CLI calls.
    const timer = window.setInterval(() => !document.hidden && pull(), 4000);
    const show = () => !document.hidden && pull();
    document.addEventListener("visibilitychange", show);
    // A sidebar click is a selection the page must show now, not at the next poll.
    const off = events.assistantsSelect.on(() => pull());
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", show);
      off();
    };
  }, [pull]);
  useEffect(() => {
    if (view?.theme) document.documentElement.dataset.theme = view.theme;
  }, [view?.theme]);
  return { view, error, setError, pull };
}

/** The More drawer's lists: read on open and while open, never otherwise. */
function useDrawer(id: string, open: boolean) {
  const [drawer, setDrawer] = useState<AssistantsDrawer | null>(null);
  const pull = useCallback(() => {
    if (open) assistants.drawer(id).then(setDrawer).catch(() => undefined);
  }, [id, open]);
  useEffect(() => {
    if (!open) return;
    pull();
    const timer = window.setInterval(() => !document.hidden && pull(), 4000);
    return () => window.clearInterval(timer);
  }, [open, pull]);
  useEffect(() => setDrawer(null), [id]);
  return { drawer, pull };
}

function msg(e: unknown): string {
  const text = e instanceof Error ? e.message : String(e);
  return text.replace(/^[a-z_]+: /, "");
}

export function AssistantsPage() {
  const { view, error, setError, pull } = useAssistants();
  const [tab, setTab] = useState<Tab | null>(null);
  const [filter, setFilter] = useState<TaskFilter>("open");
  const [menu, setMenu] = useState(false);
  const [form, setForm] = useState<Form>(null);
  const [stripOpen, setStripOpen] = useState(false);
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const archiveRequest = useRef("");
  // A decision belongs to the exact Assistant and fact watermark. A
  // selection change must never resolve another Assistant's report.
  const [reportDecision, setReportDecision] = useState<{ assistantId: string; throughSeq: number; action: "skip" | "retry"; requestId: string } | null>(null);
  const [resolvingReport, setResolvingReport] = useState(false);
  const selected = view?.selected ?? "";
  const { drawer, pull: pullDrawer } = useDrawer(selected, tab !== null);
  // The sidebar's "+" posts an intent (a monotonic seq). The page reads it
  // through its own cheap bridge call — never through the plugin-backed
  // Snapshot, whose failure would hide the click — and the event hands it over
  // at once. It stays pending until acknowledged, so a page that was still
  // loading or a reply that arrives out of order cannot lose it; a seq is
  // acted on once.
  const handledIntent = useRef(0);
  const takeIntent = useCallback((i: { seq: number; kind: string } | null | undefined) => {
    if (!i || i.seq <= handledIntent.current) return;
    handledIntent.current = i.seq;
    setForm("assistant");
    assistants.ackIntent(i.seq).catch(() => undefined);
  }, []);
  useEffect(() => {
    const read = () => assistants.pendingIntent().then(takeIntent).catch(() => undefined);
    void read();
    const off = events.assistantsIntent.on(takeIntent);
    const timer = window.setInterval(() => !document.hidden && void read(), 1500);
    return () => {
      off();
      window.clearInterval(timer);
    };
  }, [takeIntent]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || form) return;
      if (menu) setMenu(false);
      else if (tab) setTab(null);
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [menu, tab, form]);
  /** Run one command; show the refusal, then re-read. Resolves with the refusal's words ("" when it went through), so a form closes only on success. */
  const act: Act = useCallback(
    async (call) => {
      setError("");
      let refusal = "";
      try {
        await call();
      } catch (e) {
        refusal = msg(e) || "Failed";
        setError(refusal);
      }
      pull();
      pullDrawer();
      return refusal;
    },
    [pull, pullDrawer, setError],
  );

  if (!view) {
    return error ? (
      <div className="as-empty">
        <h2>Assistants can't be read</h2>
        <p>{error}</p>
      </div>
    ) : (
      <div className="as-boot" />
    );
  }
  if (!view.available) {
    return (
      <div className="as-empty">
        <h2>Assistants aren't available</h2>
        <p>Install or update the Solco Automations plugin to turn them on.</p>
      </div>
    );
  }
  if (view.selectedGroup) {
    return (
      <>
        <GroupChat view={view} id={view.selectedGroup} refresh={pull} />
        {form === "assistant" ? <NewAssistant onClose={() => setForm(null)} onCreated={pull} /> : null}
        {form === "group" ? <NewGroup view={view} onClose={() => setForm(null)} onCreated={pull} /> : null}
      </>
    );
  }
  const detail = view.detail;
  if (!detail) {
    return (
      <div className="as-empty">
        <h2>No Assistants yet</h2>
        <p>An Assistant is a named helper you talk to. It keeps tasks, memory and approvals.</p>
        <button type="button" className="as-btn primary" onClick={() => setForm("assistant")}>
          New Assistant
        </button>
        {form === "assistant" ? <NewAssistant onClose={() => setForm(null)} onCreated={pull} /> : null}
        {form === "group" ? <NewGroup view={view} onClose={() => setForm(null)} onCreated={pull} /> : null}
      </div>
    );
  }
  const a = detail.assistant;
  const pending = detail.approvals.filter((p) => p.state === "pending");
  const others = detail.inbox.items.filter((i) => i.kind !== "approval" && i.kind !== "news");
  const needsCount = pending.length + others.length;
  const co = detail.coordinator;
  const report = detail.report;
  const row = view.assistants.find((r) => r.id === a.id);
  const status = coordinatorStatus(a.status, co.state, detail.coordinatorOpen, (row?.running ?? 0) + (row?.queued ?? 0) > 0, detail.idleSleepMin);
  const tabs: [Tab, string, number][] = [
    ["needs", "Needs you", needsCount],
    ["tasks", "Tasks", drawer ? drawer.tasks.filter((t) => OPEN_TASK.has(t.state)).length : 0],
    ["memory", "Memory", 0],
    ["settings", "Settings", 0],
  ];
  const choose = (fn: () => void) => () => {
    setMenu(false);
    fn();
  };
  return (
    <div className="as">
      <header className="as-head">
        <h1>{a.name}</h1>
        {a.primary ? (
          <span className="as-chip" title="Always listed first, never archived, and the only Assistant that can create, rename, archive or hand work to the others.">
            Primary
          </span>
        ) : null}
        {a.system ? null : (
          <span className={`as-state ${status.tone}`} title={status.hint}>
            <span className={`as-dot ${status.tone}`} />
            {status.label}
          </span>
        )}
        {needsCount > 0 ? (
          <button type="button" className="as-needs" onClick={() => setStripOpen((v) => !v)}>
            {needsCount} {needsCount === 1 ? "needs" : "need"} you
          </button>
        ) : null}
        <span className="as-grow" />
        <button type="button" className={`as-ib ${tab ? "on" : ""}`} onClick={() => setTab(tab ? null : needsCount > 0 ? "needs" : "tasks")}>
          More
        </button>
        <div className="as-menu-wrap">
          <button type="button" className={`as-ib ${menu ? "on" : ""}`} aria-label="Actions" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((v) => !v)}>
            ⋯
          </button>
          {menu ? (
            <>
              <div className="as-menu-scrim" onMouseDown={() => setMenu(false)} />
              <div className="as-menu" role="menu">
                <button type="button" role="menuitem" onClick={choose(() => setForm("task"))}>
                  New task…
                </button>
                <hr />
                <button type="button" role="menuitem" onClick={choose(() => setTab("settings"))}>
                  Settings…
                </button>
                <button type="button" role="menuitem" onClick={choose(() => setForm("rename"))}>
                  Rename…
                </button>
                {a.primary ? null : (
                  <button type="button" role="menuitem" onClick={choose(() => { archiveRequest.current = rid(); setConfirmArchive(true); })}>
                    Archive Assistant…
                  </button>
                )}
                <button type="button" role="menuitem" onClick={choose(() => void act(() => assistants.setAssistantEnabled(rid(), a.id, a.status !== "active", a.revision)))}>
                  {a.status === "active" ? "Pause" : "Resume"} {a.name}
                </button>
                <hr />
                <button type="button" role="menuitem" onClick={choose(() => setForm("assistant"))}>
                  New Assistant…
                </button>
                {a.primary ? (
                  <button type="button" role="menuitem" onClick={choose(() => setForm("groups"))}>
                    Groups…
                  </button>
                ) : null}
              </div>
            </>
          ) : null}
        </div>
      </header>
      {error ? (
        <div className="as-error" role="alert">
          <span>{error}</span>
          <button type="button" className="as-link" onClick={() => setError("")}>
            Dismiss
          </button>
        </div>
      ) : null}
      {needsCount > 0 ? <Strip pending={pending} others={others} open={stripOpen} setOpen={setStripOpen} act={act} more={(to, f) => { if (f) setFilter(f); setTab(to ?? "needs"); }} /> : null}
      <WorkPulse row={row} onTasks={() => { setFilter("open"); setTab("tasks"); }} />
      {report?.state === "uncertain" ? (
        <section className="as-note warn as-report" aria-label="Coordinator progress report needs review">
          <strong>Progress report needs review</strong>
          <p>Solco could not confirm a completed OMP reply. Check the original conversation first; Solco will not send this batch again without your decision.</p>
          {report.lastError ? <p className="as-meta">{report.lastError}</p> : null}
          <p className="as-meta">Activity through fact #{report.throughSeq}. No completed OMP reply has been verified.</p>
          <div className="as-report-actions">
            {report.paneId ? <button type="button" className="as-btn" onClick={() => { if (report.paneId) void assistants.openPane(report.paneId).catch((e) => setError(msg(e))); }}>Open original Terminal</button> : null}
            <button type="button" className="as-btn" disabled={report.throughSeq <= 0} onClick={() => setReportDecision({ assistantId: a.id, throughSeq: report.throughSeq, action: "skip", requestId: rid() })}>Already delivered · Skip</button>
            <button type="button" className="as-btn" disabled={report.throughSeq <= 0} onClick={() => setReportDecision({ assistantId: a.id, throughSeq: report.throughSeq, action: "retry", requestId: rid() })}>Send again…</button>
          </div>
        </section>
      ) : report?.state === "queued" || report?.state === "claimed" ? (
        <div className="as-note as-report" role="status">
          Solco is {report.state === "claimed" ? "submitting" : "waiting to submit"} a progress update to the existing Coordinator. This is not a completed reply.
        </div>
      ) : report?.state === "submitted" && report.replyState === "awaiting" ? (
        <div className="as-note as-report" role="status">
          <strong>Report delivered · Waiting for OMP reply</strong>
          <span className="as-meta">Herdr accepted activity through fact #{report.throughSeq}; Solco is checking the original OMP transcript for a completed answer.</span>
        </div>
      ) : report?.state === "submitted" && report.replyState === "verified" ? (
        <div className="as-note as-report" role="status">
          <strong>OMP reply verified</strong>
          <span className="as-meta">Activity through fact #{report.throughSeq} · original OMP message {report.replyMessageId || "verified"}.</span>
          {report.paneId ? <button type="button" className="as-btn" onClick={() => { if (report.paneId) void assistants.openPane(report.paneId).catch((e) => setError(msg(e))); }}>Open original Terminal</button> : null}
        </div>
      ) : null}
      {a.status !== "active" ? (
        <div className="as-note warn">
          Paused — new tasks and watches are not started. The conversation keeps working.
          <button type="button" className="as-link" onClick={() => void act(() => assistants.setAssistantEnabled(rid(), a.id, true, a.revision))}>
            Resume
          </button>
        </div>
      ) : null}
      <main className="as-body chat">
        <Conversation key={a.id} view={view} detail={detail} setError={setError} refresh={pull} onTasks={(f) => { setFilter(f); setTab("tasks"); }} />
        {tab ? (
          <aside className="as-drawer" aria-label="More">
            <nav className="as-tabs" role="tablist">
              {tabs.map(([id, label, n]) => (
                <button key={id} type="button" role="tab" aria-selected={tab === id} className={tab === id ? "on" : ""} onClick={() => setTab(id)}>
                  {label}
                  {n > 0 ? <span className="as-count">{n}</span> : null}
                </button>
              ))}
              <span className="as-grow" />
              <button type="button" className="as-ib" aria-label="Close" onClick={() => setTab(null)}>
                ✕
              </button>
            </nav>
            <div className="as-dbody">
              <DrawerBody tab={tab} filter={filter} setFilter={setFilter} detail={detail} drawer={drawer} view={view} pending={pending} act={act} pull={pull} />
            </div>
          </aside>
        ) : null}
      </main>
      {form === "assistant" ? <NewAssistant onClose={() => setForm(null)} onCreated={pull} /> : null}
      {form === "group" ? <NewGroup view={view} onClose={() => setForm(null)} onCreated={pull} /> : null}
      {form === "rename" ? <Rename assistant={a} onClose={() => setForm(null)} act={act} /> : null}
      {form === "groups" ? <GroupsSheet view={view} onClose={() => setForm(null)} onOpen={(id) => { setForm(null); void assistants.selectGroup(id).then(pull); }} onNew={() => setForm("group")} /> : null}
      {form === "task" ? <NewTask assistantId={a.id} agents={view.agents} onClose={() => setForm(null)} act={act} /> : null}
      {reportDecision?.assistantId === a.id && report?.state === "uncertain" && report.throughSeq === reportDecision.throughSeq ? (
        <Sheet title={reportDecision.action === "skip" ? "Confirm report already delivered" : "Send the progress report again?"}
          submit={resolvingReport ? "Saving…" : reportDecision.action === "skip" ? "Mark handled" : "Retry delivery"}
          busy={resolvingReport} error={error}
          onClose={() => setReportDecision(null)}
          onSubmit={() => {
            if (resolvingReport || !reportDecision) return;
            setResolvingReport(true);
            void act(() => assistants.resolveCoordinatorReport(reportDecision.requestId, reportDecision.assistantId,
              reportDecision.throughSeq, reportDecision.action, true)).then((failure) => {
              setResolvingReport(false);
              if (!failure) { setReportDecision(null); pull(); }
            });
          }}>
          <p>Check the original OMP conversation before proceeding. Herdr may have accepted the request, but Solco cannot verify a completed OMP answer for this batch.</p>
          <p>{reportDecision.action === "skip"
            ? "Choose this only if the report already arrived, or if you intentionally want to discard this pending update. The activity facts remain available."
            : "Retrying can add the same progress message twice. Only proceed after checking the original conversation and accepting that risk."}</p>
          <p className="as-meta">This decision applies only to activity through fact #{reportDecision.throughSeq} for {a.name}.</p>
        </Sheet>
      ) : null}
      {confirmArchive ? (
        <Sheet title={"Archive " + a.name + "?"} submit={archiving ? "Archiving…" : "Archive"} busy={archiving}
          error={error} onClose={() => setConfirmArchive(false)} onSubmit={() => {
            if (archiving) return;
            setArchiving(true);
            void act(() => assistants.archiveAssistant(archiveRequest.current, a.id, a.revision))
              .then((failure) => { setArchiving(false); if (!failure) { setConfirmArchive(false); pull(); } });
          }}>
          <p>The Assistant will disappear from your active list. Conversations, tasks and memories stay intact.</p>
          <p>Finish or cancel pending work, close the Assistant's Herdr panes, then archive. Solco checks pane closure before hiding it.</p>
          <p>Restore it later from Settings → Archived Assistants.</p>
        </Sheet>
      ) : null}
    </div>
  );
}

/** How long a quiet Coordinator is left running, in words ("" = never sleeps). */
function napWords(min: number): string {
  if (min <= 0) return "";
  return min === 60 ? "an hour" : `${min} minutes`;
}

/**
 * The header's one-word truth about the conversation: Sleeping, Ready or
 * Working. Start/stop are not the person's business — Send wakes it, quiet puts
 * it to sleep — so "not started", "stopped" and "asleep" are one word. "Working"
 * is the Assistant's tasks running or queued, read from the core's overview, not
 * guessed from prose. Only a real problem (paused, could not start, needs a
 * permission, not in this window) says anything else.
 */
function coordinatorStatus(assistant: string, state: string, open: boolean, working: boolean, nap: number): { label: string; tone: string; hint: string } {
  const sleeps = napWords(nap) ? `Goes to sleep after ${napWords(nap)} without activity; wakes when you send a message.` : "Stays awake until stopped; wakes when you send a message.";
  if (assistant !== "active") return { label: "Paused", tone: "warn", hint: "New tasks and watches are not started." };
  switch (state) {
    case "starting":
      return { label: "Waking up…", tone: "work", hint: "" };
    case "ready":
      if (!open) return { label: "Not in this window", tone: "warn", hint: "" };
      return working ? { label: "Working", tone: "work", hint: sleeps } : { label: "Ready", tone: "ok", hint: sleeps };
    case "failed":
      return { label: "Could not start", tone: "bad", hint: "Send a message to try again." };
    case "blocked":
      return { label: "Needs permission", tone: "warn", hint: "" };
    case "uncertain":
      return { label: "Check start", tone: "warn", hint: "" };
  }
  return { label: "Sleeping", tone: "", hint: sleeps };
}

/** What needs the person, above the chat: approvals decide here, anything else points at the drawer. */
function Strip({ pending, others, open, setOpen, act, more }: { pending: Approval[]; others: InboxItem[]; open: boolean; setOpen: (v: boolean) => void; act: Act; more: (to?: Tab, filter?: TaskFilter) => void }) {
  const first = pending[0]?.action ?? others[0]?.title ?? "";
  return (
    <section className="as-strip" aria-label="Needs you">
      <div className="as-strip-top">
        <strong>{pending.length + others.length === 1 ? "1 needs you" : `${pending.length + others.length} need you`}</strong>
        {!open ? <span className="as-meta as-ellipsis">{first}</span> : null}
        <span className="as-grow" />
        <button type="button" className="as-link" onClick={() => setOpen(!open)}>
          {open ? "Hide" : "Review"}
        </button>
      </div>
      {open ? (
        <>
          {pending.map((p) => (
            <div key={p.id} className="as-strip-item">
              <div className="as-strip-text">
                <strong>Approve?</strong>
                <p className="as-text">{p.action}</p>
                <span className="as-meta">{p.subjectKind}</span>
              </div>
              <button type="button" className="as-btn primary" onClick={() => act(() => assistants.decideApproval(rid(), p.id, p.actionHash, true))}>
                Approve
              </button>
              <button type="button" className="as-btn" onClick={() => act(() => assistants.decideApproval(rid(), p.id, p.actionHash, false))}>
                Deny
              </button>
            </div>
          ))}
          {others.map((i) => (
            <div key={i.id} className="as-strip-item">
              <div className="as-strip-text">
                <strong>{i.title}</strong>
                <p className="as-meta">{i.detail}</p>
              </div>
              {i.paneId && i.actions?.includes("open_pane") ? (
                <button type="button" className="as-btn" onClick={() => void assistants.openPane(i.paneId as string)}>
                  Open Pane
                </button>
              ) : null}
              <button type="button" className="as-btn" onClick={() => more()}>
                Open
              </button>
            </div>
          ))}
        </>
      ) : null}
    </section>
  );
}

function DrawerBody({ tab, filter, setFilter, detail, drawer, view, pending, act, pull }: { tab: Tab; filter: TaskFilter; setFilter: (f: TaskFilter) => void; detail: AssistantsDetail; drawer: AssistantsDrawer | null; view: AssistantsView; pending: Approval[]; act: Act; pull: () => void }) {
  const reasons = new Map(detail.inbox.items.filter((i) => i.kind !== "approval" && i.kind !== "news" && i.taskId).map((i) => [i.taskId as string, i]));
  if (tab === "settings") return <SettingsTab detail={detail} view={view} act={act} />;
  if (tab === "memory") return <MemoryTab id={detail.assistant.id} act={act} />;
  if (!drawer) return <Empty>Loading…</Empty>;
  if (tab === "needs") {
    const needTasks = drawer.tasks.filter((t) => reasons.has(t.id) || t.needsYou);
    return <NeedsYou approvals={pending} tasks={needTasks} reasons={reasons} act={act} pull={pull} />;
  }
  return <TasksTab filter={filter} setFilter={setFilter} drawer={drawer} act={act} />;
}

/** One list for the work: open tasks, routines, and what is done. */
function TasksTab({ filter, setFilter, drawer, act }: { filter: TaskFilter; setFilter: (f: TaskFilter) => void; drawer: AssistantsDrawer; act: Act }) {
  const open = drawer.tasks.filter((t) => OPEN_TASK.has(t.state));
  const done = drawer.tasks.filter((t) => !OPEN_TASK.has(t.state) && !t.needsYou);
  const routines = drawer.routines.filter((r) => r.state !== "cancelled");
  const chips: [TaskFilter, string, number][] = [
    ["open", "Open", open.length],
    ["routines", "Routines", routines.length],
    ["done", "Done", done.length],
  ];
  return (
    <>
      <nav className="as-tabs as-filters" role="tablist" aria-label="Show">
        {chips.map(([id, label, n]) => (
          <button key={id} type="button" role="tab" aria-selected={filter === id} className={filter === id ? "on" : ""} onClick={() => setFilter(id)}>
            {label}
            {n > 0 ? <span className="as-count">{n}</span> : null}
          </button>
        ))}
      </nav>
      {filter === "open" ? <Tasks tasks={open} act={act} /> : null}
      {filter === "routines" ? <Routines routines={drawer.routines} act={act} /> : null}
      {filter === "done" ? <Done tasks={done} act={act} /> : null}
    </>
  );
}

/** Everything that configures this Assistant, one page: instructions and sleep in the open, when it acts on its own, and the plumbing under Advanced. */
function SettingsTab({ detail, view, act }: { detail: AssistantsDetail; view: AssistantsView; act: Act }) {
  return (
    <>
      <h3 className="as-sub as-first">How it works</h3>
      <WorkStyle
        id={detail.assistant.id}
        agents={view.agents}
        act={act}
        between={
          <>
            <h3 className="as-sub">On its own</h3>
            <Behavior id={detail.assistant.id} act={act} />
          </>
        }
        advanced={<About detail={detail} view={view} act={act} />}
      />
    </>
  );
}

function About({ detail, view, act }: { detail: AssistantsDetail; view: AssistantsView; act: Act }) {
  const a = detail.assistant;
  const co = detail.coordinator;
  const canStart = a.status === "active" && (co.state === "none" || co.state === "stopped" || co.state === "failed");
  return (
    <>
      <section className="as-card">
        <strong>{a.name}</strong>
        <p className="as-text">{a.description || "No description."}</p>
      </section>
      <section className="as-card">
        <div className="as-row">
          <strong>Coordinator</strong>
          <span className={`as-chip ${co.state}`}>{co.state === "none" ? "not started" : co.state}</span>
        </div>
        <p className="as-meta">The Agent you talk to. It runs as a real Herdr Agent and files tasks for you to publish.</p>
        <div className="as-row">
          {canStart ? (
            <button type="button" className="as-btn" disabled={view.agents.length === 0} onClick={() => act(() => assistants.startCoordinator(rid(), a.id, ""))}>
              {co.state === "none" ? "Start" : "Start again"}
            </button>
          ) : null}
          {co.state === "starting" || co.state === "ready" ? (
            <button type="button" className="as-btn danger" onClick={() => act(() => assistants.stopCoordinator(rid(), a.id))}>
              Stop
            </button>
          ) : null}
        </div>
      </section>
    </>
  );
}

const sleep = (ms: number) => new Promise<void>((resolve) => window.setTimeout(resolve, ms));

/** A "ready" Coordinator the Chat still cannot reach for this long is not coming (another Herdr session, a pane that left the tree). */
const UNREACHABLE_LIMIT_MS = 20_000;

/**
 * The conversation: the Coordinator's real Chat. It is always the same Chat
 * page. While no Coordinator is reachable, the page's own composer stands in
 * (`App`'s `offline` mode — the same box, the same keys, the same column), and
 * Send does what the person would otherwise have to do first: it starts the
 * Coordinator (idempotent requestId), waits until the Chat is on it and the
 * Agent takes prompts, then delivers the text once through the Chat's own send
 * path (Herdr's agent.prompt). A start that cannot proceed — paused, blocked on
 * a permission, a launch Herdr never confirmed — is the composer's ordinary
 * "Not delivered · Retry · Edit" row, so nothing typed is lost.
 */
function Conversation({ view, detail, setError, refresh, onTasks }: { view: AssistantsView; detail: AssistantsDetail; setError: (e: string) => void; refresh: () => void; onTasks: (filter: "open" | "done") => void }) {
  const a = detail.assistant;
  const co = detail.coordinator;
  const [bind, setBind] = useState<CoordinatorChat | null>(null);
  const [agent, setAgent] = useState(detail.profileAgent || view.defaultAgent);
  const live = co.state === "ready" && detail.coordinatorOpen;
  useEffect(() => {
    if (!agent || !view.agents.some((x) => x.id === agent)) setAgent(detail.profileAgent || view.defaultAgent);
  }, [agent, view.agents, view.defaultAgent, detail.profileAgent]);
  // The Assistant's own profile is the source of truth for the chip: a change
  // made elsewhere (Advanced) shows here at the next read.
  useEffect(() => {
    if (detail.profileAgent && view.agents.some((x) => x.id === detail.profileAgent)) setAgent(detail.profileAgent);
  }, [detail.profileAgent, view.agents]);
  useEffect(() => {
    if (!live) {
      setBind(null);
      return;
    }
    let cancelled = false;
    // The native side keeps this WebView alive (hidden) after the route
    // changes; a hidden page costs CLI calls for nothing and its binds would
    // fight the user's Chat (the 2026-11-07 Loading storm). Poll only while
    // visible, and catch up the moment the page is shown again.
    const attempt = () => {
      if (document.hidden) return;
      void assistants
        .bindCoordinator(a.id)
        .then((r) => !cancelled && setBind(r))
        .catch(() => !cancelled && setBind(null));
    };
    void attempt();
    // Until the Agent has reported its session (an invitation Chat has none),
    // look again.
    const timer = window.setInterval(attempt, bind?.invitation || !bind?.bound ? 1500 : 6000);
    document.addEventListener("visibilitychange", attempt);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", attempt);
    };
  }, [a.id, co.paneId, live, bind?.invitation, bind?.bound]);
  const bound = live && bind?.bound === true && bind.paneId === co.paneId;
  // Until the live Coordinator is bound, the Chat shows what it said last: the
  // Assistant's previous conversation, read-only (the same OMP session a restart
  // resumes). The host drops it by itself the moment the live Chat binds.
  const [previewing, setPreviewing] = useState(false);
  useEffect(() => {
    if (bound || a.system) {
      setPreviewing(false);
      return;
    }
    let cancelled = false;
    // Same hidden-page rule as the bind poller above.
    const attempt = () => {
      if (document.hidden) return;
      void assistants
        .previewCoordinator(a.id)
        .then((r) => !cancelled && setPreviewing(r.previewing))
        .catch(() => !cancelled && setPreviewing(false));
    };
    void attempt();
    const timer = window.setInterval(attempt, 5000);
    document.addEventListener("visibilitychange", attempt);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", attempt);
    };
  }, [a.id, a.system, bound]);
  // Clicking the composer starts the Coordinator once per state it was found in,
  // so it is up by the time the person has typed; Send still starts it when not.
  const wokeFor = useRef("");
  const wake = () => {
    if (live || a.system || a.status !== "active" || !agent) return;
    if (co.state !== "none" && co.state !== "stopped" && co.state !== "failed") return;
    const mark = `${a.id}:${co.revision}`;
    if (wokeFor.current === mark) return;
    wokeFor.current = mark;
    void assistants
      .ensureChat(rid(), a.id, agent)
      .then(() => refresh())
      .catch((e) => setError(msg(e)));
  };
  // A Coordinator that went to sleep by itself (not one the person stopped) is
  // woken as the chat opens — this conversation selected, or the window shown
  // again — so it is up by the time the first word is typed. Sleeping while the
  // page stays open is left alone: waking it again here would defeat the sleep.
  const wakeRef = useRef(wake);
  wakeRef.current = wake;
  const detailRef = useRef(detail);
  detailRef.current = detail;
  useEffect(() => {
    const opened = () => {
      const c = detailRef.current.coordinator;
      if (!document.hidden && c.state === "stopped" && c.stopReason === "idle") wakeRef.current();
    };
    opened();
    document.addEventListener("visibilitychange", opened);
    return () => document.removeEventListener("visibilitychange", opened);
  }, []);
  // `/memory` opens the Assistant's two memory files in the person's own editor;
  // Solco never edits memory inside this page.
  const systemAssistant = a.system;
  const localCommands = useMemo<LocalCommand[]>(
    () =>
      systemAssistant
        ? []
        : [
            {
              name: "/memory",
              detail: "Open this Assistant's memory files in your editor",
              run: () => {
                void assistants
                  .openMemoryFile(a.id, "user")
                  .then(() => assistants.openMemoryFile(a.id, "assistant"))
                  .catch((e) => setError(msg(e)));
              },
            },
          ],
    [a.id, systemAssistant, setError],
  );
  // A send in flight outlives renders: it reads the latest "is the Chat on the
  // Coordinator" and "is this page still here" through refs.
  const boundRef = useRef(bound);
  boundRef.current = bound;
  const here = useRef(true);
  useEffect(() => {
    here.current = true;
    return () => {
      here.current = false;
    };
  }, []);

  const sendToCoordinator = async (text: string): Promise<ChatBridgeState> => {
    if (!agent) throw new Error(view.agentsReady ? "Turn on an agent in Settings → Agents." : "Agents are still loading; try again in a moment.");
    if (a.status !== "active" && !live) throw new Error("This Assistant is paused. Resume it (the bar above) to start its Coordinator.");
    try {
      setError("");
      let r = await assistants.ensureChat(rid(), a.id, agent);
      refresh();
      const startedAt = Date.now();
      let unreachableSince = 0;
      for (;;) {
        if (!here.current) throw new Error("You left this conversation before the Coordinator was ready; the message was not sent.");
        if (r.bound) {
          const s = await chat.snapshot();
          // A fresh Agent reports no status for a moment, and a Send into that
          // would be refused without a word: ask the Chat first, look again until it says yes.
          if (s.disposition === "ready" || s.disposition === "queue") {
            if (!here.current) throw new Error("You left this conversation before the Coordinator was ready; the message was not sent.");
            const next = await chat.send(text);
            refresh();
            // Hand the screen over to the live Chat before the "Sending…" row goes,
            // so the empty card never flashes back between the two.
            const handover = Date.now() + 8000;
            while (!boundRef.current && here.current && Date.now() < handover) await sleep(150);
            return next;
          }
        } else {
          switch (r.state) {
            case "failed":
              throw new Error("The Coordinator could not start. Send again to try once more.");
            case "blocked":
              throw new Error("The Coordinator is waiting for a permission or trust decision in its Terminal. Resolve it there, then send again.");
            case "uncertain":
              throw new Error("Herdr has not confirmed the original Coordinator, and Solco will not start a second Agent while it may still be running. Check its Terminal (⋯ → Open original Terminal), then send again.");
            case "stopped":
              throw new Error("The Coordinator stopped before it was ready. Send again to start it.");
          }
          // Ready, yet not open here: tolerated briefly (the tree is catching up), then said plainly.
          if (r.state === "ready") {
            unreachableSince ||= Date.now();
            if (Date.now() - unreachableSince > UNREACHABLE_LIMIT_MS) throw new Error(r.reason || "The Coordinator is not open in this Herdr window.");
          } else {
            unreachableSince = 0;
          }
        }
        if (Date.now() - startedAt > HOLD_LIMIT_MS) throw new Error("The Coordinator is taking too long to start. Check its Terminal before sending again.");
        await sleep(700);
        r = await assistants.bindCoordinator(a.id);
      }
    } catch (e) {
      // The composer's "Not delivered" row holds the text while the page is
      // here; a page that was left keeps it in the box for the next visit.
      if (!here.current) localStorage.setItem(`as-draft:${a.id}`, text);
      throw new Error(msg(e));
    }
  };

  if (a.system) {
    // The plugin's own owner of scheduled runs: there is no one to talk to.
    return (
      <div className="as-shell">
        <div className="as-shell-body">
          <div className="as-hello">
            <h2>{a.name}</h2>
            <p>Runs your scheduled Automations. There is nothing to chat with here — see its runs under More, and manage the schedules in Automations.</p>
          </div>
        </div>
      </div>
    );
  }
  const starting = co.state === "starting" || (live && !bind?.bound);
  const stranded = co.state === "ready" && !detail.coordinatorOpen;
  const asleep = co.state === "none" || co.state === "stopped";
  const paused = a.status !== "active";
  const noAgents = view.agentsReady && view.agents.length === 0;
  const nap = napWords(detail.idleSleepMin);
  const sentence = paused
    ? "Paused — resume this Assistant to start its Coordinator."
    : noAgents
      ? "Turn on an agent in Settings → Agents to start a Coordinator."
      : co.state === "blocked"
        ? "The Coordinator is waiting for a permission or trust decision in Herdr. Resolve it in the original Terminal, then send your message."
        : co.state === "uncertain"
          ? "Herdr has not confirmed the original Coordinator. Send a message: Solco reuses it if it is still there, starts a new one if its terminal is gone, and never a second one while it may still be running."
          : stranded
            ? "This Assistant's Coordinator is not open in this window. Send a message: Solco starts a new one if the old one is gone."
            : starting
              ? "Getting the Coordinator ready…"
              : co.state === "failed"
                ? "The Coordinator could not start. Send a message to try again."
                : `Talk to it like a colleague${asleep ? "; it wakes when you send a message" : ""}. It turns what you ask into tasks and keeps them moving, and you approve anything risky.${asleep && nap ? ` It goes back to sleep after ${nap} of quiet.` : ""}`;
  const original = (co.state === "blocked" || co.state === "uncertain") && co.paneId ? co.paneId : "";
  // The Agent that runs the Coordinator is chosen while it sleeps (the choice is
  // the Assistant's profile and applies to its next start); once it is waking or
  // awake the chip only says which one it is.
  const choosable = view.agents.length > 1 && (asleep || co.state === "failed");
  const pickAgent = (next: string) => {
    setAgent(next);
    void assistants
      .persona(a.id)
      .then((v) => (v.runtime.profile.agent === next ? undefined : assistants.saveRuntime(rid(), a.id, next, "", "", v.runtime.profile.revision, -1)))
      .then(() => refresh())
      .catch((e) => setError(msg(e)));
  };
  const chip = choosable ? (
    <select className="chip mono modelchip as-agent" aria-label="Agent that runs the Coordinator" title="Agent that runs the Coordinator" value={agent} onChange={(e) => pickAgent(e.target.value)}>
      {view.agents.map((x) => (
        <option key={x.id} value={x.id}>
          {x.label}
        </option>
      ))}
    </select>
  ) : view.agents.length > 0 ? (
    <span className="chip mono" title="Agent that runs the Coordinator">
      {view.agents.find((x) => x.id === agent)?.label ?? view.agents[0].label}
    </span>
  ) : null;
  const offline: OfflineChat | undefined = bound
    ? undefined
    : {
        key: a.id,
        placeholder: paused ? "Paused — resume to start the Coordinator" : noAgents ? "Turn on an agent in Settings → Agents" : starting ? "The Coordinator is starting… you can type your message" : `Message ${a.name}…`,
        chip,
        send: sendToCoordinator,
        history: previewing,
        onFocus: wake,
        empty: (
          <div className="sess-state invite" role="status">
            <Icon name="step" size={18} />
            <div className="st-title">{a.name}</div>
            <div className="st-sub">{sentence}</div>
            {a.description ? <div className="st-sub as-desc">{a.description}</div> : null}
            {original ? (
              <button
                type="button"
                className="goterm"
                onClick={() => {
                  void assistants
                    .openPane(original)
                    .then((opened) => {
                      if (!opened) setError("The original Coordinator pane is not available in this Herdr window.");
                    })
                    .catch((e) => setError(msg(e)));
                }}
              >
                Open original Terminal
              </button>
            ) : null}
          </div>
        ),
      };
  // The Needs-you strip already carries these tasks (failed, awaiting review, asking): no second card for them.
  const asked = new Set(detail.inbox.items.flatMap((i) => (i.taskId && i.kind !== "news" ? [i.taskId] : [])));
  // One Chat instance for the Assistant's whole life: offline → live is the same
  // page handing over, so a draft typed meanwhile and the sent row survive it.
  return (
    <div className="as-chatwrap">
      <App
        offline={offline}
        localCommands={localCommands}
        dockAbove={<ResultCards assistantId={a.id} results={detail.results} skip={asked} onTasks={onTasks} setError={setError} />}
      />
    </div>
  );
}

type Act = (call: () => Promise<unknown>) => Promise<string>;

function Empty({ children }: { children: ReactNode }) {
  return <p className="as-none">{children}</p>;
}

function NeedsYou({ approvals, tasks, reasons, act }: { approvals: Approval[]; tasks: AssistantsTask[]; reasons: Map<string, InboxItem>; act: Act; pull: () => void }) {
  if (approvals.length === 0 && tasks.length === 0) return <Empty>Nothing is waiting for you.</Empty>;
  return (
    <>
      {approvals.map((p) => (
        <section key={p.id} className="as-card">
          <div className="as-row">
            <strong>Approve?</strong>
            <span className="as-meta">{p.subjectKind}</span>
          </div>
          <p className="as-text">{p.action}</p>
          <div className="as-row">
            <button type="button" className="as-btn primary" onClick={() => act(() => assistants.decideApproval(rid(), p.id, p.actionHash, true))}>
              Approve
            </button>
            <button type="button" className="as-btn" onClick={() => act(() => assistants.decideApproval(rid(), p.id, p.actionHash, false))}>
              Deny
            </button>
          </div>
        </section>
      ))}
      {tasks.map((t) => (
        <TaskCard key={t.id} task={t} act={act} reason={reasons.get(t.id)} />
      ))}
    </>
  );
}

function Tasks({ tasks, act }: { tasks: AssistantsTask[]; act: Act }) {
  if (tasks.length === 0) return <Empty>No open tasks.</Empty>;
  return (
    <>
      {tasks.map((t) => (
        <TaskCard key={t.id} task={t} act={act} />
      ))}
    </>
  );
}

function Done({ tasks, act }: { tasks: AssistantsTask[]; act: Act }) {
  if (tasks.length === 0) return <Empty>Nothing finished yet.</Empty>;
  return (
    <>
      {tasks.map((t) => (
        <TaskCard key={t.id} task={t} act={act} />
      ))}
    </>
  );
}

function TaskCard({ task: t, act, reason }: { task: AssistantsTask; act: Act; reason?: InboxItem }) {
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>{t.title}</strong>
        <span className={`as-chip ${t.state}`}>{t.state.replace("_", " ")}</span>
      </div>
      <p className="as-meta">
        {t.kind}
        {t.agent ? ` · ${t.agent}` : ""}
        {t.attempt > 1 ? ` · attempt ${t.attempt}` : ""}
      </p>
      {reason ? <p className={`as-reason ${reason.kind}`}>{reason.detail}</p> : null}
      {t.runs.map((r) => (
        <RunLine key={r.id} run={r} />
      ))}
      <ReviewBox task={t} act={act} />
      {t.state === "done" || t.state === "review_required" ? <ProofBox taskId={t.id} stamp={`${t.revision}:${t.state}:${t.reviewState}`} /> : null}
      <div className="as-row">
        {t.needsYou && t.state !== "review_required" ? (
          <button type="button" className="as-btn primary" onClick={() => act(() => assistants.ackTask(rid(), t.id, t.revision))}>
            Got it
          </button>
        ) : null}
        {t.state === "draft" ? (
          <button type="button" className="as-btn primary" onClick={() => act(() => assistants.publishTask(rid(), t.id, t.revision))}>
            Publish
          </button>
        ) : null}
        {t.state === "failed" || t.state === "cancelled" ? (
          <button type="button" className="as-btn" onClick={() => act(() => assistants.retryTask(rid(), t.id, t.revision))}>
            Retry
          </button>
        ) : null}
      </div>
    </section>
  );
}

function RunLine({ run }: { run: AssistantsRun }) {
  return (
    <p className="as-run">
      <span className={`as-dot ${run.status}`} />
      <span>{run.status}</span>
      {run.error ? <span className="as-err">{run.error}</span> : null}
      {run.canOpen ? (
        <button type="button" className="as-link" onClick={() => void assistants.openPane(run.paneId)}>
          Open Pane
        </button>
      ) : null}
    </p>
  );
}

function when(ms: number): string {
  return new Date(ms).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

const ROUTINE_STATE: Record<string, string> = { draft: "waiting for you", active: "on", stale: "changed — ask again", cancelled: "withdrawn" };

/** Scheduled work this Assistant drafted. Enabling is the approval card's answer; only withdrawing and pausing live here. */
function Routines({ routines, act }: { routines: Routine[]; act: Act }) {
  const shown = routines.filter((r) => r.state !== "cancelled");
  if (shown.length === 0) return <Empty>No routines. Ask {"“"}check this repo every morning{"”"} in the chat; it is only a draft until you approve it.</Empty>;
  return (
    <>
      {shown.map((r) => (
        <section key={r.name} className="as-card">
          <div className="as-row">
            <strong>{r.name}</strong>
            <span className={`as-chip ${r.state}`}>{r.paused ? "paused" : (ROUTINE_STATE[r.state] ?? r.state)}</span>
          </div>
          {r.rationale ? <p className="as-text">{r.rationale}</p> : null}
          <p className="as-meta">
            {r.cron} · {r.agent}
            {r.headless ? " · headless" : ""} · {r.repo}
          </p>
          <p className="as-meta">
            Asked by {r.requestedBy === "assistant" ? "your Assistant" : "you"} in {r.source === "chat" ? "the chat" : "the CLI"}
            {r.timezone ? ` · ${r.timezone}` : ""}
            {r.skills?.length ? ` · skills: ${r.skills.join(", ")}` : ""}
          </p>
          {r.reason ? <p className="as-meta">{r.reason}</p> : null}
          {r.state === "active" && r.next && !r.paused ? <p className="as-meta">Next run {when(r.next)}</p> : null}
          {r.lastState ? <p className="as-meta">Last run {r.lastState} · {ago(r.lastAt ?? 0)}</p> : null}
          {r.missing ? <p className="as-meta">Its automation is gone from the Automations list.</p> : null}
          <div className="as-row">
            {r.state === "active" ? (
              <button type="button" className="as-btn" onClick={() => act(() => assistants.routineAction(rid(), r.paused ? "resume" : "pause", r.name))}>
                {r.paused ? "Resume" : "Pause"}
              </button>
            ) : null}
            {r.state === "draft" || r.state === "stale" ? (
              <button type="button" className="as-btn" onClick={() => act(() => assistants.routineAction(rid(), "cancel", r.name))}>
                Withdraw
              </button>
            ) : null}
          </div>
        </section>
      ))}
    </>
  );
}

function ago(ms: number): string {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  return `${Math.round(s / 3600)} h ago`;
}

/** A sheet: a title, fields, Cancel + submit. Esc and the backdrop close it. */
function Sheet({ title, submit, busy, error, onClose, onSubmit, children }: { title: string; submit: string; busy: boolean; error?: string; onClose: () => void; onSubmit: () => void; children: ReactNode }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [onClose]);
  const go = (e: FormEvent) => {
    e.preventDefault();
    onSubmit();
  };
  return (
    <div className="as-scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form className="as-sheet" onSubmit={go}>
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

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="as-field">
      <span>{label}</span>
      {children}
    </label>
  );
}

/** The agent choice of a form: the Herdr kinds Settings offers; free text only when none is listed. */
function AgentPick({ agents, value, onChange }: { agents: AssistantsAgent[]; value: string; onChange: (v: string) => void }) {
  if (agents.length === 0) return <input value={value} onChange={(e) => onChange(e.target.value)} />;
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      {agents.map((x) => (
        <option key={x.id} value={x.id}>
          {x.label}
        </option>
      ))}
    </select>
  );
}

function Rename({ assistant, onClose, act }: { assistant: { id: string; name: string; revision: number }; onClose: () => void; act: Act }) {
  const [name, setName] = useState(assistant.name);
  const [failure, setFailure] = useState("");
  return (
    <Sheet
      title="Rename Assistant"
      submit="Rename"
      busy={!name.trim() || name.trim() === assistant.name}
      error={failure}
      onClose={onClose}
      onSubmit={() => {
        void act(() => assistants.renameAssistant(rid(), assistant.id, name, assistant.revision)).then((e) => (e ? setFailure(e) : onClose()));
      }}
    >
      <Field label="Name">
        <input autoFocus value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
      </Field>
    </Sheet>
  );
}

/**
 * The New Assistant sheet. The create call is awaited: the sheet closes only
 * on success (the bridge selects the new Assistant, so the page lands in its
 * conversation), a refusal keeps every typed character and shows the reason,
 * a double submit is ignored, and a retry of the same input reuses one
 * requestId so the plugin replays instead of creating a second Assistant.
 */
function NewAssistant({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const request = useRef({ key: "", id: "" });
  const submit = async () => {
    if (submitting || !name.trim()) return;
    const key = `${name.trim()}\n${description.trim()}`;
    if (request.current.key !== key) request.current = { key, id: rid() };
    setSubmitting(true);
    setError("");
    try {
      await assistants.createAssistant(request.current.id, name, description);
      onCreated();
      onClose();
    } catch (e) {
      setError(msg(e));
      setSubmitting(false);
    }
  };
  return (
    <Sheet title="New Assistant" submit={submitting ? "Creating…" : "Create"} busy={!name.trim() || submitting} error={error} onClose={onClose} onSubmit={() => void submit()}>
      <Field label="Name">
        <input autoFocus value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
      </Field>
      <Field label="What does it do?">
        <textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
      </Field>
    </Sheet>
  );
}

function NewTask({ assistantId, agents, onClose, act }: { assistantId: string; agents: AssistantsAgent[]; onClose: () => void; act: Act }) {
  const [title, setTitle] = useState("");
  const [brief, setBrief] = useState("");
  const [kind, setKind] = useState("research");
  const [agent, setAgent] = useState(agents[0]?.id ?? "pi");
  const [repoPath, setRepoPath] = useState("");
  const [requireChecks, setRequireChecks] = useState(false);
  const [failure, setFailure] = useState("");
  const valid = title.trim() !== "" && brief.trim() !== "" && (kind !== "coding" || repoPath.trim() !== "");
  return (
    <Sheet
      title="New task"
      submit="Queue Task"
      busy={!valid}
      error={failure}
      onClose={onClose}
      onSubmit={() => {
        void act(() =>
          assistants.createTask(rid(), {
            assistantId,
            kind,
            title: title.trim(),
            brief: brief.trim(),
            agent,
            repoPath: repoPath.trim() || undefined,
            requireChecks: kind === "coding" && requireChecks ? true : undefined,
          }),
        ).then((e) => (e ? setFailure(e) : onClose()));
      }}
    >
      <Field label="Title">
        <input autoFocus value={title} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field label="Brief">
        <textarea value={brief} onChange={(e) => setBrief(e.target.value)} rows={4} />
      </Field>
      <Field label="Kind">
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="research">Research (read-only)</option>
          <option value="coding">Coding (needs a repository)</option>
        </select>
      </Field>
      <Field label="Agent">
        <AgentPick agents={agents} value={agent} onChange={setAgent} />
      </Field>
      {kind === "coding" ? (
        <Field label="Repository path">
          <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="/path/to/repo" />
        </Field>
      ) : null}
      {kind === "coding" ? (
        <label className="as-meta">
          <input type="checkbox" checked={requireChecks} onChange={(e) => setRequireChecks(e.target.checked)} /> Require approved checks before review
        </label>
      ) : null}
    </Sheet>
  );
}
