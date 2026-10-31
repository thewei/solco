import { useEffect, useMemo, useState, type ReactNode } from "react";
import { projects, type LibraryItemPane, type ProjectPane, type ProjectsView, type TaskGroupPane, type TaskPane } from "../mygo";
import { Icon } from "../icons";
import { Dots, Mark, Menu, Tabs, type MenuItem } from "./ui";
import { run } from "./useProjects";
import { EditDescription } from "./Compose";

/** The right column: the board on a Project home, the Inspector on a task. */

type ProjectTab = "tasks" | "library" | "reports" | "auto";

export function ProjectAside({ view }: { view: ProjectsView }) {
  const project = view.project!;
  const [tab, setTab] = useState<ProjectTab>("tasks");
  const tasks = project.groups.reduce((n, g) => n + g.tasks.length, 0);
  useEffect(() => {
    if (tab === "reports") run(() => projects.loadTab("reports"));
  }, [tab]);
  return (
    <>
      <Tabs
        value={tab}
        onChange={setTab}
        tabs={[
          { id: "tasks", label: "Tasks", count: tasks },
          { id: "library", label: "Library", count: project.library.items.length },
          { id: "reports", label: "Reports", count: project.reports.rows.length },
          { id: "auto", label: "Auto", count: project.automations.length },
        ]}
      />
      <div className="pj-pane">
        <CoordinatorCard project={project} />
        {tab === "tasks" ? <TasksTab project={project} /> : null}
        {tab === "library" ? <LibraryTab project={project} /> : null}
        {tab === "reports" ? <ReportsTab project={project} /> : null}
        {tab === "auto" ? <AutoTab project={project} /> : null}
      </div>
      {tab === "tasks" ? (
        <footer className="pj-foot">
          <button type="button" className="pj-btn" onClick={() => run(() => projects.openCompose(false))}>
            <Icon name="plus" size={13} /> New task
          </button>
        </footer>
      ) : null}
    </>
  );
}

function CoordinatorCard({ project }: { project: ProjectPane }) {
  const co = project.coordinator;
  const live = co.running;
  const items: (MenuItem | "-")[] = [
    { label: project.pinned ? "Unpin Project" : "Pin Project", onSelect: () => run(() => projects.pin(project.name, !project.pinned)) },
  ];
  if (!project.other) {
    items.push("-");
    if (live) {
      items.push(
        { label: "Restart Coordinator", onSelect: () => run(() => projects.manage("coordinator-restart", project.name)) },
        { label: "New Coordinator Conversation…", onSelect: () => run(() => projects.manage("coordinator-new", project.name)) },
        { label: "Stop Coordinator", onSelect: () => run(() => projects.manage("coordinator-stop", project.name)) },
        { label: "Open in Workspace", onSelect: () => run(() => projects.openInWorkspace()) },
      );
    } else {
      items.push({ label: "Start Coordinator", onSelect: () => run(() => projects.manage("coordinator-start", project.name)) });
    }
    items.push(
      project.autopilot
        ? { label: "Turn Autopilot Off", onSelect: () => run(() => projects.manage("autopilot-off", project.name)) }
        : { label: "Turn Autopilot On…", onSelect: () => run(() => projects.manage("autopilot-on", project.name)) },
    );
    items.push("-");
    if (project.doneCount > 0) items.push({ label: "Archive Done Tasks…", onSelect: () => run(() => projects.manage("archive-done", project.name)) });
    if (project.working > 0) items.push({ label: "Stop All Tasks…", onSelect: () => run(() => projects.manage("stop-all", project.name)), danger: true });
    items.push({ label: "Archive Project…", onSelect: () => run(() => projects.manage("archive-project", project.name)), danger: true });
  }
  items.push("-", { label: "Open Work Board", onSelect: () => run(() => projects.openWorkBoard()) });
  const state = !live ? "not running" : [co.agent, co.state].filter(Boolean).join(" · ");
  return (
    <div className="pj-co">
      <Mark tone={co.state === "blocked" ? "attention" : co.state === "working" || co.state === "starting" ? "working" : "idle"} />
      <div className="who">
        <strong>{project.other ? "Other tasks" : "Coordinator"}</strong>
        <span>{project.other ? "No repository" : state}{project.autopilot ? " · autopilot" : ""}</span>
      </div>
      <Menu items={items} label="Project actions" icon={<Dots />} />
    </div>
  );
}

