import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { App } from "../App";
import {
  assistants,
  type CoordinatorChat,
  type AssistantsDetail,
  type AssistantsRun,
  type AssistantsTask,
  type AssistantsView,
  type Approval,
  type Goal,
} from "../mygo";
import "./assistants.css";

/**
 * The Assistants page (product decision 2026-11-03). It reads one bridge
 * snapshot (the plugin's Assistants core answers; nothing is cached here) and
 * every action goes back through the bridge with a fresh requestId, so a
 * double click replays instead of duplicating. A refusal shows the plugin's
 * own sentence. The page polls; the sidebar's counters follow each read.
 */

type Tab = "coordinator" | "needs" | "watching" | "tasks" | "done" | "memory";
type Form = "assistant" | "watch" | "task" | null;

const rid = () => crypto.randomUUID();
const OPEN_TASK = new Set(["draft", "waiting", "queued", "claimed", "starting", "running", "blocked", "review_required", "uncertain"]);

function useAssistants() {
  const [view, setView] = useState<AssistantsView | null>(null);
  const [error, setError] = useState("");
  const pull = useCallback(() => {
    assistants.snapshot().then(setView).catch((e) => setError(msg(e)));
  }, []);
  useEffect(() => {
    pull();
    const timer = window.setInterval(pull, 3000);
    return () => window.clearInterval(timer);
  }, [pull]);
  useEffect(() => {
    if (view?.theme) document.documentElement.dataset.theme = view.theme;
  }, [view?.theme]);
  /** Run one command; show the refusal, then re-read. */
  const act = useCallback(
    async (call: () => Promise<unknown>) => {
      setError("");
      try {
        await call();
      } catch (e) {
        setError(msg(e));
      }
      pull();
    },
    [pull],
  );
  return { view, error, setError, pull, act };
}

function msg(e: unknown): string {
  const text = e instanceof Error ? e.message : String(e);
  return text.replace(/^[a-z_]+: /, "");
}

