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
  expect(x.args).toEqual(["assistant", "routine", "draft"]);
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
  expect(route("task.graph", owner)).toEqual({ args: ["assistant", "task", "graph", owner], input: "" });
  expect(route("task.list", owner)).toEqual({ args: ["assistant", "task", "list", "--assistant", owner], input: "" });
});

test("unapproved mutation requires stable idempotency receipt", () => {
  expect(() => route("routine.draft", owner, "", '{"name":"x"}')).toThrow(/requestId/);
  expect(() => route("task.create", owner, "", "[]")).toThrow(/object/);
  expect(() => route("system.launchd", owner)).toThrow(/Unsupported/);
  expect(() => route("goal.set-plan", owner, "../another", "{}")).toThrow(/record ID/);
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
  expect(g.args).toEqual(["assistant", "checkpoint", "get", "a_owner"]);
  const set = route("checkpoint.set", "a_owner", "", JSON.stringify({ requestId: "cp-12345678", assistantId: "a_other", progress: "p" }));
  expect(set.args).toEqual(["assistant", "checkpoint", "set"]);
  expect(JSON.parse(set.input).assistantId).toBe("a_owner");
  expect(() => route("checkpoint.set", "a_owner", "", JSON.stringify({ progress: "p" }))).toThrow(/requestId/);
});
