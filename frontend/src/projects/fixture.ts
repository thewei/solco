import type { ProjectPane, ProjectsView, TaskPane, TaskRow } from "../mygo";

/**
 * Dev-only sample data (`?mock&scene=home|library|reports|auto|task|plan|ready|ask|none|compose`)
 * so the page can be reviewed in a plain browser. Never reached in a built app.
 */
const row = (id: string, title: string, group: string, tone: string, msg: string, extra: Partial<TaskRow> = {}): TaskRow => ({
  id, title, msg, group, tone, app: "claude", pr: "", round: 0, pinned: false, research: false, chat: false, stage: "", ...extra,
});

const project = (): ProjectPane => ({
  name: "solco", other: false, pinned: true, open: 5, doneCount: 1, autopilot: false, working: 1,
  coordinator: { state: "working", agent: "claude", running: true, said: "", queued: 0, error: "" },
  groups: [
    { key: "needs", label: "Needs you", tone: "attention", tasks: [
      row("SLC-14", "Fix login redirect loop", "needs", "attention", "Question for you"),
      row("SLC-11", "Export design tokens", "needs", "attention", "Plan to approve", { app: "codex" }),
    ] },
    { key: "ready", label: "Ready to approve", tone: "ready", tasks: [
      row("SLC-12", "Task chat header", "ready", "ready", "Ready · R#2 pass · 1 minor", { round: 2, pr: "60" }),
    ] },
    { key: "working", label: "Working", tone: "working", tasks: [
      row("SLC-13", "Import dropped files into the Library", "working", "working", "Wiring the drop zone · 62% · 6m", { app: "codex" }),
    ] },
    { key: "queued", label: "Queued", tone: "idle", tasks: [
      row("SLC-16", "Sync Library index after merge", "todo", "idle", "after SLC-12", { stage: "after" }),
    ] },
    { key: "todo", label: "To do", tone: "idle", tasks: [row("SLC-17", "Write onboarding notes", "todo", "idle", "")] },
  ],
  done: [row("SLC-9", "Fix stale Sessions row", "done", "idle", "Merged · PR#57", { pr: "57" })],
  library: {
    available: true, note: "Delivered to 3 places", block: "", alwaysUsed: 1100, alwaysCap: 4096, totalSize: 3_400_000, totalText: "3.4 MB",
    items: [
      { id: "a", name: "House rules.md", mode: "always", kind: "text", size: 1100, sizeText: "1 KB" },
      { id: "b", name: "PRD - Projects.md", mode: "reference", kind: "text", size: 8600, sizeText: "8 KB" },
      { id: "c", name: "login-flow.png", mode: "reference", kind: "image", size: 412_000, sizeText: "402 KB" },
      { id: "d", name: "api-contract.pdf", mode: "reference", kind: "file", size: 1_200_000, sizeText: "1.1 MB" },
      { id: "e", name: "brand-tokens.json", mode: "task", kind: "text", size: 6100, sizeText: "5 KB" },
    ],
  },
  reports: { loading: false, rows: [
    { taskId: "SLC-9", title: "Fix stale Sessions row", link: "http://127.0.0.1:8123/SLC-9", local: false },
    { taskId: "SLC-8", title: "Audit the Library format", link: "/tmp/report.md", local: true },
  ] },
  automations: [
    { id: "a1", name: "nightly check", caption: "daily 03:00 · next in 5h", on: true },
    { id: "a2", name: "weekly sweep", caption: "off: its project is archived · research only", on: false },
  ],
});

const task = (over: Partial<TaskPane> = {}): TaskPane => ({
  ...row("SLC-13", "Import dropped files into the Library", "working", "working", "Wiring the drop zone · 62% · 6m", { app: "codex" }),
  project: "solco", groupLabel: "Working", pinned: false, loading: false, hasAgent: true, canOpenProject: true,
  worktree: "/wt/slc-13", reportLink: "",
  dockTitle: "Working", dockNote: "",
  strip: [
    { label: "Work", state: "current" }, { label: "Check", state: "pending" }, { label: "PR", state: "pending" },
    { label: "Review", state: "pending" }, { label: "Approve", state: "pending" }, { label: "Merge", state: "pending" },
  ],
  actions: [
    { kind: "stop", label: "Stop", primary: false, dock: false },
    { kind: "archive", label: "Archive", primary: false, dock: false },
  ],
  facts: [
    { label: "Status", value: "Working" }, { label: "Agent", value: "codex · gpt-5 · high" },
    { label: "Branch", value: "work/13-import-dropped-files" }, { label: "Time limit", value: "1h12m of 2h" },
  ],
  narrative: {
    description: "Import dropped files into the Library. Context: login-flow.png (opened 2x), House rules, api-contract.pdf",
    failureTitle: "", failureDetail: "", failureNext: "", plan: "", limitStopped: "", handovers: [], attempts: [],
  },
  changes: { loaded: true, loading: false, additions: 38, deletions: 6, more: 0, files: [
    { path: "internal/library/drop.go", additions: 31, deletions: 4 }, { path: "internal/library/drop_test.go", additions: 7, deletions: 2 },
  ] },
  settings: { loaded: true, rows: [{ name: "worker.agent", value: "codex", from: "your entry for solco" }] },
  context: {
    description: "Import dropped files into the Library.", full: "Import dropped files into the Library.", canEdit: true, tellable: true, live: true,
    items: [{ id: "a", name: "House rules.md", mode: "always", inBrief: false }, { id: "c", name: "login-flow.png", mode: "reference", inBrief: true }],
  },
  ...over,
});

