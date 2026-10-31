import { App } from "../App";
import "./projects.css";
import { projects, type ProjectsView, type TaskPane, type TaskRow } from "../mygo";
import { ProjectAside, TaskAside } from "./Aside";
import { Compose } from "./Compose";
import { isMock, run, useProjectsView } from "./useProjects";
import { Dots, Mark, Menu, type MenuItem } from "./ui";

/**
 * The Projects page (product decision 2026-10-31): the centre column is the
 * existing Chat (the coordinator on a Project, the agent on a task), the right
 * column the board. Presentation only: it reads the bridge snapshot and every
 * write goes back to a native Shell function (confirm dialogs and toasts are
 * the native ones, drawn over this page).
 */
export function ProjectsPage() {
  const view = useProjectsView();
  if (!view) return <div className="pj-boot" />;
  if (!view.available || view.mode === "gone") {
    return (
      <div className="pj-gone">
        <h2>{view.available ? "This is no longer on the board" : "The work board isn't available"}</h2>
        {view.available ? (
          <button type="button" className="pj-btn" onClick={() => run(() => projects.back())}>
            Back
          </button>
        ) : null}
      </div>
    );
  }
  return (
    <div className="pj" data-mode={view.mode}>
      <section className="pj-main">
        {view.task ? <TaskHeader task={view.task} /> : null}
        <div className="pj-chat">
          <Center view={view} />
        </div>
      </section>
      <aside className="pj-aside" aria-label={view.mode === "task" ? "Task" : "Project"}>
        {view.mode === "task" && view.task ? <TaskAside key={view.task.id} task={view.task} /> : null}
        {view.mode === "project" && view.project ? <ProjectAside key={view.project.name} view={view} /> : null}
      </aside>
      {view.compose ? <Compose compose={view.compose} /> : null}
    </div>
  );
}

/** The Chat with the page's dock above its composer, or the reason there is none. */
function Center({ view }: { view: ProjectsView }) {
  const dock = <PageDock view={view} />;
  if (view.chat.target && view.chat.waiting) {
    // Stopped on a prompt before any conversation exists (e.g. "trust this folder").
    return (
      <div className="pj-nochat">
        <div className="pj-nochat-card">
          <h2>{view.chat.label} is waiting for you</h2>
          <p>It stopped on a question in its terminal before it could start a conversation. Answer it there; the chat appears right after.</p>
          <div className="row">
            <button type="button" className="pj-btn primary" onClick={() => run(() => projects.openInWorkspace())}>
              Open Terminal
            </button>
          </div>
        </div>
        <div className="pj-nochat-dock">{dock}</div>
      </div>
    );
  }
  if (view.chat.target) {
    // One Chat per target: the key remounts it so no draft or scroll leaks across.
    // The dev fixture has no MyGo runtime, so the Chat is stood in for by its dock slot.
    if (isMock) return <div className="pj-mockchat">{dock}</div>;
    return <App key={view.chat.target} dockAbove={dock} />;
  }
  return (
    <div className="pj-nochat">
      <div className="pj-nochat-card">
        <h2>{view.chat.label || "Nothing to talk to"}</h2>
        <p>{view.chat.why}</p>
        <div className="row">{noChatActions(view)}</div>
      </div>
      <div className="pj-nochat-dock">{dock}</div>
    </div>
  );
}

function noChatActions(view: ProjectsView) {
  if (view.mode === "project" && view.project && !view.project.other && !view.project.coordinator.running) {
    return (
      <button type="button" className="pj-btn primary" onClick={() => run(() => projects.manage("coordinator-start", view.project!.name))}>
        Start coordinator
      </button>
    );
  }
  if (view.mode === "task" && view.task) {
    const t = view.task;
    return (
      <>
        {t.actions
          .filter((a) => a.kind === "delegate" || a.kind === "delegate-with")
          .map((a) => (
            <button key={a.kind} type="button" className={`pj-btn${a.kind === "delegate" ? " primary" : ""}`} onClick={() => run(() => projects.act(a.kind, t.id))}>
              {a.label}
            </button>
          ))}
      </>
    );
  }
  return null;
}

/** What waits on the human, above the composer. */
function PageDock({ view }: { view: ProjectsView }) {
  if (view.mode === "task" && view.task) return <TaskDock task={view.task} />;
  if (view.mode === "project" && view.project) return <NeedsYouDock view={view} />;
  return null;
}

