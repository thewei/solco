/**
 * [INPUT]: Solco Assistant CLI in ../bin, OMP extension API and a Coordinator's private cwd.
 * [OUTPUT]: solco_assistant (scoped task/goal/routine/memory broker) and a tool_call deny-by-default gate.
 * [POS]: OMP Coordinator launch-only policy; worker Agents never load this extension.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { spawnSync } from "node:child_process";
import { resolve, dirname, basename } from "node:path";
import { fileURLToPath } from "node:url";

// OMP loads extensions through its own loader, where `import.meta.dir` is not
// always set (a real Herdr pane showed it undefined while Bun's own test runner
// had it). Derive the folder from whichever form the loader gives.
export function extensionDir(meta: { dir?: string; dirname?: string; url?: string } = import.meta): string {
  if (meta.dir) return meta.dir;
  if (meta.dirname) return meta.dirname;
  if (meta.url) return dirname(fileURLToPath(meta.url));
  throw new Error("Cannot locate the Solco plugin folder.");
}

export const allowedTools = new Set(["solco_assistant", "read", "grep", "glob"]);

// Cwd is fixed by the Herdr workspace Solco provisions for this Coordinator.
// Never accept an assistantId invented by a tool call or inferred from chat.
export function scopedAssistant(cwd: string): string {
  const full = resolve(cwd);
  const id = basename(full);
  if (basename(dirname(full)) !== "coordinators" || !/^a_[a-zA-Z0-9_-]{2,120}$/.test(id)) {
    throw new Error("This is not a Solco Coordinator workspace; product mutations are blocked.");
  }
  return id;
}

export const actions = [
  "task.create", "task.list", "task.show", "task.graph", "task.why", "task.retry",
  "goal.create", "goal.list", "goal.show", "goal.plan", "goal.set-plan", "goal.pause", "goal.resume",
  "routine.draft", "routine.list", "routine.show",
  "trigger.draft", "trigger.list",
  "memory.propose", "memory.list",
  "checkpoint.get", "checkpoint.set",
  "recovery.show", "trace.show",
] as const;

// Actions that change state need a stable requestId (a retry replays, never repeats).
const mutating = new Set(["task.create", "task.retry", "goal.create", "goal.set-plan", "goal.pause", "goal.resume", "routine.draft", "trigger.draft", "memory.propose", "checkpoint.set"]);

// The extension only shapes the call. Who the caller is, what it may touch and
// whether a record is its own are decided by the Solco core (`assistant --as`),
// so nothing here is the security boundary.
export function route(action: string, assistantId: string, target = "", json = ""): { args: string[]; input: string } {
  if (!(actions as readonly string[]).includes(action)) throw new Error("Unsupported Solco Assistant action.");
  const [entity, operation] = action.split(".");
  const payload = json.trim() ? JSON.parse(json) : {};
  if (payload === null || Array.isArray(payload) || typeof payload !== "object") {
    throw new Error("Payload must be a JSON object.");
  }
  const as = ["assistant", "--as", assistantId];
  if (action === "checkpoint.get" || action === "recovery.show") {
    return { args: [...as, entity, operation === "show" ? "show" : "get", assistantId], input: "" };
  }
  if (action === "task.create" || action === "goal.create" || action === "routine.draft" || action === "trigger.draft" || action === "memory.propose" || action === "checkpoint.set") {
    payload.assistantId = assistantId;
    if (action === "task.create") {
      payload.agent = "omp";
      if (payload.kind === "coding") payload.draft = true;
    }
    if (action === "routine.draft") {
      payload.agent = "omp";
      payload.headless = true;
    }
  }
  if (operation === "list" || operation === "graph") {
    const scoped = operation === "graph" || entity === "memory" ? [assistantId] : ["--assistant", assistantId];
    return { args: [...as, entity, operation, ...scoped], input: "" };
  }
  const creates = operation === "create" || operation === "draft" || operation === "propose" || action === "checkpoint.set";
  if (!creates) {
    if (!/^[A-Za-z0-9_-]{2,120}$/.test(target)) throw new Error("A valid Solco record ID is required.");
  }
  if (mutating.has(action) && (typeof payload.requestId !== "string" || !/^[A-Za-z0-9_-]{8,120}$/.test(payload.requestId))) {
    throw new Error("Mutations need a stable unique requestId (8–120 characters; reuse it on retries).");
  }
  if (!creates) return { args: [...as, entity, operation, target], input: JSON.stringify(payload) };
  return { args: [...as, entity, operation], input: JSON.stringify(payload) };
}

export default function (pi: ExtensionAPI) {
  const z = pi.zod;

  // The Coordinator may inspect files and use Solco's broker, but may not
  // launch shell commands, external schedulers, generic subagents or workers.
  // This is an OMP tool gate, NOT an OS sandbox or a protection against
  // a privileged malicious extension loaded outside this policy.
  pi.on("tool_call", event => {
    if (!allowedTools.has(event.toolName)) {
      return { block: true, reason: "This Solco Assistant delegates work only with the solco_assistant tool. Do not run shell commands, system schedulers or subagents here." };
    }
  });

  pi.registerTool({
    name: "solco_assistant",
    label: "Solco Assistant",
    description: "Manage only this Assistant's durable Solco tasks, goal plans, routine and event-trigger drafts and its own checkpoint (read it first and save it last on every autonomous wake turn). task.why explains why a task is not moving; goal.pause / goal.resume and task.retry act on one named record; recovery.show restates the recorded state after a restart or context compaction (read it before acting, instead of trusting memory); trace.show follows a task or goal id back to its goal, milestone, runs and records. task.create refuses an unfinished duplicate (pass allowDuplicate true only when you truly need another). A routine or trigger draft (trigger.draft: on task.failed/blocked/uncertain/completed + a taskId or titleContains filter, then the one task to start) creates an approval card and NEVER runs before its owner approves. Delegate browsing and research to workers through task.create. No system cron, launchd or async subagents.",
    parameters: z.object({
      action: z.enum(actions).describe("Solco operation."),
      target: z.string().optional().describe("A goal/task/routine ID for show, why, retry, plan, set-plan, pause or resume."),
      payload: z.string().optional().describe("JSON object for mutations, with a stable requestId, task brief, schedule or goal plan. assistantId and agent are always taken from the owning Assistant."),
    }),
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      try {
        const assistantId = scopedAssistant(ctx.cwd);
        const { args, input } = route(params.action, assistantId, params.target ?? "", params.payload ?? "");
        const cli = resolve(extensionDir(), "../bin/solco-automations");
        const result = spawnSync(cli, args, { encoding: "utf8", input, timeout: 15000, maxBuffer: 2 << 20 });
        if (result.error) throw result.error;
        const output = result.stdout.trim();
        return {
          content: [{ type: "text", text: output || result.stderr.trim() || "No response from Solco." }],
          isError: result.status !== 0,
        };
      } catch (err) {
        return { content: [{ type: "text", text: err instanceof Error ? err.message : String(err) }], isError: true };
      }
    },
  });
}
