import { useEffect, useState } from "react";
import { Icon } from "../icons";
import { feedback } from "../mygo";

/**
 * "Clean up": the two ways to keep the inbox short. A one-off cleanup deletes the finished threads idle for more than N
 * days (a dry run says how many first); the keep-for setting removes them on its own once they are older than that.
 * A thread an Agent is still waiting on is never touched by either. Desktop and phone share this dialog.
 */

const AGES = [7, 30, 90, 180];
const KEEP = [
  { days: 0, label: "Forever" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
];

const text = (e: unknown) => (e instanceof Error ? e.message : String(e)).replace(/^[a-z_]+: /, "");

export function TidyDialog(props: { keep: number; onClose: () => void; onChanged: () => void; onError: (m: string) => void }) {
  const { keep, onClose, onChanged, onError } = props;
  const [age, setAge] = useState(30);
  const [count, setCount] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [kept, setKept] = useState(keep);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    let live = true;
    setCount(null);
    feedback
      .cleanup(age, true)
      .then((n) => live && setCount(n))
      .catch((e) => live && onError(text(e)));
    return () => {
      live = false;
    };
  }, [age, onError]);

  const remove = () => {
    setBusy(true);
    feedback
      .cleanup(age, false)
      .then(() => {
        onChanged();
        onClose();
      })
      .catch((e) => {
        onError(text(e));
        setBusy(false);
      });
  };
  const keepFor = (days: number) => {
    setKept(days);
    feedback.setRetention(days).then(onChanged).catch((e) => onError(text(e)));
  };

  return (
    <div className="fb-scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="fb-dialog" role="dialog" aria-modal="true" aria-label="Clean up threads">
        <header className="fb-dh">
          <div className="fb-dt">Clean up threads</div>
          <button className="fb-x" aria-label="Close" onClick={onClose}>
            <Icon name="x" size={16} />
          </button>
        </header>
        <div className="fb-dl">Delete finished threads older than</div>
        <div className="fb-seg" role="tablist">
          {AGES.map((d) => (
            <button key={d} role="tab" aria-selected={age === d} className={age === d ? "on" : ""} onClick={() => setAge(d)}>
              {d} days
            </button>
          ))}
        </div>
        <p className="fb-dn sub">
          {count === null
            ? "Counting…"
            : count === 0
              ? "Nothing to clean up."
              : `${count} finished thread${count === 1 ? "" : "s"} will be deleted. A thread an agent is still waiting on is never deleted.`}
        </p>
        <div className="fb-send">
          <span className="fb-hint" />
          <button className="fb-ghost" onClick={onClose}>
            Cancel
          </button>
          <button className="fb-primary danger" disabled={!count || busy} onClick={remove}>
            {count ? `Delete ${count}` : "Delete"}
          </button>
        </div>
        <div className="fb-dsep" />
        <div className="fb-dl">Keep finished threads for</div>
        <div className="fb-seg" role="tablist">
          {KEEP.map((k) => (
            <button key={k.days} role="tab" aria-selected={kept === k.days} className={kept === k.days ? "on" : ""} onClick={() => keepFor(k.days)}>
              {k.label}
            </button>
          ))}
        </div>
        <p className="fb-dn sub">{kept === 0 ? "Nothing is removed on its own." : `Finished threads idle for more than ${kept === 365 ? "a year" : `${kept} days`} are removed automatically.`}</p>
      </div>
    </div>
  );
}