function match(row: { id: string; title: string; msg: string; app: string }, q: string) {
  const needle = q.trim().toLowerCase();
  return !needle || [row.id, row.title, row.msg, row.app].some((v) => v.toLowerCase().includes(needle));
}

function TasksTab({ project }: { project: ProjectPane }) {
  const [find, setFind] = useState("");
  const [folded, setFolded] = useState<Record<string, boolean>>({ done: true });
  const groups: TaskGroupPane[] = useMemo(() => {
    const all = [...project.groups, ...(project.done.length ? [{ key: "done", label: "Done", tone: "idle", tasks: project.done }] : [])];
    return all.map((g) => ({ ...g, tasks: g.tasks.filter((t) => match(t, find)) })).filter((g) => g.tasks.length);
  }, [project.groups, project.done, find]);
  return (
    <>
      <label className="pj-find">
        <Icon name="search" size={13} />
        <input value={find} onChange={(e) => setFind(e.target.value)} placeholder="Find tasks" aria-label="Find tasks" />
      </label>
      {groups.length === 0 ? <p className="pj-empty">{find ? "No matching tasks" : "No tasks yet. Add one, or ask the coordinator."}</p> : null}
      {groups.map((g) => {
        const open = find ? true : !folded[g.key];
        return (
          <section key={g.key} className="pj-group">
            <button type="button" className="gh" aria-expanded={open} onClick={() => setFolded((f) => ({ ...f, [g.key]: open }))}>
              <span className={`chev${open ? " open" : ""}`}>›</span>
              <span className={`gl ${g.tone}`}>{g.label}</span>
              <span className="gn">{g.tasks.length}</span>
            </button>
            {open
              ? g.tasks.map((t) => (
                  <button key={t.id} type="button" className="pj-trow" onClick={() => run(() => projects.openTask(t.id))}>
                    <Mark tone={t.tone} />
                    <span className="body">
                      <span className="line">
                        <span className="title">{t.pinned ? "★ " : ""}{t.title}</span>
                        <span className="meta">
                          {t.round > 0 ? <em>R#{t.round}</em> : null}
                          {t.pr ? <em>PR#{t.pr}</em> : null}
                          <span className="id">{t.id}</span>
                          {t.app ? <span className="app" title={t.app}>{t.app[0].toUpperCase()}</span> : null}
                        </span>
                      </span>
                      {t.msg ? <span className={`sub ${t.group === "needs" ? "attention" : ""}`}>{t.msg}</span> : null}
                    </span>
                  </button>
                ))
              : null}
          </section>
        );
      })}
    </>
  );
}

const MODE_LABEL: Record<string, string> = { always: "Always", reference: "Reference", task: "Task" };

function kindIcon(kind: string) {
  return <Icon name={kind === "image" ? "image" : "read"} size={15} />;
}

function LibraryRow({ item }: { item: LibraryItemPane }) {
  return (
    <div className="pj-lrow">
      <span className="ic">{kindIcon(item.kind)}</span>
      <span className="nm" title={item.name}>{item.name}</span>
      <span className="sz">{item.sizeText}</span>
      <Menu
        label={`${item.name} actions`}
        items={[
          ...(["always", "reference", "task"] as const).map((m) => ({
            label: `${item.mode === m ? "✓ " : ""}${MODE_LABEL[m]}`,
            onSelect: () => run(() => projects.setLibraryMode(item.id, m)),
          })),
          "-",
          { label: "Remove", danger: true, onSelect: () => run(() => projects.removeLibraryItem(item.id)) },
        ]}
      />
    </div>
  );
}

function LibraryTab({ project }: { project: ProjectPane }) {
  const lib = project.library;
  if (!lib.available) return <p className="pj-empty">The library needs a repository and the app's data folder.</p>;
  const always = lib.items.filter((i) => i.mode === "always");
  const reference = lib.items.filter((i) => i.mode === "reference");
  const task = lib.items.filter((i) => i.mode === "task");
  const pct = Math.min(100, Math.round((lib.alwaysUsed / lib.alwaysCap) * 100));
  return (
    <>
      <div className="pj-lhead">
        <div>
          <strong>Library</strong>
          <span>{lib.items.length} item{lib.items.length === 1 ? "" : "s"}{lib.items.length ? ` · ${lib.totalText}` : ""} · this project only</span>
        </div>
        <button type="button" className="pj-btn" onClick={() => run(() => projects.pickFiles("reference"))}>
          <Icon name="plus" size={13} /> Add
        </button>
      </div>
      <button type="button" className="pj-drop" onClick={() => run(() => projects.pickFiles("reference"))}>
        <strong>Add specs, screenshots, notes</strong>
        <span>Agents open them by path when they need background. Nothing is sent anywhere.</span>
      </button>
      <div className="pj-budget">
        <div className="bh">
          <Icon name="shield" size={13} />
          <strong>Always read</strong>
          <span>{(lib.alwaysUsed / 1024).toFixed(1)} / {(lib.alwaysCap / 1024).toFixed(1)} KB</span>
        </div>
        <div className="bar"><i style={{ width: `${pct}%` }} /></div>
        <p>Named in every agent's instructions, every turn. Keep it short; everything else stays on demand.</p>
      </div>
      <Section title="Always read" items={always} />
      <Section title="Reference · on demand" right={reference.length ? `INDEX.md · ${reference.length}` : ""} items={reference} />
      <Section title="Sent with a task" items={task} />
      {lib.note ? <p className="pj-note">{lib.note}</p> : null}
      <p className="pj-note">
        Stored under ProjectLibrary/{project.name}/ next to an INDEX.md. Agents are told where it is once, in the instructions they already get, and read files with their own tools.
      </p>
    </>
  );
}

function Section({ title, right, items }: { title: string; right?: string; items: LibraryItemPane[] }): ReactNode {
  if (items.length === 0) return null;
  return (
    <section className="pj-lsec">
      <div className="sh"><span>{title}</span>{right ? <span>{right}</span> : null}</div>
      {items.map((i) => <LibraryRow key={i.id} item={i} />)}
    </section>
  );
}

function ReportsTab({ project }: { project: ProjectPane }) {
  const r = project.reports;
  if (r.rows.length === 0) return <p className="pj-empty">{r.loading ? "Reading reports…" : "No reports yet. A research task writes one."}</p>;
  return (
    <section className="pj-lsec">
      {r.rows.map((row) => (
        <div key={row.taskId} className="pj-lrow">
          <span className="ic"><Icon name="read" size={15} /></span>
          <span className="nm" title={row.title}>{row.title}</span>
          <span className="sz">{row.taskId}</span>
          <button type="button" className="pj-btn small" aria-label={`Open report of ${row.taskId}`} onClick={() => run(() => projects.openReport(row.link))}>Open</button>
        </div>
      ))}
      {r.loading ? <p className="pj-note">Reading more…</p> : null}
    </section>
  );
}

function AutoTab({ project }: { project: ProjectPane }) {
  return (
    <>
      <div className="pj-lhead">
        <div><strong>Automations</strong><span>{project.automations.length ? "Run on a schedule or an event" : "None yet"}</span></div>
        {project.other ? null : (
          <button type="button" className="pj-btn" onClick={() => run(() => projects.manage("automation-new", project.name))}>
            <Icon name="plus" size={13} /> New
          </button>
        )}
      </div>
      {project.automations.map((a) => (
        <div key={a.id} className="pj-arow">
          <div className="who">
            <strong title={a.name}>{a.name}</strong>
            <span title={a.caption}>{a.caption}</span>
          </div>
          <button type="button" className="pj-btn small" onClick={() => run(() => projects.manage(a.on ? "automation-off" : "automation-on", a.id))}>
            {a.on ? "Turn Off" : "Turn On"}
          </button>
          <Menu
            label={`${a.name} actions`}
            items={[
              { label: "Run…", onSelect: () => run(() => projects.manage("automation-run", a.id)) },
              { label: "Remove…", danger: true, onSelect: () => run(() => projects.manage("automation-remove", a.id)) },
            ]}
          />
        </div>
      ))}
    </>
  );
}

// ---------------------------------------------------------------- task

type TaskTab = "run" | "context" | "changes" | "review" | "settings";

export function TaskAside({ task }: { task: TaskPane }) {
  const [tab, setTab] = useState<TaskTab>("run");
  const tabs: { id: TaskTab; label: string }[] = [{ id: "run", label: "Run" }, { id: "context", label: "Context" }];
  if (task.worktree) tabs.push({ id: "changes", label: "Changes" });
  if (task.review) tabs.push({ id: "review", label: "Review" });
  tabs.push({ id: "settings", label: "Settings" });
  const shown = tabs.some((t) => t.id === tab) ? tab : "run";
  useEffect(() => {
    if (shown === "changes") run(() => projects.loadTab("changes"));
    if (shown === "settings") run(() => projects.loadTab("settings"));
  }, [shown]);
  return (
    <>
      <Tabs value={shown} onChange={setTab} tabs={tabs} />
      <div className="pj-pane">
        {shown === "run" ? <RunTab task={task} /> : null}
        {shown === "context" ? <ContextTab task={task} /> : null}
        {shown === "changes" ? <ChangesTab task={task} /> : null}
        {shown === "review" ? <ReviewTab task={task} /> : null}
        {shown === "settings" ? <SettingsTab task={task} /> : null}
      </div>
    </>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="pj-card">
      <h3>{title}</h3>
      {children}
    </section>
  );
}

function RunTab({ task }: { task: TaskPane }) {
  const n = task.narrative;
  return (
    <>
      <dl className="pj-facts">
        {task.facts.map((f) => (
          <div key={f.label}>
            <dt>{f.label}</dt>
            <dd title={f.value}>{f.value}</dd>
          </div>
        ))}
      </dl>
      {task.loading ? <p className="pj-note">Reading the plugin's details…</p> : null}
      {n.failureTitle || n.failureDetail ? (
        <Block title="Failure">
          {n.failureTitle ? <strong>{n.failureTitle}</strong> : null}
          {n.failureDetail ? <p className="sel">{n.failureDetail}</p> : null}
          {n.failureNext ? <p className="mute">{n.failureNext}</p> : null}
        </Block>
      ) : null}
      {n.plan ? <Block title="Plan"><p className="sel pre">{n.plan}</p></Block> : null}
      {n.limitStopped ? <Block title="Stopped at a limit"><p className="sel">{n.limitStopped}</p></Block> : null}
      {n.handovers.length ? <Block title="Handovers">{n.handovers.map((h) => <p key={h} className="sel">{h}</p>)}</Block> : null}
      {n.attempts.length ? (
        <Block title="Attempts">
          {n.attempts.map((a) => (
            <p key={a.letter}>{a.letter} · {a.agent || "agent"} · {a.standing}{a.stat ? ` · ${a.stat}` : ""}</p>
          ))}
        </Block>
      ) : null}
      {task.reportLink ? (
        <Block title="Report">
          <p className="mute sel">{task.reportLink}</p>
          <button type="button" className="pj-btn small" onClick={() => run(() => projects.openReport(task.reportLink))}>Open Report</button>
        </Block>
      ) : null}
    </>
  );
}

function ContextTab({ task }: { task: TaskPane }) {
  const c = task.context;
  const [editing, setEditing] = useState(false);
  return (
    <>
      <Block title="Description">
        <p className="sel pre">{c.description || "No description."}</p>
        {c.canEdit ? (
          <div className="end"><button type="button" className="pj-btn small" aria-label="Edit description" onClick={() => setEditing(true)}>Edit…</button></div>
        ) : null}
      </Block>
      <Block title="Library files">
        {c.items.length === 0 ? (
          <p className="mute">Nothing in this project's library. Add files from the Project page.</p>
        ) : (
          <>
            <p className="mute">A task reads the files attached when it starts. To give a running agent one more, send it.</p>
            {c.items.map((i) => (
              <div key={i.id} className="pj-lrow">
                <span className="nm" title={i.name}>{i.name}</span>
                <span className="sz">{i.inBrief ? "in the description" : MODE_LABEL[i.mode] ?? i.mode}</span>
                {c.tellable && !i.inBrief ? (
                  <button type="button" className="pj-btn small" aria-label={`Send ${i.name} to the agent`} onClick={() => run(() => projects.sendLibraryItem(i.id))}>
                    Send
                  </button>
                ) : null}
              </div>
            ))}
          </>
        )}
      </Block>
      {editing ? <EditDescription id={task.id} text={c.full} tellable={c.tellable} onClose={() => setEditing(false)} /> : null}
    </>
  );
}

function ChangesTab({ task }: { task: TaskPane }) {
  const ch = task.changes;
  return (
    <Block title="Uncommitted in the worktree">
      {!ch.loaded ? <p className="mute">{ch.loading ? "Reading…" : "Could not read the worktree."}</p> : null}
      {ch.loaded && ch.files.length === 0 ? <p className="mute">Nothing uncommitted. What the agent finished is committed; open the pull request to review it.</p> : null}
      {ch.loaded && ch.files.length > 0 ? (
        <>
          <p className="mute">{ch.files.length + ch.more} files · +{ch.additions} −{ch.deletions}</p>
          {ch.files.map((f) => (
            <div key={f.path} className="pj-lrow">
              <span className="nm" title={f.path}>{f.path}</span>
              <span className="sz">+{f.additions} −{f.deletions}</span>
            </div>
          ))}
          {ch.more > 0 ? <p className="mute">…and {ch.more} more</p> : null}
        </>
      ) : null}
      <div className="end"><button type="button" className="pj-btn small" onClick={() => run(() => projects.loadTab("changes-refresh"))}>Refresh</button></div>
    </Block>
  );
}

function ReviewTab({ task }: { task: TaskPane }) {
  const r = task.review;
  if (!r) return <p className="pj-empty">No review yet.</p>;
  return (
    <Block title="Review">
      <strong>{r.verdict}</strong>
      {r.text ? <p className="sel pre">{r.text}</p> : null}
      {r.findings.map((f, i) => (
        <div key={i} className="pj-find-row">
          <span className="sev">{f.severity}</span>
          <div>
            <p className="sel">{f.subject}</p>
            {f.where ? <p className="mute sel">{f.where}</p> : null}
            {f.proof ? <p className="mute sel">{f.proof}</p> : null}
          </div>
        </div>
      ))}
      {r.rounds.map((line) => <p key={line} className="mute">{line}</p>)}
    </Block>
  );
}

function SettingsTab({ task }: { task: TaskPane }) {
  const s = task.settings;
  if (!s.loaded) return <p className="pj-empty">Reading…</p>;
  if (s.rows.length === 0) return <p className="pj-empty">Everything is at the plugin's defaults. Change them in Settings → Work Board, or with Delegate with….</p>;
  return (
    <dl className="pj-facts">
      {s.rows.map((r) => (
        <div key={r.name}>
          <dt title={r.from}>{r.name}</dt>
          <dd title={r.from}>{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}
