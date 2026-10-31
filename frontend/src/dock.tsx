import React, { useEffect, useState } from "react";
import type { ChatBridgeInteraction, ChatBridgeLink, ChatBridgePlan, ChatBridgePlanItem } from "./mygo";
import { Icon } from "./icons";

/**
 * The docks above the composer, shaped by designs/chat-view (states, 2026-10-30):
 * the Tasks tray (the agent's own task list, read-only), the Question card
 * (structured interaction: options with their explanations, free-text row),
 * and the "waiting for you in the terminal" card when a prompt could not be
 * read structurally. Presentation only: every fact comes from the bridge.
 */

// ---- Tasks tray ----

const OPEN_STATES = new Set(["pending", "active", "blocked"]);

/** True once nothing is left to do (done / dropped only). */
export function planFinished(plan: ChatBridgePlan): boolean {
  return plan.items.every((item) => !OPEN_STATES.has(item.status));
}

function TaskGlyph({ status, live }: { status: string; live: boolean }) {
  switch (status) {
    case "done":
      return (
        <span className="tg done" aria-label="done">
          <Icon name="check" size={11} stroke={2.6} />
        </span>
      );
    case "active":
      return live ? <span className="tg"><span className="spin" /></span> : <span className="tg active" aria-label="in progress" />;
    case "blocked":
      return <span className="tg blocked" aria-label="blocked"><Icon name="warn" size={12} /></span>;
    case "dropped":
      return <span className="tg dropped" aria-label="dropped" />;
    default:
      return <span className="tg pending" aria-label="pending" />;
  }
}

/**
 * The agent's task list. One card above the composer: a header line with the
 * count and the item in progress, opening into the list. Open by default
 * while the list is short and unfinished; the user's toggle always wins.
 */
export function TasksTray({ plan, live, compact }: { plan: ChatBridgePlan; live: boolean; compact: boolean }) {
  const [toggled, setToggled] = useState<boolean | null>(null);
  const items = plan.items;
  const done = items.filter((item) => item.status === "done").length;
  const counted = items.filter((item) => item.status !== "dropped").length;
  const finished = planFinished(plan);
  // A card asking the user something takes the room: the list folds then.
  const open = toggled ?? (!compact && !finished && items.length <= 7);
  const current = items.find((item) => item.status === "active") ?? items.find((item) => item.status === "pending");

  let phase = "";
  return (
    <div className={`tasks${finished ? " finished" : ""}`}>
      <button type="button" className="thead" aria-expanded={open} onClick={() => setToggled(!open)}>
        <Icon name="tasks" size={15} />
        <span className="ttl">Tasks</span>
        <span className="cnt">
          {done}/{counted}
        </span>
        <span className="cur">{finished ? "All done" : current?.text ?? ""}</span>
        <span className={`chev${open ? " open" : ""}`}>
          <Icon name="chev" size={13} />
        </span>
      </button>
      {open ? (
        <ol className="tlist">
          {items.map((item: ChatBridgePlanItem, index) => {
            const heading = item.phase && item.phase !== phase ? item.phase : "";
            phase = item.phase ?? phase;
            return (
              <React.Fragment key={index}>
                {heading ? <li className="tphase">{heading}</li> : null}
                <li className={`titem ${item.status}`}>
                  <TaskGlyph status={item.status} live={live} />
                  <span className="tx">{item.text}</span>
                </li>
              </React.Fragment>
            );
          })}
        </ol>
      ) : null}
    </div>
  );
}

// ---- Question / approval card ----

export interface QuestionDockProps {
  interaction: ChatBridgeInteraction;
  textPrompt: { id: string; option: string } | null;
  textAnswer: string;
  answering: boolean;
  composing: React.MutableRefObject<boolean>;
  onPick: (option: string) => void;
  /** Multi-select questions submit every ticked row together. */
  onPickMany: (options: string[]) => void;
  onTextChange: (text: string) => void;
  onTextSubmit: () => void;
  onTextCancel: () => void;
}

/** "Other (type your own)", "Type something.", "Chat about this": rows that take typed text. */
export function isFreeTextOption(option: string): boolean {
  return /^(type something|chat about|other)\b/i.test(option.trim());
}

