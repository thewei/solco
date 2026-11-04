import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { App } from "../App";
import { Icon } from "../icons";
import {
  assistants,
  chat,
  events,
  type AssistantsAgent,
  type AssistantsDetail,
  type AssistantsDrawer,
  type AssistantsRun,
  type AssistantsTask,
  type AssistantsView,
  type Approval,
  type CoordinatorChat,
  type Fact,
  type Goal,
  type GoalPlan,
  type InboxItem,
  type Routine,
} from "../mygo";
import { GroupChat, NewGroup } from "./GroupChat";
import { FactStrip, PlanCard, WorkPulse } from "./Progress";
import { ChatComposerBox, ChatComposerTextarea, ChatComposerToolbar, useComposerAutoSize } from "../composer-surface";
import { WorkStyle } from "./WorkStyle";
import { MemoryTab } from "./MemoryTab";
import { Behavior } from "./Behavior";
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

type Tab = "needs" | "watching" | "routines" | "tasks" | "done" | "memory" | "behavior" | "style" | "about";
type Form = "assistant" | "group" | "watch" | "task" | "rename" | null;

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
    setForm(i.kind === "group" ? "group" : "assistant");
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
        <p>An Assistant is a named helper you talk to. It keeps goals, tasks, memory and approvals.</p>
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
  const status = coordinatorStatus(a.status, co.state, detail.coordinatorOpen);
  const canStop = co.state === "starting" || co.state === "ready";
  const tabs: [Tab, string, number][] = [
    ["needs", "Needs you", needsCount],
    ["watching", "Watching", drawer ? drawer.goals.filter((g) => g.state === "draft" || g.state === "active" || g.state === "paused").length : 0],
    ["routines", "Routines", drawer ? drawer.routines.filter((r) => r.state === "draft" || r.state === "active").length : 0],
    ["tasks", "Tasks", drawer ? drawer.tasks.filter((t) => OPEN_TASK.has(t.state)).length : 0],
    ["done", "Done", 0],
    ["memory", "Memory", 0],
    ["behavior", "Behavior", 0],
    ["style", "Work style", 0],
    ["about", "About", 0],
  ];
  const choose = (fn: () => void) => () => {
    setMenu(false);
    fn();
  };
  return (
    <div className="as">
      <header className="as-head">
        <h1>{a.name}</h1>
        <span className={`as-state ${status.tone}`}>
          <span className={`as-dot ${status.tone}`} />
          {status.label}
        </span>
        {needsCount > 0 ? (
          <button type="button" className="as-needs" onClick={() => setStripOpen((v) => !v)}>
            {needsCount} {needsCount === 1 ? "needs" : "need"} you
          </button>
        ) : null}
        <span className="as-grow" />
        <button type="button" className={`as-ib ${tab ? "on" : ""}`} onClick={() => setTab(tab ? null : needsCount > 0 ? "needs" : "watching")}>
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
                <button type="button" role="menuitem" onClick={choose(() => setForm("watch"))}>
                  Watch a pull request…
                </button>
                <button type="button" role="menuitem" onClick={choose(() => setForm("task"))}>
                  New task…
                </button>
                <hr />
                <button type="button" role="menuitem" onClick={choose(() => setTab("style"))}>
                  Work style…
                </button>
                <button type="button" role="menuitem" onClick={choose(() => setForm("rename"))}>
                  Rename…
                </button>
                <button type="button" role="menuitem" onClick={choose(() => { archiveRequest.current = rid(); setConfirmArchive(true); })}>
                  Archive Assistant…
                </button>
                <button type="button" role="menuitem" onClick={choose(() => void act(() => assistants.setAssistantEnabled(rid(), a.id, a.status !== "active", a.revision)))}>
                  {a.status === "active" ? "Pause" : "Resume"} {a.name}
                </button>
                {a.status === "active" && co.state !== "none" ? (
                  <button type="button" role="menuitem" onClick={choose(() => void act(() => assistants.startCoordinator(rid(), a.id, "")))} disabled={canStop}>
                    Restart Coordinator
                  </button>
                ) : null}
                <hr />
                <button type="button" role="menuitem" onClick={choose(() => setForm("assistant"))}>
                  New Assistant…
                </button>
                {canStop ? (
                  <button type="button" role="menuitem" className="danger" onClick={choose(() => void act(() => assistants.stopCoordinator(rid(), a.id)))}>
                    Stop Coordinator
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
      {needsCount > 0 ? <Strip pending={pending} others={others} open={stripOpen} setOpen={setStripOpen} act={act} more={() => setTab("needs")} /> : null}
      <WorkPulse row={view.assistants.find((row) => row.id === a.id)} onTasks={() => setTab("tasks")} />
      <FactStrip facts={detail.facts} onOpenTask={() => setTab("tasks")} onOpenGoal={() => setTab("watching")} />
      {report?.state === "uncertain" ? (
        <section className="as-note warn as-report" aria-label="Coordinator progress report needs review">
          <strong>Progress report delivery uncertain</strong>
          <p>Herdr might have received the automatic update. Check the original OMP conversation first; Solco will not send it again without your decision.</p>
          <p className="as-meta">Activity through fact #{report.throughSeq}. This is delivery status, not an OMP reply.</p>
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
        <Conversation key={a.id} view={view} detail={detail} setError={setError} refresh={pull} />
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
              <DrawerBody tab={tab} detail={detail} drawer={drawer} view={view} pending={pending} act={act} pull={pull} />
            </div>
          </aside>
        ) : null}
      </main>
      {form === "assistant" ? <NewAssistant onClose={() => setForm(null)} onCreated={pull} /> : null}
      {form === "group" ? <NewGroup view={view} onClose={() => setForm(null)} onCreated={pull} /> : null}
      {form === "rename" ? <Rename assistant={a} onClose={() => setForm(null)} act={act} /> : null}
      {form === "watch" ? <NewWatch assistantId={a.id} agents={view.agents} onClose={() => setForm(null)} act={act} /> : null}
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
          <p>Check the original OMP conversation before proceeding. Solco cannot prove whether Herdr accepted the report at the last attempt.</p>
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
          <p>The Assistant will disappear from your active list. Conversations, goals, tasks and memories stay intact.</p>
          <p>Finish or cancel pending work, close the Assistant's Herdr panes, then archive. Solco checks pane closure before hiding it.</p>
          <p>Restore it later from Settings → Archived Assistants.</p>
        </Sheet>
      ) : null}
    </div>
  );
}