export function AssistantsPage() {
  const { view, error, setError, pull, act } = useAssistants();
  const [tab, setTab] = useState<Tab>("needs");
  const [form, setForm] = useState<Form>(null);
  useEffect(() => {
    if (view?.newForm) setForm("assistant");
  }, [view?.newForm]);

  if (!view) return <div className="as-boot" />;
  if (!view.available) {
    return (
      <div className="as-empty">
        <h2>Assistants aren't available</h2>
        <p>Install or update the Solco Automations plugin to turn them on.</p>
      </div>
    );
  }
  const detail = view.detail;
  if (!detail) {
    return (
      <div className="as-empty">
        <h2>No Assistants yet</h2>
        <p>An Assistant is a named helper with its own goals, tasks, memory and approvals.</p>
        <button type="button" className="as-btn primary" onClick={() => setForm("assistant")}>
          New Assistant
        </button>
        {form === "assistant" ? <NewAssistant onClose={() => setForm(null)} act={act} /> : null}
      </div>
    );
  }
  const a = detail.assistant;
  const pending = detail.approvals.filter((p) => p.state === "pending");
  const needTasks = detail.tasks.filter((t) => t.needsYou);
  const needsCount = pending.length + needTasks.length;
  const live = detail.goals.filter((g) => g.state === "draft" || g.state === "active" || g.state === "paused");
  const open = detail.tasks.filter((t) => OPEN_TASK.has(t.state));
  const done = detail.tasks.filter((t) => !OPEN_TASK.has(t.state) && !t.needsYou);
  const closedGoals = detail.goals.filter((g) => g.state === "completed" || g.state === "cancelled");
  const tabs: [Tab, string, number][] = [
    ["coordinator", "Coordinator", 0],
    ["needs", "Needs You", needsCount],
    ["watching", "Watching", live.length],
    ["tasks", "Tasks", open.length],
    ["done", "Done", done.length + closedGoals.length],
    ["memory", "Memory", detail.memory.length],
  ];
  return (
    <div className="as">
      <header className="as-head">
        <div className="as-title">
          <h1>{a.name}</h1>
          <span className={`as-chip ${a.status}`}>{a.status === "active" ? "Active" : "Paused"}</span>
        </div>
        {a.description ? <p className="as-desc">{a.description}</p> : null}
        <div className="as-actions">
          <button type="button" className="as-btn" onClick={() => setForm("watch")}>
            Watch a PR
          </button>
          <button type="button" className="as-btn" onClick={() => setForm("task")}>
            New Task
          </button>
          <button
            type="button"
            className="as-btn"
            onClick={() => act(() => assistants.setAssistantEnabled(rid(), a.id, a.status !== "active", a.revision))}
          >
            {a.status === "active" ? "Pause" : "Resume"}
          </button>
        </div>
      </header>
      {error ? (
        <div className="as-error" role="alert">
          {error}
          <button type="button" className="as-link" onClick={() => setError("")}>
            Dismiss
          </button>
        </div>
      ) : null}
      <nav className="as-tabs" role="tablist">
        {tabs.map(([id, label, n]) => (
          <button key={id} type="button" role="tab" aria-selected={tab === id} className={tab === id ? "on" : ""} onClick={() => setTab(id)}>
            {label}
            {n > 0 ? <span className="as-count">{n}</span> : null}
          </button>
        ))}
      </nav>
      <main className={tab === "coordinator" ? "as-body chat" : "as-body"}>
        {tab === "coordinator" ? <Coordinator detail={detail} act={act} /> : null}
        {tab === "needs" ? <NeedsYou approvals={pending} tasks={needTasks} act={act} pull={pull} /> : null}
        {tab === "watching" ? <Watching goals={live} act={act} /> : null}
        {tab === "tasks" ? <Tasks tasks={open} act={act} /> : null}
        {tab === "done" ? <Done tasks={done} goals={closedGoals} act={act} /> : null}
        {tab === "memory" ? <Memory detail={detail} /> : null}
      </main>
      {form === "assistant" ? <NewAssistant onClose={() => setForm(null)} act={act} /> : null}
      {form === "watch" ? <NewWatch assistantId={a.id} onClose={() => setForm(null)} act={act} /> : null}
      {form === "task" ? <NewTask assistantId={a.id} onClose={() => setForm(null)} act={act} /> : null}
    </div>
  );
}

type Act = (call: () => Promise<unknown>) => Promise<void>;

function Empty({ children }: { children: ReactNode }) {
  return <p className="as-none">{children}</p>;
}

function NeedsYou({ approvals, tasks, act }: { approvals: Approval[]; tasks: AssistantsTask[]; act: Act; pull: () => void }) {
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
        <TaskCard key={t.id} task={t} act={act} />
      ))}
    </>
  );
}

/**
 * The Assistant's own Coordinator: the ordinary Chat page bound to its real
 * Herdr Agent. The page asks the bridge to bind first (the pane comes from the
 * plugin's record of the Coordinator's run, never a guess), then mounts the one
 * Chat; a Coordinator that is not up yet shows why, with Start.
 */
function Coordinator({ detail, act }: { detail: AssistantsDetail; act: Act }) {
  const a = detail.assistant;
  const co = detail.coordinator;
  const [bind, setBind] = useState<CoordinatorChat | null>(null);
  const live = co.state === "ready" && detail.coordinatorOpen;
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
    // Until the Agent has reported its session, look again.
    const timer = window.setInterval(() => void attempt(), 3000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [a.id, co.paneId, live]);
  if (live && bind?.bound && bind.paneId === co.paneId) return <App key={`${a.id}|${co.paneId}`} />;
  const busy = co.state === "starting" || (live && !bind?.bound);
  const canStart = co.state === "none" || co.state === "stopped" || co.state === "failed";
  const why = live ? (bind?.reason ?? "Connecting to the Coordinator…") : co.state === "ready" ? "The Coordinator is not open in this window." : stateLine(co.state);
  return (
    <div className="as-nochat">
      <div className="as-nochat-card">
        <h2>{a.name}'s Coordinator</h2>
        <p>{why}</p>
        <div className="as-row">
          {canStart ? (
            <button type="button" className="as-btn primary" disabled={a.status !== "active"} onClick={() => act(() => assistants.startCoordinator(rid(), a.id))}>
              {co.state === "none" ? "Start Coordinator" : "Start again"}
            </button>
          ) : null}
          {co.state === "starting" || co.state === "ready" ? (
            <button type="button" className="as-btn" onClick={() => act(() => assistants.stopCoordinator(rid(), a.id))}>
              Stop
            </button>
          ) : null}
        </div>
        {busy ? <p className="as-meta">This can take a few seconds.</p> : null}
      </div>
    </div>
  );
}

