import { useEffect, useMemo, useRef, useState } from "react";
import type { RefObject } from "react";
import type { ChatTurn } from "./mygo";
import { Icon } from "./icons";
import { userDisplay } from "./turn";
import type { TurnList } from "./turnlist";

/**
 * TocRail is the turn outline in the left gutter (designs/chat-composer, T):
 * one tick per user message, a summary card on hover, a click scrolls to the
 * turn. Everything derives from the turns the page already holds — no bridge
 * call, no stored state, and (by AGENTS.md) no bookmarks.
 */

interface Entry {
  /** Index into the user-turn list. */
  at: number;
  /** Index of that turn in the whole transcript (what the turn list scrolls by). */
  index: number;
  title: string;
  heading: string;
  point: string;
  files: { name: string; image: boolean }[];
  /** Tick length bucket from how much the turn said. */
  weight: number;
}

const IMAGE = /\.(png|jpe?g|gif|webp|heic|bmp|svg)$/i;
const MAX_TICKS = 36;

/** plain strips the inline markdown a one-line summary should not show. */
function plain(text: string): string {
  return text
    .replace(/`([^`]*)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
}

function firstLine(text: string): string {
  const line = text.split("\n").find((row) => row.trim() !== "") ?? "";
  return plain(line);
}

/** outline derives one entry per user turn from the settled transcript. */
export function outline(turns: ChatTurn[]): Entry[] {
  const entries: Entry[] = [];
  let at = 0;
  turns.forEach((turn, index) => {
    if (!turn.user) return;
    const text = userDisplay(turn.text ?? "").text;
    const answer = turns.slice(index + 1).find((next) => !next.user);
    const reply = answer?.narration ?? "";
    const heading = reply.match(/^\s{0,3}#{1,6}\s+(.+)$/m)?.[1] ?? "";
    const point = reply.match(/^\s*(?:[-*+]|\d+[.)])\s+(.+)$/m)?.[1] ?? "";
    const files = [...text.matchAll(/@(\S+)/g)].slice(0, 3).map((hit) => {
      const name = hit[1].split("/").pop() ?? hit[1];
      return { name, image: IMAGE.test(name) };
    });
    const size = text.length + reply.length;
    entries.push({
      at: at++,
      index,
      title: firstLine(text.replace(/@"[^"\n]+"|@\S+/g, "").trim() || text),
      heading: plain(heading),
      point: plain(point),
      files,
      weight: size < 200 ? 0 : size < 800 ? 1 : size < 2000 ? 2 : 3,
    });
  });
  return entries;
}

const WIDTH = [8, 11, 14, 16];

export function TocRail({
  turns,
  logRef,
  top,
  scrollTo,
}: {
  turns: ChatTurn[];
  logRef: RefObject<HTMLDivElement | null>;
  top: TurnList["top"];
  scrollTo: TurnList["scrollTo"];
}) {
  const entries = useMemo(() => outline(turns), [turns]);
  const [active, setActive] = useState(0);
  const [hover, setHover] = useState<{ at: number; top: number } | null>(null);
  const frame = useRef(0);

  // Follow the scroll: the active tick is the last user turn whose top has
  // passed the upper third of the viewport.
  useEffect(() => {
    const log = logRef.current;
    if (!log) return;
    const update = () => {
      frame.current = 0;
      const line = log.scrollTop + log.clientHeight * 0.33;
      let current = 0;
      entries.forEach((entry, at) => {
        const start = top(entry.index);
        if (start !== null && start <= line) current = at;
      });
      setActive(current);
    };
    const onScroll = () => {
      if (!frame.current) frame.current = requestAnimationFrame(update);
    };
    update();
    log.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      log.removeEventListener("scroll", onScroll);
      cancelAnimationFrame(frame.current);
    };
  }, [logRef, entries, top]);

  if (entries.length < 3) return null;

  const step = Math.max(1, Math.ceil(entries.length / MAX_TICKS));
  const groups: Entry[][] = [];
  for (let i = 0; i < entries.length; i += step) groups.push(entries.slice(i, i + step));
  const activeGroup = Math.floor(Math.min(active, entries.length - 1) / step);

  const jump = (at: number) => {
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    scrollTo(entries[at].index, !reduced);
    // A tap has no pointer-leave: close the summary the tap opened.
    if (window.matchMedia("(hover: none)").matches) setHover(null);
  };

  const shown = hover ? entries[hover.at] : null;
  return (
    <nav className="toc" aria-label="Conversation outline" onMouseLeave={() => setHover(null)}>
      {groups.map((group, index) => {
        const head = group[0];
        return (
          <button
            key={head.at}
            type="button"
            className={`tick${index === activeGroup ? " on" : ""}`}
            aria-label={head.title}
            aria-current={index === activeGroup ? "true" : undefined}
            onClick={() => jump(head.at)}
            onFocus={(event) => setHover({ at: head.at, top: event.currentTarget.getBoundingClientRect().top })}
            onMouseEnter={(event) => setHover({ at: head.at, top: event.currentTarget.getBoundingClientRect().top })}
          >
            <i style={{ width: WIDTH[Math.max(...group.map((entry) => entry.weight))] }} />
          </button>
        );
      })}
      {shown && hover ? (
        <div className="toc-card" style={{ top: Math.max(8, Math.min(hover.top - 20, window.innerHeight - 190)) }} role="tooltip">
          <div className="t1">{shown.title}</div>
          {shown.heading ? <div className="t2">{shown.heading}</div> : null}
          {shown.point ? <div className="t3">{shown.point}</div> : null}
          {shown.files.length ? (
            <div className="t4">
              {shown.files.map((file) => (
                <span key={file.name}>
                  <Icon name={file.image ? "image" : "read"} size={13} />
                  {file.name}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </nav>
  );
}