export function mockView(search: string): ProjectsView {
  const scene = new URLSearchParams(search).get("scene") ?? "home";
  const base: ProjectsView = {
    seq: 1, theme: new URLSearchParams(search).get("theme") ?? "dark", mode: "project", available: true,
    project: project(), chat: { target: "p1", label: "solco coordinator", why: "", waiting: false },
    counts: { needsYou: 2, ready: 1, working: 1 },
  };
  switch (scene) {
    case "task":
      return { ...base, mode: "task", task: task(), chat: { target: "p2", label: "SLC-13's agent", why: "", waiting: false } };
    case "plan":
      return { ...base, mode: "task", chat: { target: "p2", label: "SLC-11's agent", why: "", waiting: false }, task: task({
        id: "SLC-11", title: "Export design tokens", group: "needs", tone: "attention", groupLabel: "Needs you", app: "codex", hasAgent: true,
        dockTitle: "Plan ready", dockNote: "Read the plan under Run, then approve it or send it back with words.",
        actions: [
          { kind: "approve-plan", label: "Approve Plan…", primary: true, dock: true },
          { kind: "plan-back", label: "Send Plan Back…", primary: false, dock: true },
          { kind: "stop", label: "Stop", primary: false, dock: false },
        ],
        strip: [
          { label: "Work", state: "waiting" }, { label: "Check", state: "pending" }, { label: "PR", state: "pending" },
          { label: "Review", state: "pending" }, { label: "Approve", state: "pending" }, { label: "Merge", state: "pending" },
        ],
        narrative: { description: "Export tokens from Figma", failureTitle: "", failureDetail: "", failureNext: "", limitStopped: "", handovers: [], attempts: [],
          plan: "1. Read the Figma export\n2. Map tokens to theme.css\n3. Generate the Go constants\n4. Tests" },
      }) };
    case "ready":
      return { ...base, mode: "task", chat: { target: "p3", label: "SLC-12's agent", why: "", waiting: false }, task: task({
        id: "SLC-12", title: "Task chat header", group: "ready", tone: "ready", groupLabel: "Ready to approve", pr: "60", round: 2,
        dockTitle: "Ready to approve", dockNote: "Ready · R#2 pass · 1 minor",
        actions: [
          { kind: "approve", label: "Approve…", primary: true, dock: true },
          { kind: "archive", label: "Archive", primary: false, dock: false },
        ],
        strip: [
          { label: "Work", state: "done" }, { label: "Check", state: "done" }, { label: "PR", state: "done" },
          { label: "Review", state: "done" }, { label: "Approve", state: "waiting" }, { label: "Merge", state: "pending" },
        ],
        review: { verdict: "Passed · 1 finding · codex", failed: false, by: "codex", text: "Looks right overall.", rounds: ["Round 1 · Did not pass · 2 findings"],
          findings: [{ severity: "MINOR", subject: "Header truncates long titles", where: "header.tsx:44", proof: "Title clipped at 28 chars" }] },
      }) };
    case "ask":
      return { ...base, mode: "task", chat: { target: "p4", label: "SLC-14's agent", why: "", waiting: false }, task: task({
        id: "SLC-14", title: "Fix login redirect loop", group: "needs", tone: "attention", groupLabel: "Needs you", msg: "Question for you",
        dockTitle: "Needs you", dockNote: "Question for you",
        narrative: { description: "Fix the login redirect loop", failureTitle: "Pull request checks failed", failureDetail: "lint: 2 errors", failureNext: "Retry after fixing lint",
          plan: "", limitStopped: "", handovers: [], attempts: [] },
        actions: [{ kind: "retry", label: "Retry", primary: false, dock: false }, { kind: "fix", label: "Send failure details", primary: false, dock: false }, { kind: "archive", label: "Archive", primary: false, dock: false }],
      }) };
    case "none":
      return { ...base, chat: { target: "", label: "solco coordinator", why: "The coordinator isn't running. Start it to plan and delegate by talking.", waiting: false },
        project: { ...project(), coordinator: { state: "none", agent: "", running: false, said: "", queued: 0, error: "" } } };
    case "todo":
      return { ...base, mode: "task", chat: { target: "", label: "SLC-17", why: "Nobody is on this task yet. Delegate it to start an agent.", waiting: false }, task: task({
        id: "SLC-17", title: "Write onboarding notes", group: "todo", tone: "idle", groupLabel: "To do", hasAgent: false,
        actions: [{ kind: "delegate", label: "Delegate", primary: false, dock: false }, { kind: "delegate-with", label: "Delegate with…", primary: false, dock: false }, { kind: "archive", label: "Archive", primary: false, dock: false }],
      }) };
    case "compose":
      return { ...base, compose: { project: "solco", delegate: false, canAttach: true, items: project().library.items } };
    default:
      return base;
  }
}