/** The header's one-word truth about the conversation. */
function coordinatorStatus(assistant: string, state: string, open: boolean): { label: string; tone: string } {
  if (assistant !== "active") return { label: "Paused", tone: "warn" };
  switch (state) {
    case "starting":
      return { label: "Starting…", tone: "work" };
    case "ready":
      return open ? { label: "Ready", tone: "ok" } : { label: "Not in this window", tone: "warn" };
    case "failed":
      return { label: "Could not start", tone: "bad" };
    case "blocked":
      return { label: "Needs permission", tone: "warn" };
    case "uncertain":
      return { label: "Check start", tone: "warn" };
    case "stopped":
      return { label: "Stopped", tone: "" };
  }
  return { label: "Not started", tone: "" };
}

/** What needs the person, above the chat: approvals decide here, anything else points at the drawer. */
function Strip({ pending, others, open, setOpen, act, more }: { pending: Approval[]; others: InboxItem[]; open: boolean; setOpen: (v: boolean) => void; act: Act; more: () => void }) {
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
              <button type="button" className="as-btn" onClick={more}>
                Open
              </button>
            </div>
          ))}
        </>
      ) : null}
    </section>
  );
}

function DrawerBody({ tab, detail, drawer, view, pending, act, pull }: { tab: Tab; detail: AssistantsDetail; drawer: AssistantsDrawer | null; view: AssistantsView; pending: Approval[]; act: Act; pull: () => void }) {
  const reasons = new Map(detail.inbox.items.filter((i) => i.kind !== "approval" && i.kind !== "news" && i.taskId).map((i) => [i.taskId as string, i]));
  if (tab === "about") return <About detail={detail} view={view} act={act} />;
  if (tab === "memory") return <MemoryTab id={detail.assistant.id} act={act} />;
  if (tab === "behavior") return <Behavior id={detail.assistant.id} act={act} />;
  if (tab === "style") return <WorkStyle id={detail.assistant.id} agents={view.agents} act={act} />;
  if (!drawer) return <Empty>Loading…</Empty>;
  const needTasks = drawer.tasks.filter((t) => reasons.has(t.id) || t.needsYou);
  const live = drawer.goals.filter((g) => g.state === "draft" || g.state === "active" || g.state === "paused");
  const open = drawer.tasks.filter((t) => OPEN_TASK.has(t.state));
  const done = drawer.tasks.filter((t) => !OPEN_TASK.has(t.state) && !t.needsYou);
  const closedGoals = drawer.goals.filter((g) => g.state === "completed" || g.state === "cancelled");
  switch (tab) {
    case "needs":
      return <NeedsYou approvals={pending} tasks={needTasks} reasons={reasons} act={act} pull={pull} />;
    case "watching":
      return <Watching goals={live} plans={drawer.plans} facts={drawer.facts} act={act} />;
    case "routines":
      return <Routines routines={drawer.routines} act={act} />;
    case "tasks":
      return <Tasks tasks={open} act={act} />;
    case "done":
      return <Done tasks={done} goals={closedGoals} act={act} />;
  }
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

const DRAFT_KEY = (id: string) => `as-draft:${id}`;

/**
 * The conversation. The Coordinator's real Chat once it is up; before that the
 * same shell with a composer of its own — the first Send starts the Coordinator
 * (idempotent requestId) and the typed text is held until the Chat is bound to
 * it, then sent exactly once. A start that fails gives the text back.
 */
function Conversation({ view, detail, setError, refresh }: { view: AssistantsView; detail: AssistantsDetail; setError: (e: string) => void; refresh: () => void }) {
  const a = detail.assistant;
  const co = detail.coordinator;
  const [bind, setBind] = useState<CoordinatorChat | null>(null);
  const [draft, setDraft] = useState(() => localStorage.getItem(DRAFT_KEY(a.id)) ?? "");
  const [agent, setAgent] = useState(detail.profileAgent || view.defaultAgent);
  const [held, setHeld] = useState<{ id: string; text: string; at: number } | null>(null);
  const sentFor = useRef("");
  const live = co.state === "ready" && detail.coordinatorOpen;
  useEffect(() => {
    if (!agent || !view.agents.some((x) => x.id === agent)) setAgent(detail.profileAgent || view.defaultAgent);
  }, [agent, view.agents, view.defaultAgent, detail.profileAgent]);
  useEffect(() => {
    if (!live) {
      setBind(null);
      return;
    }
    let cancelled = false;
    const attempt = () =>
      assistants
        .bindCoordinator(a.id)
        .then((r) => !cancelled && setBind(r))
        .catch(() => !cancelled && setBind(null));
    void attempt();
    // Until the Agent has reported its session (an invitation Chat has none),
    // look again.
    const timer = window.setInterval(() => void attempt(), bind?.invitation || !bind?.bound ? 1500 : 6000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [a.id, co.paneId, live, bind?.invitation, bind?.bound]);
  const bound = live && bind?.bound === true && bind.paneId === co.paneId;

  // The held first message goes out once: when the Chat is on the Coordinator
  // AND its composer takes a prompt (ready, or queued behind the Agent's own
  // first turn). A fresh Agent reports no status for a moment, and Send into
  // that would be refused without a word — so ask the Chat first, and look
  // again until it says yes.
  useEffect(() => {
    if (!held || !bound || sentFor.current === held.id) return;
    let cancelled = false;
    let timer = 0;
    const tryOnce = () => {
      chat
        .snapshot()
        .then((s) => {
          if (cancelled) return;
          if (s.disposition !== "ready" && s.disposition !== "queue") {
            timer = window.setTimeout(tryOnce, 700);
            return;
          }
          sentFor.current = held.id;
          return chat.send(held.text).then(() => {
            if (cancelled) return;
            setHeld(null);
            localStorage.removeItem(DRAFT_KEY(a.id));
          });
        })
        .catch((e) => {
          if (cancelled) return;
          sentFor.current = held.id;
          setHeld(null);
          setError(`Not sent (${msg(e)}): ${held.text}`);
        });
    };
    tryOnce();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [held, bound, a.id, setError]);
  // A start that failed, or took too long, gives the text back.
  useEffect(() => {
    if (!held) return;
    const giveBack = (why: string) => {
      setDraft(held.text);
      setHeld(null);
      setError(why);
    };
    if (!bound && sentFor.current !== held.id) {
      if (co.state === "failed") return giveBack("The Coordinator could not start. Your message was not sent and is back in the box.");
      if (co.state === "blocked") return giveBack("The Coordinator needs your permission in Terminal. Your message was not sent and is back in the box.");
      if (co.state === "uncertain") return giveBack("The Coordinator launch is uncertain. Your message was not sent and is back in the box; Solco will not start a second Agent.");
    }
    const timer = window.setTimeout(() => giveBack("The Coordinator is taking too long to start. Check the original Terminal before sending again; your text is back in the box."), Math.max(1000, HOLD_LIMIT_MS - (Date.now() - held.at)));
    return () => window.clearTimeout(timer);
  }, [held, bound, co.state, setError]);

  const send = () => {
    const text = draft.trim();
    if (!text || held || !agent) return;
    const item = { id: rid(), text, at: Date.now() };
    setHeld(item);
    setDraft("");
    localStorage.setItem(DRAFT_KEY(a.id), text);
    setError("");
    assistants
      .ensureChat(item.id, a.id, agent)
      .catch((e) => {
        setDraft(text);
        setHeld(null);
        setError(msg(e));
      })
      .finally(refresh);
  };

  if (bound) return <div className="as-chatwrap"><App key={`${a.id}|${co.paneId}`} /></div>;
  const starting = held !== null || co.state === "starting" || (live && !bind?.bound);
  const stranded = co.state === "ready" && !detail.coordinatorOpen;
  const blocked = co.state === "blocked";
  const uncertain = co.state === "uncertain";
  const paused = a.status !== "active";
  const noAgents = view.agentsReady && view.agents.length === 0;
  return (
    <div className="as-shell">
      <div className="as-shell-body">
        {held ? (
          <div className="as-thread">
            <div className="as-user">{held.text}</div>
            <p className="as-meta">Starting {agentLabel(view.agents, agent)}…</p>
          </div>
        ) : (
          <div className="as-hello">
            <h2>{a.name}</h2>
            <p>{blocked ? "The Coordinator is waiting for a permission or trust decision in Herdr. Resolve it in the original Terminal; Solco will not launch another Agent." : uncertain ? "Herdr has not confirmed whether the original Coordinator started. Solco is checking that session and will not risk launching a duplicate. Check the original Terminal before retrying." : stranded ? "This Assistant's Coordinator ran in another Herdr session, so it can't be opened here." : starting ? "Getting the Coordinator ready…" : co.state === "failed" ? "The Coordinator could not start. Send a message to try again." : "Talk to it like a colleague. It turns goals into tasks and keeps them moving; you approve anything risky."}</p>
            {a.description ? <p className="as-meta">{a.description}</p> : null}
            {(blocked || uncertain) && co.paneId ? (
              <button type="button" className="as-btn" onClick={() => {
                const paneId = co.paneId;
                if (!paneId) return;
                void assistants.openPane(paneId).then((opened) => {
                  if (!opened) setError("The original Coordinator pane is not available in this Herdr window.");
                }).catch((e) => setError(msg(e)));
              }}>Open original Terminal</button>
            ) : null}
          </div>
        )}
      </div>
      <Composer
        value={draft}
        onChange={setDraft}
        onSend={send}
        disabled={paused || noAgents || held !== null || starting || stranded || blocked || uncertain}
        placeholder={blocked ? "Resolve the permission request in Terminal" : uncertain ? "Checking the original Coordinator launch…" : paused ? "Paused — resume to start a conversation" : noAgents ? "Turn on an agent in Settings → Agents" : starting ? "Starting the Coordinator…" : stranded ? "Restart the Coordinator from ⋯" : `Message ${a.name}…`}
        agents={view.agents}
        agent={agent}
        onAgent={setAgent}
      />
    </div>
  );
}

function agentLabel(agents: AssistantsAgent[], id: string): string {
  return agents.find((x) => x.id === id)?.label ?? "the Coordinator";
}

/**
 * The composer before a Coordinator exists. It wears the Chat page's own
 * markup (`.composer > .col > .cmp > textarea + .cbar`, the `.chip.modelchip`
 * agent chip, the round `.send`) so the conversation looks the same before and
 * after the first message — only the controls the Chat needs a live Agent for
 * (the + menu, model panel, context ring) are absent.
 */
function Composer({ value, onChange, onSend, disabled, placeholder, agents, agent, onAgent }: { value: string; onChange: (v: string) => void; onSend: () => void; disabled: boolean; placeholder: string; agents: AssistantsAgent[]; agent: string; onAgent: (v: string) => void; onStop?: undefined }) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useComposerAutoSize(ref, value);
  const key = (e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    // Enter sends; Shift+Enter breaks the line; an IME's own Enter never sends.
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      if (!disabled) onSend();
    }
  };
  return (
    <div className="composer">
      <div className="col">
        <ChatComposerBox>
          <ChatComposerTextarea ref={ref} rows={1} value={value} placeholder={placeholder} aria-label="Chat prompt" spellCheck disabled={disabled} onChange={(e) => onChange(e.target.value)} onKeyDown={key} />
          <ChatComposerToolbar>
            <span className="sp" />
            {agents.length > 1 ? (
              <select className="chip mono modelchip as-agent" aria-label="Agent that runs the Coordinator" title="Agent that runs the Coordinator" value={agent} disabled={disabled} onChange={(e) => onAgent(e.target.value)}>
                {agents.map((x) => (
                  <option key={x.id} value={x.id}>
                    {x.label}
                  </option>
                ))}
              </select>
            ) : agents.length === 1 ? (
              <span className="chip mono">{agents[0].label}</span>
            ) : null}
            <button type="button" className="send" title="Send" aria-label="Send" disabled={disabled || value.trim() === ""} onClick={onSend}>
              <Icon name="up" size={16} stroke={2.2} />
            </button>
          </ChatComposerToolbar>
        </ChatComposerBox>
      </div>
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

function Watching({ goals, plans, facts, act }: { goals: Goal[]; plans: GoalPlan[]; facts: Fact[]; act: Act }) {
  if (goals.length === 0) return <Empty>No goals yet. Start with “Watch a PR”.</Empty>;
  return (
    <>
      {goals.map((g) => (
        <section key={g.id} className="as-card">
          <div className="as-row">
            <strong>{g.title}</strong>
            <span className={`as-chip ${g.state}`}>{g.state}</span>
          </div>
          {g.objective ? <p className="as-text">{g.objective}</p> : null}
          {plans.find((p) => p.goalId === g.id) ? <PlanCard plan={plans.find((p) => p.goalId === g.id) as GoalPlan} facts={facts.filter((f) => f.goalId === g.id)} /> : null}
          {g.commitments.map((c) => (
            <p key={c.id} className="as-meta">
              {c.kind === "pr_watch" ? watchLabel(c.spec) : c.kind}
              {c.lastError ? ` · ${c.lastError}` : c.lastCheckedAt ? ` · looked ${ago(c.lastCheckedAt)}` : " · not looked yet"}
            </p>
          ))}
          <div className="as-row">
            {g.state === "draft" ? (
              <button type="button" className="as-btn primary" onClick={() => act(() => assistants.goalAction(rid(), "approve", g.id, g.revision, ""))}>
                Start
              </button>
            ) : null}
            {g.state === "active" ? (
              <>
                <button type="button" className="as-btn" onClick={() => act(() => assistants.goalAction(rid(), "pause", g.id, g.revision, ""))}>
                  Pause
                </button>
                <button
                  type="button"
                  className="as-btn"
                  onClick={() => {
                    const evidence = window.prompt("What shows this goal is done?");
                    if (evidence) void act(() => assistants.goalAction(rid(), "complete", g.id, g.revision, evidence));
                  }}
                >
                  Complete…
                </button>
              </>
            ) : null}
            {g.state === "paused" ? (
              <button type="button" className="as-btn" onClick={() => act(() => assistants.goalAction(rid(), "resume", g.id, g.revision, ""))}>
                Resume
              </button>
            ) : null}
            <button type="button" className="as-btn danger" onClick={() => act(() => assistants.goalAction(rid(), "cancel", g.id, g.revision, ""))}>
              Cancel
            </button>
          </div>
        </section>
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

function Done({ tasks, goals, act }: { tasks: AssistantsTask[]; goals: Goal[]; act: Act }) {
  if (tasks.length === 0 && goals.length === 0) return <Empty>Nothing finished yet.</Empty>;
  return (
    <>
      {goals.map((g) => (
        <section key={g.id} className="as-card quiet">
          <div className="as-row">
            <strong>{g.title}</strong>
            <span className={`as-chip ${g.state}`}>{g.state}</span>
          </div>
          {g.evidence ? <p className="as-text">{g.evidence}</p> : null}
        </section>
      ))}
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
      <div className="as-row">
        {t.needsYou ? (
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

function watchLabel(spec: string): string {
  try {
    const s = JSON.parse(spec) as { repo?: string; number?: number };
    return `${s.repo ?? "?"}#${s.number ?? "?"}`;
  } catch {
    return "PR watch";
  }
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

function NewWatch({ assistantId, agents, onClose, act }: { assistantId: string; agents: AssistantsAgent[]; onClose: () => void; act: Act }) {
  const [repo, setRepo] = useState("");
  const [number, setNumber] = useState("");
  const [minutes, setMinutes] = useState("5");
  const [agent, setAgent] = useState(agents[0]?.id ?? "pi");
  const [repoPath, setRepoPath] = useState("");
  const [failure, setFailure] = useState("");
  const valid = /^[\w.-]+\/[\w.-]+$/.test(repo.trim()) && Number(number) > 0;
  return (
    <Sheet
      title="Watch a pull request"
      submit="Start Watching"
      busy={!valid}
      error={failure}
      onClose={onClose}
      onSubmit={() => {
        const n = Number(number);
        const seconds = Math.min(86400, Math.max(10, Math.round(Number(minutes) * 60) || 300));
        void act(async () => {
          await assistants.createGoal(rid(), {
            assistantId,
            title: `Watch ${repo.trim()}#${n}`,
            objective: "Tell me when it changes.",
            watch: { repo: repo.trim(), number: n, intervalSeconds: seconds, agent, model: "", repoPath: repoPath.trim() },
          });
        }).then((e) => (e ? setFailure(e) : onClose()));
      }}
    >
      <Field label="Repository (owner/name)">
        <input autoFocus value={repo} onChange={(e) => setRepo(e.target.value)} placeholder="owner/repo" />
      </Field>
      <Field label="Pull request number">
        <input value={number} onChange={(e) => setNumber(e.target.value)} inputMode="numeric" />
      </Field>
      <Field label="Look every (minutes)">
        <input value={minutes} onChange={(e) => setMinutes(e.target.value)} inputMode="numeric" />
      </Field>
      <Field label="Agent that reads a change">
        <AgentPick agents={agents} value={agent} onChange={setAgent} />
      </Field>
      <Field label="Local checkout (optional)">
        <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="/path/to/repo" />
      </Field>
      <p className="as-meta">The goal starts as a draft: press Start on it to begin looking.</p>
    </Sheet>
  );
}

function NewTask({ assistantId, agents, onClose, act }: { assistantId: string; agents: AssistantsAgent[]; onClose: () => void; act: Act }) {
  const [title, setTitle] = useState("");
  const [brief, setBrief] = useState("");
  const [kind, setKind] = useState("research");
  const [agent, setAgent] = useState(agents[0]?.id ?? "pi");
  const [repoPath, setRepoPath] = useState("");
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
    </Sheet>
  );
}
