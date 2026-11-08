/**
 * [INPUT]: Coordinator OMP broker routing and event gate.
 * [OUTPUT]: Regression tests for product-only scheduling and scoped, OMP-only delegates.
 * [POS]: Tests that do not need a model, Herdr daemon, or user scheduling state.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import { test, expect } from "bun:test";
import extension, { route, scopedAssistant, allowedTools, extensionDir } from "./solco-coordinator";

const owner = "a_1234567890abcdef";
const req = "stable-request-123";

test("only the Herdr-provisioned Coordinator directory supplies the Assistant ID", () => {
  expect(scopedAssistant("/isolated/assistants-work/personal/coordinators/" + owner)).toBe(owner);
  expect(() => scopedAssistant("/tmp/research/" + owner)).toThrow();
  expect(() => scopedAssistant("/tmp/coordinators/a_invalid?")).toThrow();
});

test("all repeat requests remain Solco draft routines under their owner and OMP", () => {
  const x = route("routine.draft", owner, "", JSON.stringify({
    requestId: req, assistantId: "a_other", agent: "claude", headless: false,
    name: "x-digest", cron: "0 9 * * *", prompt: "Use ego-browser to summarise X.com", rationale: "Requested",
  }));
  expect(x.args).toEqual(["assistant", "--as", owner, "routine", "draft"]);
  const body = JSON.parse(x.input);
  expect(body.assistantId).toBe(owner);
  expect(body.agent).toBe("omp");
  expect(body.headless).toBe(true);
  expect(body.prompt).toContain("ego-browser");
  expect(body.repo).toBeUndefined();
});

test("worker creation never self-starts coding or another agent kind", () => {
  const x = route("task.create", owner, "", JSON.stringify({
    requestId: req, kind: "coding", title: "Build", brief: "Implement", draft: false,
    assistantId: "a_other", agent: "claude",
  }));
  const body = JSON.parse(x.input);
  expect(body).toMatchObject({ assistantId: owner, agent: "omp", draft: true });
  expect(route("task.graph", owner)).toEqual({ args: ["assistant", "--as", owner, "task", "graph", owner], input: "" });
  expect(route("task.list", owner)).toEqual({ args: ["assistant", "--as", owner, "task", "list", "--assistant", owner], input: "" });
});

test("unapproved mutation requires stable idempotency receipt", () => {
  expect(() => route("routine.draft", owner, "", '{"name":"x"}')).toThrow(/requestId/);
  expect(() => route("task.create", owner, "", "[]")).toThrow(/object/);
  expect(() => route("system.launchd", owner)).toThrow(/Unsupported/);
  expect(() => route("goal.set-plan", owner, "../another", "{}")).toThrow(/record ID/);
});

test("memory is two files: read both, add one line, rewrite only its own", () => {
  expect(route("memory.read", owner)).toEqual({ args: ["assistant", "--as", owner, "memory", "read", owner], input: "" });
  // The target is always the caller: a payload cannot name another Assistant's memory.
  const add = route("memory.add", owner, "a_other", JSON.stringify({ note: "Prefers tea", assistantId: "a_other" }));
  expect(add.args).toEqual(["assistant", "--as", owner, "memory", "add", owner]);
  expect(JSON.parse(add.input)).toEqual({ note: "Prefers tea" });
  const write = route("memory.write", owner, "", JSON.stringify({ text: "- tidy", expectedRev: "abc", rev: "x" }));
  expect(write.args).toEqual(["assistant", "--as", owner, "memory", "write", owner]);
  expect(JSON.parse(write.input)).toEqual({ text: "- tidy", expectedRev: "abc" });
  // USER.md and the old approval flow are not on the Coordinator's menu at all.
  for (const gone of ["memory.write-user", "memory.propose", "memory.approve", "memory.list"]) {
    expect(() => route(gone, owner)).toThrow(/Unsupported/);
  }
});

test("manage.* is shaped for the core, which alone decides who may use it", () => {
  expect(route("manage.list", owner)).toEqual({ args: ["assistant", "--as", owner, "manage", "list"], input: "" });
  const create = route("manage.create", owner, "", JSON.stringify({ requestId: req, name: "Researcher", description: "Reads papers" }));
  expect(create.args).toEqual(["assistant", "--as", owner, "manage", "create"]);
  expect(JSON.parse(create.input)).toMatchObject({ name: "Researcher" });
  const archive = route("manage.archive", owner, "a_abcdef1234", JSON.stringify({ requestId: req }));
  expect(JSON.parse(archive.input)).toEqual({ requestId: req, id: "a_abcdef1234" });
  const delegate = route("manage.delegate", owner, "a_abcdef1234", JSON.stringify({ requestId: req, kind: "research", title: "t", brief: "b" }));
  expect(JSON.parse(delegate.input)).toMatchObject({ id: "a_abcdef1234", kind: "research" });
  expect(route("manage.tasks", owner, "a_abcdef1234").args).toEqual(["assistant", "--as", owner, "manage", "tasks"]);
  const group = route("manage.group", owner, "", JSON.stringify({ requestId: req, title: "Trip", members: ["a_abcdef1234", "a_abcdef5678"] }));
  expect(group.args).toEqual(["assistant", "--as", owner, "manage", "group"]);
  expect(() => route("manage.group", owner, "", JSON.stringify({ title: "Trip" }))).toThrow(/requestId/);
  expect(() => route("manage.archive", owner, "", JSON.stringify({ requestId: req }))).toThrow(/Assistant ID/);
  expect(() => route("manage.archive", owner, "../x", JSON.stringify({ requestId: req }))).toThrow(/Assistant ID/);
  expect(() => route("manage.rename", owner, "a_abcdef1234", JSON.stringify({ name: "x" }))).toThrow(/requestId/);
  expect(() => route("manage.delete", owner, "a_abcdef1234")).toThrow(/Unsupported/);
});

test("coordinator cannot use tools to launch a system timer or an untracked worker", () => {
  const block: Record<string, (event: { toolName: string }) => unknown> = {};
  let broker: { name: string } | undefined;
  const atom = { optional() { return this; }, describe() { return this; } };
  const pi = {
    zod: { string: () => atom, enum: () => atom, object: () => atom },
    on(name: string, handler: (event: { toolName: string }) => unknown) { block[name] = handler; },
    registerTool(tool: { name: string }) { broker = tool; },
  };
  extension(pi as never);
  expect(broker?.name).toBe("solco_assistant");
  for (const name of ["bash", "python", "write", "subagent", "spawn_agent", "functions.exec", "exec", "schedule"]) {
    expect(allowedTools.has(name)).toBe(false);
    expect(block.tool_call({ toolName: name })).toMatchObject({ block: true });
  }
  for (const name of ["solco_assistant", "read", "grep"]) {
    expect(block.tool_call({ toolName: name })).toBeUndefined();
  }
});

test("extension folder survives loaders that lack import.meta.dir", () => {
  expect(extensionDir({ dir: "/a/ext" })).toBe("/a/ext");
  expect(extensionDir({ dirname: "/b/ext" })).toBe("/b/ext");
  expect(extensionDir({ url: "file:///c/ext/solco-coordinator.ts" })).toBe("/c/ext");
  expect(() => extensionDir({})).toThrow();
});

test("tool reads its working directory from OMP's fifth execute argument", async () => {
  // OMP calls execute(toolCallId, params, signal, onUpdate, ctx). A real Herdr
  // pane showed the earlier 4-argument shape leaving ctx undefined.
  let tool: any;
  extension({ zod: { object: (x: unknown) => x, enum: () => ({ describe: () => ({}) }), string: () => ({ optional: () => ({ describe: () => ({}) }), describe: () => ({}) }) }, on() {}, registerTool: (t: unknown) => { tool = t; } } as any);
  const res = await tool.execute("call_1", { action: "task.list" }, undefined, undefined, { cwd: "/tmp/not-a-coordinator" });
  expect(res.isError).toBe(true);
  expect(res.content[0].text).toContain("not a Solco Coordinator workspace");
});

test("checkpoint actions are scoped to the owning Assistant", () => {
  const g = route("checkpoint.get", "a_owner", "a_someone_else");
  expect(g.args).toEqual(["assistant", "--as", "a_owner", "checkpoint", "get", "a_owner"]);
  const set = route("checkpoint.set", "a_owner", "", JSON.stringify({ requestId: "cp-12345678", assistantId: "a_other", progress: "p" }));
  expect(set.args).toEqual(["assistant", "--as", "a_owner", "checkpoint", "set"]);
  expect(JSON.parse(set.input).assistantId).toBe("a_owner");
  expect(() => route("checkpoint.set", "a_owner", "", JSON.stringify({ progress: "p" }))).toThrow(/requestId/);
});

test("pause, resume, retry and why are single-record actions the core scopes", () => {
  const pause = route("goal.pause", owner, "g_abc123", JSON.stringify({ requestId: req }));
  expect(pause.args).toEqual(["assistant", "--as", owner, "goal", "pause", "g_abc123"]);
  expect(JSON.parse(pause.input).requestId).toBe(req);
  expect(() => route("goal.resume", owner, "g_abc123", "{}")).toThrow(/requestId/);
  expect(() => route("task.retry", owner, "", JSON.stringify({ requestId: req }))).toThrow(/record ID/);
  expect(route("task.why", owner, "t_abc123").args).toEqual(["assistant", "--as", owner, "task", "why", "t_abc123"]);
  expect(route("recovery.show", owner).args).toEqual(["assistant", "--as", owner, "recovery", "show", owner]);
  // A person's decisions are not actions at all.
  for (const a of ["goal.approve", "goal.complete", "routine.resume", "task.publish", "approval.decide", "coordinator.stop"]) {
    expect(() => route(a, owner, "x_1234", "{}")).toThrow(/Unsupported/);
  }
});

test("trace.show needs a record id and a duplicate override travels in the payload", () => {
  expect(route("trace.show", owner, "t_abc123").args).toEqual(["assistant", "--as", owner, "trace", "show", "t_abc123"]);
  expect(() => route("trace.show", owner)).toThrow(/record ID/);
  const x = route("task.create", owner, "", JSON.stringify({ requestId: req, title: "T", brief: "B", kind: "research", allowDuplicate: true }));
  expect(JSON.parse(x.input).allowDuplicate).toBe(true);
});