function NeedsYouDock({ view }: { view: ProjectsView }) {
  const { needsYou, ready } = view.counts;
  const waiting: TaskRow[] = [];
  for (const g of view.project?.groups ?? []) if (g.key === "needs" || g.key === "ready") waiting.push(...g.tasks);
  if (needsYou + ready === 0) return null;
  const shown = waiting.slice(0, 2);
  const title = needsYou > 0 ? `${needsYou} task${needsYou === 1 ? "" : "s"} need${needsYou === 1 ? "s" : ""} you` : `${ready} ready to approve`;
  return (
    <div className="pj-dock needs" role="status">
      <div className="head">
        <Mark tone={needsYou > 0 ? "attention" : "ready"} />
        <strong>{title}</strong>
        {needsYou > 0 && ready > 0 ? <span className="aside">{ready} ready to approve</span> : null}
      </div>
      <p>{shown.map((t) => `${t.id} ${t.msg || t.group}`).join(" · ")}</p>
      <div className="row">
        {shown.map((t) => (
          <button key={t.id} type="button" className="pj-btn" onClick={() => run(() => projects.openTask(t.id))}>
            {t.group === "ready" ? `Review ${t.id}` : `Open ${t.id}`}
          </button>
        ))}
      </div>
    </div>
  );
}

function TaskDock({ task }: { task: TaskPane }) {
  const dock = task.actions.filter((a) => a.dock);
  const pick = task.actions.some((a) => a.kind === "pick");
  if (dock.length === 0) return null;
  return (
    <div className="pj-dock" role="status">
      <div className="head">
        <Mark tone="attention" />
        <strong>{task.dockTitle}</strong>
      </div>
      {task.dockNote ? <p>{task.dockNote}</p> : null}
      <div className="row">
        {dock.map((a) => (
          <button key={a.kind} type="button" className={`pj-btn${a.primary ? " primary" : ""}`} onClick={() => run(() => projects.act(a.kind, task.id))}>
            {a.label}
          </button>
        ))}
        {pick ? (
          <button type="button" className="pj-btn" onClick={() => run(() => projects.openWorkBoard())}>
            Compare in Work Board
          </button>
        ) : null}
      </div>
    </div>
  );
}

/** Task id and title, the verb that stops it, ⋯ for the rest, then the pipeline. */
function TaskHeader({ task }: { task: TaskPane }) {
  const stop = task.actions.find((a) => a.kind === "stop");
  const items: (MenuItem | "-")[] = [];
  for (const a of task.actions) if (!a.dock && a.kind !== "stop") items.push({ label: a.label, onSelect: () => run(() => projects.act(a.kind, task.id)) });
  if (items.length) items.push("-");
  items.push({ label: task.pinned ? "Unpin" : "Pin", onSelect: () => run(() => projects.pin(task.id, !task.pinned)) });
  if (task.reportLink) items.push({ label: "Open Report", onSelect: () => run(() => projects.openReport(task.reportLink)) });
  if (task.hasAgent) items.push({ label: "Open in Workspace", onSelect: () => run(() => projects.openInWorkspace()) });
  if (task.canOpenProject) items.push({ label: "Open Project", onSelect: () => run(() => projects.openProjectWorkspace()) });
  items.push("-", { label: "Open Work Board", onSelect: () => run(() => projects.openWorkBoard()) });
  return (
    <header className="pj-head">
      <div className="top">
        <Mark tone={task.tone} size={16} />
        <span className="id">{task.id}</span>
        {/* The title lives in the title bar (one title per page). */}
        <span className="grow" />
        {stop ? (
          <button type="button" className="pj-btn" onClick={() => run(() => projects.act("stop", task.id))}>
            <span className="sq" aria-hidden="true" />
            Stop
          </button>
        ) : null}
        <Menu items={items} label="Task actions" icon={<Dots />} />
      </div>
      {task.strip.length > 0 ? (
        <ol className="pj-strip" aria-label="Pipeline">
          {task.strip.map((s) => (
            <li key={s.label} className={s.state}>
              <span className="bar" />
              <span className="lbl">{s.label}</span>
            </li>
          ))}
        </ol>
      ) : null}
    </header>
  );
}