function stateLine(state: string): string {
  switch (state) {
    case "starting":
      return "The Coordinator is starting.";
    case "failed":
      return "The Coordinator could not start.";
    case "stopped":
      return "The Coordinator is stopped.";
  }
  return "The Coordinator turns your goals into tasks and keeps them moving. Start it to talk to it.";
}

function Watching({ goals, act }: { goals: Goal[]; act: Act }) {
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

function TaskCard({ task: t, act }: { task: AssistantsTask; act: Act }) {
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

function Memory({ detail }: { detail: AssistantsDetail }) {
  if (detail.memory.length === 0) return <Empty>No memory yet. Memory is only kept after you approve it.</Empty>;
  return (
    <>
      {detail.memory.map((m) => (
        <section key={m.id} className="as-card quiet">
          <p className="as-text">{m.content}</p>
          <p className="as-meta">
            {m.kind} · {m.scope}
            {m.sourceRef ? ` · ${m.sourceRef}` : ""}
          </p>
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
function Sheet({ title, submit, busy, onClose, onSubmit, children }: { title: string; submit: string; busy: boolean; onClose: () => void; onSubmit: () => void; children: ReactNode }) {
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

function NewAssistant({ onClose, act }: { onClose: () => void; act: Act }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  return (
    <Sheet
      title="New Assistant"
      submit="Create"
      busy={!name.trim()}
      onClose={onClose}
      onSubmit={() => {
        void act(() => assistants.createAssistant(rid(), name, description));
        onClose();
      }}
    >
      <Field label="Name">
        <input autoFocus value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
      </Field>
      <Field label="What does it do?">
        <textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
      </Field>
    </Sheet>
  );
}

function NewWatch({ assistantId, onClose, act }: { assistantId: string; onClose: () => void; act: Act }) {
  const [repo, setRepo] = useState("");
  const [number, setNumber] = useState("");
  const [minutes, setMinutes] = useState("5");
  const [agent, setAgent] = useState("pi");
  const [repoPath, setRepoPath] = useState("");
  const valid = /^[\w.-]+\/[\w.-]+$/.test(repo.trim()) && Number(number) > 0;
  return (
    <Sheet
      title="Watch a pull request"
      submit="Start Watching"
      busy={!valid}
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
        });
        onClose();
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
        <input value={agent} onChange={(e) => setAgent(e.target.value)} />
      </Field>
      <Field label="Local checkout (optional)">
        <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="/path/to/repo" />
      </Field>
      <p className="as-meta">The goal starts as a draft: press Start on it to begin looking.</p>
    </Sheet>
  );
}

function NewTask({ assistantId, onClose, act }: { assistantId: string; onClose: () => void; act: Act }) {
  const [title, setTitle] = useState("");
  const [brief, setBrief] = useState("");
  const [kind, setKind] = useState("research");
  const [agent, setAgent] = useState("pi");
  const [repoPath, setRepoPath] = useState("");
  const valid = title.trim() !== "" && brief.trim() !== "" && (kind !== "coding" || repoPath.trim() !== "");
  return (
    <Sheet
      title="New task"
      submit="Queue Task"
      busy={!valid}
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
        );
        onClose();
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
        <input value={agent} onChange={(e) => setAgent(e.target.value)} />
      </Field>
      {kind === "coding" ? (
        <Field label="Repository path">
          <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="/path/to/repo" />
        </Field>
      ) : null}
    </Sheet>
  );
}
