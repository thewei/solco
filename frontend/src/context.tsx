import { useEffect, useRef, useState } from "react";
import { chat, type ChatContextUsage } from "./mygo";

/**
 * The context control in the composer bar (designs/chat-context, B):
 * a ring that opens a card — used / window with a bar, the session's average
 * cache hit rate, its token totals and a Compact action. The numbers are
 * read once when the card opens (and again if the context grows while it is
 * open) through the bridge's read-only ContextUsage; nothing is polled or
 * stored. When the agent states no window (Claude Code) there is no bar and
 * no percentage: the ring is a plain circle and the card shows the used count.
 */

const HOT = 0.7; // from here the bar turns amber and Compact is the main action
const FULL = 0.9;

/** tokens formats a count the way the agents' own listings do: 132k, 1.2M. */
export function tokens(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(n % 1e6 === 0 ? 0 : 1)}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
  return String(n);
}

/** level is how full the window is: "" below 70%, "warn" from 70%, "full" from 90%. */
export function level(fraction: number): "" | "warn" | "full" {
  return fraction >= FULL ? "full" : fraction >= HOT ? "warn" : "";
}

const RADIUS = 7;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

function Ring({ fraction, lv }: { fraction: number | null; lv: string }) {
  const stroke = lv === "full" ? "var(--danger)" : lv === "warn" ? "var(--working)" : "var(--i3)";
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden>
      <circle cx="9" cy="9" r={RADIUS} fill="none" stroke="var(--hair2)" strokeWidth="2.5" />
      <circle
        cx="9" cy="9" r={RADIUS} fill="none"
        stroke={fraction === null ? "var(--i4)" : stroke}
        strokeWidth="2.5"
        strokeDasharray={fraction === null ? undefined : `${fraction * CIRCUMFERENCE} ${CIRCUMFERENCE}`}
        strokeLinecap="round"
        transform="rotate(-90 9 9)"
      />
    </svg>
  );
}

export function ContextControl(props: {
  used: number;
  /** open is owned by the composer so one panel is open at a time. */
  open: boolean;
  onToggle: () => void;
  onClose: () => void;
  /** canCompact: the agent has a /compact command and can take it now. */
  canCompact: boolean;
  compactHint: string;
  onCompact: () => void;
  /** Reports how full the window is (null: the agent states no window) whenever it is read. */
  onFraction?: (fraction: number | null) => void;
}) {
  const { used, open } = props;
  const [usage, setUsage] = useState<ChatContextUsage | null>(null);
  const root = useRef<HTMLSpanElement>(null);
  // The ring needs the window before the card is ever opened, so read once
  // per context change; the card reuses it. One streamed read of the session
  // file each time the agent's context moves (once per turn).
  useEffect(() => {
    let live = true;
    chat.contextUsage().then(
      (next) => live && setUsage(next),
      () => live && setUsage(null),
    );
    return () => {
      live = false;
    };
  }, [used]);
  useEffect(() => {
    if (!open) return;
    const away = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) props.onClose();
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        props.onClose();
      }
    };
    window.addEventListener("mousedown", away, true);
    window.addEventListener("keydown", key, true);
    return () => {
      window.removeEventListener("mousedown", away, true);
      window.removeEventListener("keydown", key, true);
    };
  }, [open, props.onClose]);

  const window_ = usage?.window ?? 0;
  const fraction = window_ > 0 ? Math.min(1, used / window_) : null;
  const { onFraction } = props;
  useEffect(() => {
    onFraction?.(fraction);
  }, [fraction, onFraction]);
  const lv = fraction === null ? "" : level(fraction);
  const percent = fraction === null ? "" : `${Math.round(fraction * 100)}%`;
  const totals: [string, number][] = usage
    ? [
        ["Input", usage.input],
        ["Output", usage.output],
        ["Cache read", usage.cacheRead],
        ["Cache write", usage.cacheWrite],
      ]
    : [];
  const hasTotals = totals.some(([, value]) => value > 0);
  return (
    <span ref={root} className="ctx">
      <button
        type="button"
        className={`ringbtn${open ? " on" : ""}${lv ? ` ${lv}` : ""}`}
        title="Context window"
        aria-label={fraction === null ? `Context window, ${tokens(used)} used` : `Context window, ${percent} used`}
        aria-expanded={open}
        onClick={props.onToggle}
      >
        <Ring fraction={fraction} lv={lv} />
        {lv ? <span className="pct">{percent}</span> : null}
      </button>
      {open ? (
        <div className="ctxcard" role="dialog" aria-label="Context window">
          <div className="ctx-h">
            <b>Context window</b>
            <span>
              {fraction === null
                ? `${tokens(used)} tokens`
                : `${tokens(used)} / ${tokens(window_)} (${(fraction * 100).toFixed(1)}%)`}
            </span>
          </div>
          {fraction !== null ? (
            <div className={`ctx-bar${lv ? ` ${lv}` : ""}`}>
              <i style={{ width: `${fraction * 100}%` }} />
            </div>
          ) : null}
          {usage?.cacheHitRate !== undefined ? (
            <div className="ctx-row">
              Avg. cache hit rate<b>{usage.cacheHitRate.toFixed(1)}%</b>
            </div>
          ) : null}
          {hasTotals ? (
            <div className="ctx-sec">
              {totals.map(([name, value]) => (
                <div className="ctx-row" key={name}>
                  {name}
                  <b>{tokens(value)}</b>
                </div>
              ))}
            </div>
          ) : null}
          {fraction === null ? <div className="ctx-note">This agent does not report its window size.</div> : null}
          {props.canCompact || props.compactHint ? (
            <button
              type="button"
              className={`ctx-cmp${lv ? " hot" : ""}`}
              disabled={!props.canCompact}
              title={props.compactHint || "Summarize the conversation to free context"}
              onClick={props.onCompact}
            >
              Compact conversation <small>/compact</small>
            </button>
          ) : null}
        </div>
      ) : null}
    </span>
  );
}
