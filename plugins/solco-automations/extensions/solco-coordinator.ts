/**
 * [INPUT]: Solco Assistant CLI in ../bin, OMP extension API and a Coordinator's private cwd.
 * [OUTPUT]: solco_assistant (scoped task/goal/routine/memory broker) and a tool_call deny-by-default gate.
 * [POS]: OMP Coordinator launch-only policy; worker Agents never load this extension.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { spawnSync } from "node:child_process";
import { resolve, dirname, basename } from "node:path";

export const allowedTools = new Set(["solco_assistant", "read", "ls", "grep", "find"]);

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
  "task.create", "task.list", "task.show", "task.graph",
  "goal.create", "goal.list", "goal.show", "goal.plan", "goal.set-plan",
  "routine.draft", "routine.list", "routine.show",
  "memory.propose", "memory.list",
] as const;

export function route(action: string, assistantId: string, target = "", json = ""): { args: string[]; input: string } {
  if (!(actions as readonly string[]).includes(action)) throw new Error("Unsupported Solco Assistant action.");
  const [entity, operation] = action.split(".");
  const payload = json.trim() ? JSON.parse(json) : {};
  if (payload === null || Array.isArray(payload) || typeof payload !== "object") {
    throw new Error("Payload must be a JSON object.");
  }
  if (action === "task.create" || action === "goal.create" || action === "routine.draft" || action === "memory.propose") {
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
    return { args: ["assistant", entity, operation, ...scoped], input: "" };
  }
  if (operation !== "create" && operation !== "draft" && operation !== "propose") {
    if (!/^[A-Za-z0-9_-]{2,120}$/.test(target)) throw new Error("A valid Solco record ID is required.");
    return { args: ["assistant", entity, operation, target], input: JSON.stringify(payload) };
  }
  if (typeof payload.requestId !== "string" || !/^[A-Za-z0-9_-]{8,120}$/.test(payload.requestId)) {
    throw new Error("Mutations need a stable unique requestId (8–120 characters; reuse it on retries).");
  }
  return { args: ["assistant", entity, operation], input: JSON.stringify(payload) };
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
    description: "Manage only this Assistant's durable Solco tasks, goal plans and routine drafts. A routine draft creates an approval card and NEVER schedules before its owner approves. Delegate browsing and research to workers through task.create. No system cron, launchd or async subagents.",
    parameters: z.object({
      action: z.enum(actions).describe("Solco operation."),
      target: z.string().optional().describe("A goal/task/routine ID for show, plan, or set-plan."),
      payload: z.string().optional().describe("JSON object for mutations, with a stable requestId, task brief, schedule or goal plan. assistantId and agent are always taken from the owning Assistant."),
    }),
    async execute(_call, params, _onUpdate, ctx) {
      try {
        const assistantId = scopedAssistant(ctx.cwd);
        const { args, input } = route(params.action, assistantId, params.target ?? "", params.payload ?? "");
        const cli = resolve(import.meta.dir, "../bin/solco-automations");
        // ID-based actions can otherwise inspect or alter another Assistant's
        // Goal/Task. Validate its owner through the read-only CLI before use.
        if (params.target && (params.action.startsWith("goal.") || params.action === "task.show")) {
          const type = params.action.split(".")[0];
          const check = spawnSync(cli, ["assistant", type, "show", params.target], { encoding: "utf8", timeout: 10000, maxBuffer: 1 << 20 });
          if (check.status !== 0 || JSON.parse(check.stdout)[type]?.assistantId !== assistantId) {
            throw new Error("This record is not owned by the current Assistant.");
          }
        }
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
