import { useState } from "react";
import { Icon } from "../icons";

/**
 * One-tap replies above the Send row (2026-11-09): a few stock answers plus the user's own, which they save from
 * what they typed. Kept in this page's localStorage (the phone's WebView keeps its own), never sent anywhere.
 */

const STOCK = ["Continue", "Looks good, go ahead", "Hold off, don't change anything yet"];
const KEY = "solco.feedback.quick";
const MAX_SAVED = 6;
const MAX_LEN = 200;

function load(): string[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(raw) ? raw.filter((s): s is string => typeof s === "string" && s.trim() !== "").slice(0, MAX_SAVED) : [];
  } catch {
    return [];
  }
}

function store(list: string[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    // private mode / quota: the saved replies just do not last
  }
}

export function QuickReplies(props: { current: string; onUse: (text: string) => void }) {
  const { current, onUse } = props;
  const [saved, setSaved] = useState(load);
  const typed = current.trim().slice(0, MAX_LEN);
  const canSave = typed !== "" && !STOCK.includes(typed) && !saved.includes(typed) && saved.length < MAX_SAVED;
  const update = (next: string[]) => {
    setSaved(next);
    store(next);
  };
  return (
    <div className="fb-quick" aria-label="Quick replies">
      {STOCK.map((s) => (
        <button key={s} className="fb-tool" onClick={() => onUse(s)}>
          {s}
        </button>
      ))}
      {saved.map((s) => (
        <span className="fb-tool saved" key={s}>
          <button className="fb-quick-use" title={s} onClick={() => onUse(s)}>
            {s}
          </button>
          <button className="fb-quick-x" aria-label={`Remove saved reply ${s}`} onClick={() => update(saved.filter((x) => x !== s))}>
            <Icon name="x" size={12} />
          </button>
        </span>
      ))}
      {canSave && (
        <button className="fb-tool dashed" onClick={() => update([...saved, typed])}>
          <Icon name="plus" size={12} />
          Save as quick reply
        </button>
      )}
    </div>
  );
}
