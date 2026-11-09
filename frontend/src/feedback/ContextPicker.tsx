import { useEffect, useRef, useState } from "react";
import { Icon, type IconName } from "../icons";
import { feedback, type ContextRef, type PickItem } from "../mygo";

/**
 * The answer form's "File" / "Skill" picker (2026-11-09): the user points the Agent at files, folders or Skills
 * with an answer. Rows come from the broker's read-only picker for the thread's project (names only: the broker
 * resolves a pick to a path when the answer is sent, so a phone never holds a Mac path). Picks toggle; the form
 * owns the list of picked refs.
 */

export type PickKind = "files" | "skills";

const KIND_ICON: Record<string, IconName> = { file: "read", folder: "folder", skill: "sparkles" };

export const refIcon = (kind: string): IconName => KIND_ICON[kind] ?? "read";

const key = (r: { kind: string; name: string }) => `${r.kind}\u0000${r.name}`;

export function ContextPicker(props: {
  threadId: string;
  kind: PickKind;
  picked: ContextRef[];
  onToggle: (item: PickItem) => void;
  onClose: () => void;
  onError: (m: string) => void;
}) {
  const { threadId, kind, picked, onToggle, onClose, onError } = props;
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<PickItem[] | null>(null);
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const seq = useRef(0);

  useEffect(() => {
    const mine = ++seq.current;
    const timer = window.setTimeout(() => {
      feedback
        .pickContext(threadId, kind, query)
        .then((rows) => {
          if (mine !== seq.current) return;
          setItems(rows);
          setActive(0);
        })
        .catch((e) => {
          if (mine !== seq.current) return;
          setItems([]);
          onError(e instanceof Error ? e.message : String(e));
        });
    }, query ? 150 : 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId, kind, query]);

  const chosen = new Set(picked.map(key));
  const empty = kind === "skills" ? "No installed skill matches." : "No file or folder matches.";

  return (
    <div className="fb-pick" role="dialog" aria-label={kind === "skills" ? "Pick skills" : "Pick files and folders"}>
      <div className="fb-pick-head">
        <span className="fb-search">
          <Icon name="search" size={14} />
          <input
            autoFocus
            value={query}
            placeholder={kind === "skills" ? "Search skills" : "Search files and folders"}
            aria-label="Search"
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              const n = items?.length ?? 0;
              if (e.key === "Escape") {
                e.stopPropagation();
                onClose();
              } else if ((e.key === "ArrowDown" || e.key === "ArrowUp") && n > 0) {
                e.preventDefault();
                const next = (active + (e.key === "ArrowDown" ? 1 : n - 1)) % n;
                setActive(next);
                listRef.current?.children[next]?.scrollIntoView({ block: "nearest" });
              } else if (e.key === "Enter" && items?.[active]) {
                e.preventDefault();
                e.stopPropagation(); // not the form's send
                onToggle(items[active]);
              }
            }}
          />
        </span>
        <button className="fb-link" onClick={onClose}>
          Done
        </button>
      </div>
      <div className="fb-pick-list" role="listbox" aria-multiselectable ref={listRef}>
        {items === null ? null : items.length === 0 ? (
          <div className="fb-pick-empty">{empty}</div>
        ) : (
          items.map((it, i) => {
            const on = chosen.has(key(it));
            return (
              <button key={key(it)} className={`fb-pk${on ? " on" : ""}${i === active ? " cur" : ""}`} role="option" aria-selected={on} onClick={() => onToggle(it)}>
                <Icon name={refIcon(it.kind)} size={14} />
                <span className="fb-pk-name">{it.name}</span>
                {it.detail && <span className={`fb-pk-detail${it.detail === "changed" ? " changed" : ""}`}>{it.detail}</span>}
                {on && <Icon name="check" size={14} />}
              </button>
            );
          })
        )}
      </div>
    </div>
  );
}