// inlineCode renders `backtick` spans as code; option descriptions are plain
// text otherwise (no Markdown engine for a one-line hint).
function inlineCode(text: string) {
  return text.split("`").map((part, i) => (i % 2 === 1 ? <code key={i}>{part}</code> : part));
}

export function QuestionDock(props: QuestionDockProps) {
  const { interaction, textPrompt, textAnswer, answering, composing } = props;
  const typing = textPrompt?.id === interaction.id;
  const permission = interaction.kind === "permission";
  const options = interaction.options;
  const multi = Boolean(interaction.multi);
  // Ticked rows of a multi-select question (the user's local draft).
  const [ticked, setTicked] = useState<string[]>([]);
  const toggle = (option: string) =>
    setTicked((current) => (current.includes(option) ? current.filter((o) => o !== option) : [...current, option]));
  const submitMany = () => props.onPickMany(options.filter((o) => ticked.includes(o)));

  // 1-9 pick the matching row (tick it, for a multi-select) while focus is not in a text field.
  const { onPick } = props;
  useEffect(() => {
    if (typing) return;
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target && /^(input|textarea)$/i.test(target.tagName)) return;
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      const index = Number(event.key) - 1;
      if (Number.isInteger(index) && index >= 0 && index < options.length) {
        event.preventDefault();
        if (multi) setTicked((current) => (current.includes(options[index]) ? current.filter((o) => o !== options[index]) : [...current, options[index]]));
        else onPick(options[index]);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [typing, options, onPick, multi]);

  const onKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (composing.current || event.nativeEvent.isComposing) return;
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      props.onTextSubmit();
    }
    if (event.key === "Escape") {
      event.preventDefault();
      props.onTextCancel();
    }
  };

  return (
    <div className={`ask${permission ? " permission" : ""}`} role="alert">
      <div className="ah">
        <Icon name={permission ? "shield" : "help"} size={15} />
        <span className="pill">{permission ? "Approval" : interaction.header || "Question"}</span>
        <span className="await">{interaction.progress ? `Question ${interaction.progress} · Waiting for you` : "Waiting for you"}</span>
      </div>
      <div className="prompt">{interaction.prompt}</div>
      {typing ? (
        <div className="answer">
          <textarea
            autoFocus
            value={textAnswer}
            placeholder={`Answering “${textPrompt?.option}” — type your answer…`}
            aria-label="Free-text answer"
            onChange={(event) => props.onTextChange(event.target.value)}
            onKeyDown={onKeyDown}
            onCompositionStart={() => (composing.current = true)}
            onCompositionEnd={() => (composing.current = false)}
          />
          <div className="answer-row">
            <button type="button" className="ghost" onClick={props.onTextCancel}>
              Back
            </button>
            <span className="answer-hint">Enter submits · Esc goes back</span>
            <button
              type="button"
              className="answer-send"
              disabled={textAnswer.trim() === "" || answering}
              onClick={props.onTextSubmit}
            >
              {answering ? "Sending…" : "Send answer"}
            </button>
          </div>
        </div>
      ) : (
        <div className="options">
          {options.map((option, index) => {
            const description = interaction.descriptions?.[index];
            const free = isFreeTextOption(option);
            return (
              <button
                key={option}
                type="button"
                className={`opt${free ? " free" : ""}${multi && ticked.includes(option) ? " on" : ""}`}
                role={multi ? "checkbox" : undefined}
                aria-checked={multi ? ticked.includes(option) : undefined}
                onClick={() => (multi ? toggle(option) : onPick(option))}
              >
                {multi ? <span className="box" aria-hidden>{ticked.includes(option) ? <Icon name="check" size={12} stroke={2.6} /> : null}</span> : <kbd>{index + 1}</kbd>}
                <span className="otx">
                  <span className="olabel">{option}</span>
                  {description ? <span className="odesc">{inlineCode(description)}</span> : null}
                </span>
                {free ? <Icon name="pencil" size={13} /> : null}
              </button>
            );
          })}
          {multi ? (
            <div className="answer-row multi-row">
              <span className="answer-hint">{ticked.length === 0 ? "Tick any that apply" : `${ticked.length} selected`}</span>
              <button type="button" className="answer-send" disabled={props.answering} onClick={submitMany}>
                {props.answering ? "Sending…" : "Submit"}
              </button>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}

// ---- Blocked in the terminal ----

/** A blocked agent whose prompt the page could not read: say so, one click away. */
export function BlockedDock({ onOpenTerminal }: { onOpenTerminal: () => void }) {
  return (
    <div className="ask blocked" role="status">
      <div className="ah">
        <Icon name="terminal" size={15} />
        <span className="pill">Needs you</span>
        <span className="prompt inline">The agent is waiting for input in its terminal.</span>
        <button type="button" className="goterm" onClick={onOpenTerminal}>
          Open Terminal
        </button>
      </div>
    </div>
  );
}

// ---- Working line ----

function formatElapsed(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

/**
 * "Working…" with the real elapsed time once it passes a few seconds, so a
 * slow model reads as slow rather than stuck. `sinceMs` is the provider
 * timestamp the wait started at; without one the line times itself from mount.
 */
export function WorkingLine({ sinceMs }: { sinceMs?: number }) {
  const [origin] = useState(() => sinceMs ?? Date.now());
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const seconds = Math.max(0, Math.floor((now - (sinceMs ?? origin)) / 1000));
  return (
    <span className="workline">
      <span className="shim working">Working…</span>
      {seconds >= 5 && seconds < 3600 ? <span className="elapsed">{formatElapsed(seconds)}</span> : null}
    </span>
  );
}

// ---- Before there is a conversation ----

const STARTERS = ["Explain this repository", "Find and fix a failing test", "Review my uncommitted changes"];

/**
 * The two states ahead of a transcript: the session is being read, or the
 * agent has not opened one yet (a fresh pane). Centered in the log, quiet.
 * The fresh pane is an invitation, not a wait: the composer below is live
 * and the first prompt it sends opens the conversation.
 */
export function SessionState(props: {
  kind: "loading" | "waiting";
  provider?: string;
  /** The agent is still launching: the composer holds Send until it is ready. */
  starting?: boolean;
  onPick?: (text: string) => void;
  /** Recent sessions of this agent's provider in its folder; picking one
   *  follows its transcript (the agent never reported a session of its own). */
  links?: ChatBridgeLink[];
  onLink?: (key: string) => void;
  onOpenTerminal: () => void;
}) {
  if (props.kind === "loading") {
    return (
      <div className="sess-state" role="status" aria-live="polite">
        <span className="spin" aria-hidden="true" />
        <span className="shim">Loading conversation…</span>
      </div>
    );
  }
  const name = props.provider || "The agent";
  return (
    <div className="sess-state invite" role="status">
      <Icon name="step" size={18} />
      <div className="st-title">Start the conversation</div>
      <div className="st-sub">
        {props.starting ? `${name} is starting…` : `${name} is ready.`} <b>Type a prompt below</b> and Solco sends it to the agent — the
        conversation shows up here as soon as the agent reports its session.
      </div>
      {props.starting || !props.onPick ? null : (
        <div className="starters">
          {STARTERS.map((text) => (
            <button key={text} type="button" onClick={() => props.onPick?.(text)}>
              {text}
            </button>
          ))}
        </div>
      )}
      {props.links && props.links.length > 0 && !props.starting ? (
        <div className="links">
          <div className="links-h">Already working here? Show a recent session instead — it may not be this agent's.</div>
          {props.links.map((link) => (
            <button key={link.key} type="button" onClick={() => props.onLink?.(link.key)}>
              <span className="l-title">{link.title}</span>
              <span className="l-meta">
                {ago(link.updatedMs)} · {link.messages} msg
              </span>
            </button>
          ))}
        </div>
      ) : null}
      <button type="button" className="goterm" onClick={props.onOpenTerminal}>
        Open Terminal
      </button>
    </div>
  );
}

function ago(ms: number): string {
  const secs = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (secs < 90) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 90) return `${mins}m ago`;
  return `${Math.round(mins / 60)}h ago`;
}
