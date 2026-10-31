import React, { memo, useContext, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkGfm from "remark-gfm";
import type { ChatChange, ChatStep, ChatTurn } from "./mygo";
import { Icon, type IconName } from "./icons";
import { WorkingLine } from "./dock";
import { SubagentBlock, SubagentContext } from "./subagents";

/**
 * One transcript row, shaped by designs/chat-view (A+B, chosen 2026-10-30):
 * the user speaks in a neutral bubble; the assistant is plain reading text
 * with no container; process (thinking + tool calls) folds into one summary
 * line per run that opens into typed ledger rows; a settled turn that edited
 * files ends in a changes card. Presentation only: every fact comes from the
 * bridge projection.
 */

type Turn = ChatTurn;
type Step = ChatStep;

export interface TurnProps {
  turn: Turn;
  /** The preceding user row's timestamp: the first run's duration boundary. */
  startMs?: number;
  /** Local repository root, to show tool paths repo-relative. */
  root?: string;
  canReview: boolean;
  onReview: (path: string) => void;
  isLast: boolean;
}

export function TurnRow(props: TurnProps) {
  const { turn } = props;
  if (turn.user) {
    const shown = userDisplay(turn.text ?? "");
    return (
      <div className="turn user">
        <div className={shown.output ? "bubble out" : "bubble"}>{mentions(shown.text)}</div>
        <Foot turn={turn} source={shown.text} />
      </div>
    );
  }
  return <AssistantRow {...props} />;
}

/** A prompt on its way to the agent (or one that did not arrive). */
export interface Outbox {
  text: string;
  status: "sending" | "failed" | "uncertain";
  reason?: string;
}

export function OutboxRow({
  outbox,
  onRetry,
  onEdit,
  onDiscard,
}: {
  outbox: Outbox;
  onRetry: () => void;
  onEdit: () => void;
  onDiscard: () => void;
}) {
  const shown = userDisplay(outbox.text);
  const failed = outbox.status !== "sending";
  return (
    <div className={`turn user outbox ${outbox.status}`}>
      <div className="bubble">{mentions(shown.text)}</div>
      <div className="delivery" role="status" aria-live="polite">
        {outbox.status === "sending" ? (
          <>
            <span className="spin" aria-hidden /> Sending…
          </>
        ) : (
          <>
            <Icon name="warn" size={13} />
            <span>{outbox.status === "uncertain" ? "Delivery uncertain — check the agent before resending" : "Not delivered"}</span>
            {outbox.status === "failed" ? (
              <button type="button" onClick={onRetry}>
                Retry
              </button>
            ) : null}
            <button type="button" onClick={onEdit}>
              Edit
            </button>
            <button type="button" onClick={onDiscard}>
              Discard
            </button>
          </>
        )}
      </div>
      {failed && outbox.reason && outbox.status === "failed" ? <div className="delivery-why">{outbox.reason}</div> : null}
    </div>
  );
}

function AssistantRow({ turn, startMs, root, canReview, onReview, isLast }: TurnProps) {
  const steps = effectiveSteps(turn);
  const { byId } = useContext(SubagentContext);
  const segs = segments(steps, byId);
  const lastSeg = segs[segs.length - 1];
  // Between steps the agent is busy but nothing is visibly running: the
  // Working line stands in, after a short grace so it never flashes.
  const idle =
    !!turn.live &&
    (segs.length === 0 ||
      (lastSeg.kind === "run" && !runIsActive(lastSeg.items)) ||
      (lastSeg.kind === "agents" && !lastSeg.ids.some((id) => byId.get(id)?.status === "running")));
  const showWorking = useDelayed(idle, 250, steps.length);

  let boundary = startMs;
  const body = segs.map((seg, index) => {
    if (seg.kind === "text") {
      boundary = seg.step.tsMs ?? boundary;
      const streaming = !!turn.live && index === segs.length - 1;
      return <Markdown key={seg.at} text={seg.step.text ?? ""} caret={streaming} />;
    }
    if (seg.kind === "agents") return <SubagentBlock key={seg.at} ids={seg.ids} />;
    const from = boundary;
    boundary = seg.items[seg.items.length - 1].tsMs ?? boundary;
    const live = !!turn.live && index === segs.length - 1;
    return <RunBlock key={seg.at} items={seg.items} boundary={from} live={live} root={root} />;
  });

  const changes = turn.changes ?? [];
  return (
    <div className={`turn assistant${turn.live ? " live" : ""}${isLast ? " last" : ""}`}>
      {body}
      {showWorking ? (
        <div className="run" role="status" aria-live="polite">
          <WorkingLine sinceMs={boundary} />
        </div>
      ) : null}
      {turn.failed ? (
        <div className="stopped">
          <Icon name="warn" />
          Stopped
        </div>
      ) : null}
      {!turn.live && changes.length > 0 ? (
        <ChangesCard changes={changes} canReview={canReview} onReview={onReview} />
      ) : null}
      {turn.live ? null : <Foot turn={turn} source={turn.narration ?? ""} startMs={startMs} markdown />}
    </div>
  );
}

// ---- steps ----

/** The ordered chain; turns without one (older projections) synthesize it. */
function effectiveSteps(turn: Turn): Step[] {
  if (turn.steps?.length) return turn.steps;
  const steps: Step[] = [];
  if (turn.thinking) steps.push({ kind: "thinking", text: turn.thinking });
  for (const tool of turn.tools ?? []) {
    steps.push({
      kind: "tool",
      toolName: tool.name,
      toolInput: tool.input,
      toolOutput: tool.output,
      toolError: tool.isError,
      toolDone: true,
    });
  }
  if (turn.narration) steps.push({ kind: "narration", text: turn.narration });
  return steps;
}

type Seg =
  | { kind: "text"; step: Step; at: number }
  | { kind: "agents"; ids: string[]; at: number }
  | { kind: "run"; items: Step[]; at: number };

/** Narration stays on its own; consecutive thinking/tool steps form one run.
 *  Tool calls that spawned sub-agents leave the run: all of a turn's agents
 *  gather into one block at the first spawn, so a running one is never folded
 *  away (unknown ids stay ordinary tool rows). */
function segments(steps: Step[], known: ReadonlyMap<string, unknown>): Seg[] {
  const out: Seg[] = [];
  let run: Extract<Seg, { kind: "run" }> | null = null;
  let agents: Extract<Seg, { kind: "agents" }> | null = null;
  steps.forEach((step, at) => {
    const spawned = step.kind === "tool" ? (step.subagentIds ?? []).filter((id) => known.has(id)) : [];
    if (spawned.length > 0) {
      if (!agents) {
        agents = { kind: "agents", ids: [], at };
        out.push(agents);
      }
      for (const id of spawned) if (!agents.ids.includes(id)) agents.ids.push(id);
      run = null;
      return;
    }
    if (step.kind === "thinking" || step.kind === "tool") {
      if (!run) {
        run = { kind: "run", items: [], at };
        out.push(run);
      }
      run.items.push(step);
      return;
    }
    run = null;
    out.push({ kind: "text", step, at });
  });
  return out;
}

function isRunning(step: Step): boolean {
  // toolDone is the bridge's "result landed" flag: an empty result is still a
  // result, so a finished call with no output never reads as running.
  return step.kind === "tool" && !step.toolDone && !step.toolError;
}

/** A live run is "active" while its last step is thinking or any call (calls
 *  can run in parallel) has no result yet. */
function runIsActive(items: Step[]): boolean {
  return items[items.length - 1].kind === "thinking" || items.some(isRunning);
}

// ---- run (one summary line, opening into ledger rows) ----

function RunBlock({
  items,
  boundary,
  live,
  root,
}: {
  items: Step[];
  boundary?: number;
  live: boolean;
  root?: string;
}) {
  const [toggled, setToggled] = useState<boolean | null>(null);
  const tools = items.filter((step) => step.kind === "tool");
  const thinks = items.filter((step) => step.kind === "thinking");

  // One lone call is just its row; the fold starts at thinking or two calls.
  if (thinks.length === 0 && tools.length === 1) {
    return (
      <div className="run">
        <ToolRow step={tools[0]} running={live && isRunning(tools[0])} root={root} />
      </div>
    );
  }

  const last = items[items.length - 1];
  const thinkingLive = live && last.kind === "thinking";
  const toolRunning = live && tools.some(isRunning);
  const open = toggled ?? live;
  const seconds = thoughtSeconds(items, boundary);
  const failed = tools.filter((step) => step.toolError).length;

  const parts: React.ReactNode[] = [];
  if (thinks.length > 0) {
    parts.push(
      thinkingLive ? (
        <span key="think" className="shim">Thinking…</span>
      ) : (
        <span key="think" className="v">{seconds ? `Thought for ${fmtDuration(seconds)}` : "Thought"}</span>
      ),
    );
  }
  if (tools.length > 0) {
    if (parts.length > 0) parts.push(<span key="dot" className="o"> · </span>);
    parts.push(<Summary key="sum" tools={tools} live={toolRunning} />);
  }

  return (
    <div className={`run${open ? " open" : ""}`}>
      <button
        type="button"
        className={`runbtn${open ? " open" : ""}`}
        aria-expanded={open}
        onClick={() => setToggled(!open)}
      >
        {thinkingLive || toolRunning ? <span className="spin" /> : null}
        <span>{parts}</span>
        {failed > 0 ? <span className="failed">{failed} failed</span> : null}
        <span className="chev"><Icon name="chev" size={13} /></span>
      </button>
      {open ? (
        <div className="runbody">
          {items.map((step, index) =>
            step.kind === "thinking" ? (
              <div key={index} className="think">{step.text}</div>
            ) : (
              <ToolRow
                key={index}
                step={step}
                running={live && isRunning(step)}
                root={root}
              />
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}

/** Seconds spent thinking: each thinking step's gap from the step before it
 *  (provider timestamps mark when a block landed); undefined without data. */
function thoughtSeconds(items: Step[], boundary?: number): number | undefined {
  let previous = boundary;
  let total = 0;
  for (const step of items) {
    if (step.kind === "thinking" && step.tsMs && previous && step.tsMs > previous) {
      total += (step.tsMs - previous) / 1000;
    }
    if (step.tsMs) previous = step.tsMs;
  }
  const rounded = Math.round(total);
  return rounded >= 1 ? rounded : undefined;
}

function Summary({ tools, live }: { tools: Step[]; live: boolean }) {
  const parts = summaryParts(tools, live);
  return (
    <>
      {parts.map(([verb, object], index) => (
        <React.Fragment key={verb}>
          {index > 0 ? <span className="o">, </span> : null}
          <span className={live ? "shim" : index === 0 ? "v" : "o"}>{index === 0 ? verb : verb.toLowerCase()}</span>{" "}
          <span className="o">{object}</span>
        </React.Fragment>
      ))}
    </>
  );
}

function summaryParts(tools: Step[], live: boolean): [string, string][] {
  const count = new Map<Category, number>();
  const edited = new Set<string>();
  for (const step of tools) {
    const cat = toolCategory(step.toolName ?? "");
    count.set(cat, (count.get(cat) ?? 0) + 1);
    if (cat === "edit") edited.add(step.toolInput || `#${edited.size}`);
  }
  const n = (cat: Category) => count.get(cat) ?? 0;
  const plural = (c: number, word: string) => `${c} ${word}${c === 1 ? "" : word.endsWith("ch") ? "es" : "s"}`;
  const out: [string, string][] = [];
  const explored = n("read") + n("search");
  if (explored > 0) {
    const bits: string[] = [];
    if (n("read")) bits.push(plural(n("read"), "file"));
    if (n("search")) bits.push(plural(n("search"), "search"));
    out.push([live ? "Exploring" : "Explored", bits.join(", ")]);
  }
  if (n("edit")) out.push([live ? "Editing" : "Edited", plural(edited.size, "file")]);
  if (n("command")) out.push([live ? "Running" : "Ran", plural(n("command"), "command")]);
  if (n("web")) out.push([live ? "Fetching" : "Fetched", plural(n("web"), "page")]);
  if (n("agent")) out.push([live ? "Delegating" : "Delegated", plural(n("agent"), "task")]);
  if (n("plan")) out.push([live ? "Updating" : "Updated", "tasks"]);
  if (n("step")) out.push([live ? "Using" : "Used", plural(n("step"), "other tool")]);
  return out;
}

// ---- tool rows ----

export type Category = "plan" | "agent" | "web" | "edit" | "read" | "search" | "command" | "step";

// Category vocabulary over the providers' tool names. Order matters: plan,
// agent and web names are checked first so "TodoWrite" is not an edit and
// "web_search" is not a code search.
export function toolCategory(name: string): Category {
  if (/todo|plan/i.test(name)) return "plan";
  // Codex's wait/close/resume_agent only manage an agent already delegated.
  if (/^(wait|close|resume)_agent$/i.test(name)) return "step";
  if (/task|agent|dispatch|spawn/i.test(name)) return "agent";
  if (/web|fetch|http|browse/i.test(name)) return "web";
  if (/edit|write|patch|apply|replace/i.test(name)) return "edit";
  if (/read|view|\bcat\b|open/i.test(name)) return "read";
  if (/grep|glob|search|find|list|\bls\b/i.test(name)) return "search";
  if (/bash|command|exec|shell|terminal|run|cmd/i.test(name)) return "command";
  return "step";
}

const VERB: Record<Category, string> = {
  read: "Read",
  search: "Searched",
  edit: "Edited",
  command: "Ran",
  web: "Fetched",
  agent: "Delegated",
  plan: "Updated",
  step: "Used",
};

function ToolRow({ step, running, root }: { step: Step; running: boolean; root?: string }) {
  const [open, setOpen] = useState(false);
  const name = step.toolName ?? "";
  const cat = toolCategory(name);
  const input = step.toolInput ?? "";
  const failed = !!step.toolError;
  const hasDetail = !!(step.toolInput || step.toolOutput);

  let object: React.ReactNode = null;
  if (cat === "read" || cat === "edit") {
    // Some agents' one-line preview is their arguments' JSON; the path is
    // the first path-like token in it.
    const path = input.startsWith("{") ? input.match(JSON_PATH)?.[0] : input;
    object = path ? <PathLabel path={relativeTo(path, root)} /> : <span className="q">{input || name}</span>;
  } else if (cat === "command") {
    object = input ? <code className="cmd">{input}</code> : <span className="q">{name}</span>;
  } else if (cat === "plan") {
    object = <span className="q">task list</span>;
  } else if (cat === "step") {
    object = <span className="q">{[name, input].filter(Boolean).join(" ")}</span>;
  } else {
    object = <span className="q">{input || name}</span>;
  }

  return (
    <div className="tool">
      <button
        type="button"
        className={`trow${failed ? " err" : ""}${hasDetail ? " has-detail" : ""}`}
        aria-expanded={hasDetail ? open : undefined}
        onClick={() => hasDetail && setOpen(!open)}
      >
        <span className="lead">{running ? <span className="spin" /> : <Icon name={cat as IconName} size={13} />}</span>
        <span className="verb">{VERB[cat]}</span>
        {object}
        {failed ? <span className="meta err">failed</span> : null}
      </button>
      {open ? (
        <div className="tdetail">
          {step.toolInput ? <pre>{step.toolInput}</pre> : null}
          {step.toolOutput ? <pre>{step.toolOutput}</pre> : null}
        </div>
      ) : null}
    </div>
  );
}

const JSON_PATH = /(?:\/?[\w@.+~-]+\/)+[\w@.+~-]+\.[A-Za-z0-9]{1,8}/;

function relativeTo(path: string, root?: string): string {
  return root && path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path;
}

function PathLabel({ path }: { path: string }) {
  const at = path.lastIndexOf("/");
  return (
    <span className="path" data-path={path}>
      {at < 0 ? <b>{path}</b> : (
        <>
          <span>{path.slice(0, at + 1)}</span>
          <b>{path.slice(at + 1)}</b>
        </>
      )}
    </span>
  );
}

// ---- changes card ----

function Delta({ add, del }: { add: number; del: number }) {
  if (!add && !del) return null;
  return (
    <span className="delta">
      {add ? <i className="add">+{add}</i> : null}
      {del ? <i className="del">−{del}</i> : null}
    </span>
  );
}

function ChangesCard({
  changes,
  canReview,
  onReview,
}: {
  changes: ChatChange[];
  canReview: boolean;
  onReview: (path: string) => void;
}) {
  const counted = changes.filter((change) => change.counted);
  const add = counted.reduce((sum, change) => sum + (change.additions ?? 0), 0);
  const del = counted.reduce((sum, change) => sum + (change.deletions ?? 0), 0);
  const noun = `${changes.length} file${changes.length === 1 ? "" : "s"}`;
  return (
    <div className="changes">
      <div className="chead">
        <span className="ttl">
          <Icon name="edit" />
          {counted.length > 0 ? "Changes in working tree" : `Edited ${noun}`}
          {counted.length > 0 ? <span className="o">{noun}</span> : null}
          <Delta add={add} del={del} />
        </span>
        {canReview ? (
          <button type="button" className="rev" title="Open Diff Review" onClick={() => onReview("")}>
            <Icon name="diff" size={13} />
            Review changes
          </button>
        ) : null}
      </div>
      {changes.map((change) => (
        <div className="crow" key={change.path}>
          {canReview && !change.path.startsWith("/") ? (
            <button type="button" className="cpath" onClick={() => onReview(change.path)}>
              <PathLabel path={change.path} />
            </button>
          ) : (
            <PathLabel path={change.path} />
          )}
          {change.counted ? <Delta add={change.additions ?? 0} del={change.deletions ?? 0} /> : null}
        </div>
      ))}
    </div>
  );
}

// ---- footer / copy ----

function Foot({
  turn,
  source,
  startMs,
  markdown,
}: {
  turn: Turn;
  source: string;
  startMs?: number;
  markdown?: boolean;
}) {
  const [copied, setCopied] = useState("");
  const copy = (value: string, kind: string) => {
    void navigator.clipboard.writeText(value).then(() => {
      setCopied(kind);
      window.setTimeout(() => setCopied(""), 1500);
    });
  };
  const seconds = turnSeconds(turn, startMs);
  const time = turn.tsMs ? formatTime(turn.tsMs) : "";
  return (
    <div className="foot">
      {time || seconds ? (
        <span className="t">{[time, seconds ? fmtDuration(seconds) : ""].filter(Boolean).join(" · ")}</span>
      ) : null}
      {source ? (
        <>
          <button
            type="button"
            className={`ib${copied === "md" ? " copied" : ""}`}
            title={markdown ? "Copy Markdown" : "Copy"}
            aria-label={markdown ? "Copy Markdown" : "Copy"}
            onClick={() => copy(source, "md")}
          >
            <Icon name={copied === "md" ? "check" : "copy"} />
          </button>
          {markdown ? (
            <button
              type="button"
              className={`ib txt${copied === "txt" ? " copied" : ""}`}
              title="Copy as plain text"
              onClick={() => copy(plainText(source), "txt")}
            >
              {copied === "txt" ? "Copied" : "TXT"}
            </button>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

/** Whole-turn duration: from the preceding user row to the last step. */
function turnSeconds(turn: Turn, startMs?: number): number | undefined {
  const steps = turn.steps ?? [];
  const end = [...steps].reverse().find((step) => step.tsMs)?.tsMs;
  if (!startMs || !end || end <= startMs) return undefined;
  const seconds = Math.round((end - startMs) / 1000);
  return seconds >= 1 ? seconds : undefined;
}

function fmtDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

function formatTime(tsMs: number): string {
  return new Date(tsMs).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// plainText strips common markdown syntax for the TXT copy variant.
function plainText(markdown: string): string {
  return markdown
    .replace(/```[^\n]*\n?/g, "")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/[*_]{1,3}([^*_]+)[*_]{1,3}/g, "$1")
    .replace(/^\s*[-*+]\s+/gm, "• ");
}

/**
 * userDisplay turns Claude Code's transcript records of slash commands into
 * what the user typed and saw: `<command-name>/model</command-name>…
 * <command-args>haiku</command-args>` reads "/model haiku", and
 * `<local-command-stdout>…</local-command-stdout>` is the command's own
 * output (shown muted, no bubble). Anything else is the message itself.
 */
export function userDisplay(text: string): { text: string; output: boolean } {
  const out = /^\s*<local-command-std(?:out|err)>([\s\S]*?)<\/local-command-std(?:out|err)>\s*$/.exec(text);
  if (out) return { text: out[1].trim() || "(no output)", output: true };
  // Claude Code's own "a sub-agent finished" message: its summary line, muted.
  const note = /^\s*<task-notification>[\s\S]*?<summary>([\s\S]*?)<\/summary>[\s\S]*<\/task-notification>\s*$/.exec(text);
  if (note) return { text: note[1].trim(), output: true };
  const name = /<command-name>([\s\S]*?)<\/command-name>/.exec(text);
  if (name) {
    const args = /<command-args>([\s\S]*?)<\/command-args>/.exec(text)?.[1].trim();
    return { text: args ? `${name[1].trim()} ${args}` : name[1].trim(), output: false };
  }
  return { text, output: false };
}

/** @path mentions in a user message read as inline chips (@"a path with spaces" is one chip). */
function mentions(text: string): React.ReactNode {
  return text.split(/(@"[^"\n]+"|@\S+)/g).map((part, index) =>
    part.startsWith("@") && part.length > 1 ? (
      <span key={index} className="mention" data-path={part.replace(/^@"?|"$/g, "")}>{part.replace(/^@"(.*)"$/, "@$1")}</span>
    ) : (
      part
    ),
  );
}

// useDelayed turns a condition on only after it has held for ms; any change
// of key restarts the wait (the anti-flicker rule for the Working line).
function useDelayed(on: boolean, ms: number, key: unknown): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    setShown(false);
    if (!on) return;
    const timer = window.setTimeout(() => setShown(true), ms);
    return () => window.clearTimeout(timer);
  }, [on, ms, key]);
  return on && shown;
}

// ---- markdown ----

// Markdown renders assistant narration (GFM tables/lists, fenced code with
// highlighting). Raw HTML never passes through: react-markdown skips it.
// caret marks the streaming tail: a small dot follows the last character.
export const Markdown = memo(function Markdown({ text, caret }: { text: string; caret?: boolean }) {
  return (
    <div className={`prose${caret ? " caret" : ""}`}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeHighlight]}
        components={{
          pre: (props) => <CodeBlock {...props} />,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});

function CodeBlock({ children, ...rest }: React.HTMLAttributes<HTMLPreElement>) {
  const ref = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const child = React.Children.toArray(children)[0] as React.ReactElement<{ className?: string }> | undefined;
  const lang = /language-([\w+#-]+)/.exec(child?.props?.className ?? "")?.[1] ?? "";
  const copy = () => {
    const text = ref.current?.textContent ?? "";
    if (text === "") return;
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <div className="codeb">
      <div className="ch">
        <span>{lang}</span>
        <button type="button" className="ib cp" title="Copy" aria-label="Copy code" onClick={copy}>
          <Icon name={copied ? "check" : "copy"} size={13} />
        </button>
      </div>
      <pre ref={ref} {...rest}>
        {children}
      </pre>
    </div>
  );
}
