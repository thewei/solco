import { useEffect, useRef, useState } from "react";
import type { ChatCatalog, ChatCatalogEntry } from "./mygo";
import { Icon } from "./icons";

/**
 * Composer panels (designs/chat-composer, C · Both): the `/` `$` list, the
 * `+` menu and the model switcher all open as one panel above the composer,
 * like the `@` completion list. Everything here is presentation: the lists
 * come from the bridge's read-only Catalog, sending goes through the same
 * `chat.send`, and nothing is executed in the page.
 */

export const IMAGE_EXT = /\.(png|jpe?g|gif|webp|heic|bmp|svg)$/i;

/** fileToBase64 reads a File as bare base64 (no data: prefix). */
export function fileToBase64(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.onload = () => {
      const url = String(reader.result);
      resolve(url.slice(url.indexOf(",") + 1));
    };
    reader.readAsDataURL(file);
  });
}

/** baseName is the last path segment (for chips). */
export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

const KIND_LABEL: Record<string, string> = {
  picker: "Picker",
  terminal: "Terminal",
  skill: "Skill",
  prompt: "Prompt",
};

// A Chinese input method types full-width or other characters for the keys
// that open the list: "/" gives "、" and Shift+4 gives "¥". They count as the
// key that was pressed; the draft itself is only rewritten when a row is taken.
const LEAD: Record<string, string> = { "/": "/", "／": "/", "、": "/", $: "$", "＄": "$", "¥": "$", "￥": "$" };

export interface SlashMatches {
  entries: ChatCatalogEntry[];
  /** Index in the draft where the typed token starts (taking a row replaces from here). */
  start: number;
}

/**
 * slashMatches lists what the draft's last token asks for. `/` opens the
 * commands and the skills invoked with `/` only at the start of a message
 * (that is where every agent reads a command); `$` lists the skills invoked
 * with `$`, which Codex also reads in the middle of a sentence. Prefix hits
 * rank before substring hits; max 60 rows.
 */
export function slashMatches(catalog: ChatCatalog | null, draft: string): SlashMatches {
  const none: SlashMatches = { entries: [], start: 0 };
  const m = /(^|\s)([/$／＄、¥￥])(\S*)$/.exec(draft);
  if (!catalog || !m) return none;
  const start = m.index + m[1].length;
  const lead = LEAD[m[2]];
  if (start > 0 && !(m[2] === "$" && catalog.skills.some((e) => e.name.startsWith("$")))) return none;
  const query = m[3].toLowerCase();
  const pool = [...catalog.commands, ...catalog.skills].filter((e) => e.name.startsWith(lead));
  const scored: [number, ChatCatalogEntry][] = [];
  for (const entry of pool) {
    const name = entry.name.slice(1).toLowerCase();
    const at = name.indexOf(query);
    if (query === "" || at === 0) scored.push([0, entry]);
    else if (at > 0) scored.push([1, entry]);
  }
  return { entries: scored.sort((x, y) => x[0] - y[0]).slice(0, 60).map(([, entry]) => entry), start };
}

/**
 * useMenuKeys gives a panel the same keys the `/` list has — ↑↓ move, ⏎ takes
 * the row, Esc closes — wherever the focus is (the composer, the `+` button,
 * the model search). It listens in the capture phase and stops the key, so
 * the composer never also sends on that Enter.
 */
function useMenuKeys(count: number, onTake: (index: number) => void, onClose: () => void) {
  const [pick, setPick] = useState(0);
  const live = useRef({ count, onTake, onClose, pick });
  live.current = { count, onTake, onClose, pick };
  useEffect(() => {
    if (pick >= count) setPick(0);
  }, [count, pick]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const { count: n, onTake: take, onClose: close, pick: at } = live.current;
      if (event.isComposing) return;
      if ((event.key === "ArrowDown" || event.key === "ArrowUp") && n > 0) {
        event.preventDefault();
        event.stopPropagation();
        setPick((current) => (current + (event.key === "ArrowDown" ? 1 : n - 1)) % n);
      } else if (event.key === "Enter" && !event.shiftKey && n > 0) {
        event.preventDefault();
        event.stopPropagation();
        take(at);
      } else if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        close();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);
  return [pick, setPick] as const;
}

/** SlashPanel is the `/` `$` list; the parent owns keyboard navigation. */
export function SlashPanel(props: {
  entries: ChatCatalogEntry[];
  pick: number;
  onPick: (entry: ChatCatalogEntry) => void;
  onHover: (index: number) => void;
}) {
  const pickedRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    pickedRef.current?.scrollIntoView({ block: "nearest" });
  }, [props.pick]);
  return (
    <div className="completions rich" role="listbox" aria-label="Commands and skills">
      {props.entries.map((entry, index) => (
        <button
          key={entry.name}
          type="button"
          role="option"
          aria-selected={index === props.pick}
          ref={index === props.pick ? pickedRef : undefined}
          className={index === props.pick ? "pick" : ""}
          onMouseMove={() => props.onHover(index)}
          onMouseDown={(event) => {
            event.preventDefault(); // keep the textarea focus
            props.onPick(entry);
          }}
        >
          <span className="nm mono">{entry.name}</span>
          <span className="dt">{entry.detail}</span>
          {KIND_LABEL[entry.kind] ? <span className="kd">{KIND_LABEL[entry.kind]}</span> : null}
        </button>
      ))}
    </div>
  );
}

/** PlusPanel is the mouse entry to the same lists plus the file picker. */
export function PlusPanel(props: {
  catalog: ChatCatalog | null;
  /** False on a host with no way to take a file (the phone, for now). */
  canAttach?: boolean;
  onAttach: () => void;
  onOpenList: (lead: string) => void;
  onClose: () => void;
}) {
  const skillLead = props.catalog?.skills[0]?.name[0];
  const items: { icon: "image" | "step" | "command"; name: string; detail: string; run: () => void }[] = [];
  if (props.canAttach !== false) {
    items.push({ icon: "image", name: "Attach file or image…", detail: "or paste / drop onto the message", run: props.onAttach });
  }
  if (skillLead) {
    items.push({
      icon: "step",
      name: "Skills",
      detail: `${props.catalog?.skills.length} available · type ${skillLead} to search`,
      run: () => props.onOpenList(skillLead),
    });
  }
  items.push({ icon: "command", name: "Commands", detail: "the agent's own / commands", run: () => props.onOpenList("/") });
  const [pick, setPick] = useMenuKeys(items.length, (index) => items[index]?.run(), props.onClose);
  return (
    <div className="completions rich" role="menu" aria-label="Add to message">
      {items.map((item, index) => (
        <button
          key={item.name}
          type="button"
          role="menuitem"
          className={index === pick ? "pick" : ""}
          onMouseMove={() => setPick(index)}
          onMouseDown={(e) => {
            e.preventDefault();
            item.run();
          }}
        >
          <Icon name={item.icon} size={14} />
          <span className="nm">{item.name}</span>
          <span className="dt">{item.detail}</span>
        </button>
      ))}
    </div>
  );
}

/** ModelPanel switches model: a typed list (pi, omp) and/or the agent's picker. */
export function ModelPanel(props: {
  catalog: ChatCatalog | null;
  current: string;
  busy: boolean;
  onSwitch: (id: string) => void;
  onPicker: () => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const models = props.catalog?.models ?? [];
  const shown = models.filter((m) => m.id.toLowerCase().includes(query.toLowerCase())).slice(0, 50);
  const hasPicker = Boolean(props.catalog?.modelPicker);
  const rows = (hasPicker ? 1 : 0) + shown.length;
  const take = (index: number) => {
    if (props.busy) return;
    if (hasPicker && index === 0) props.onPicker();
    else {
      const model = shown[index - (hasPicker ? 1 : 0)];
      if (model) props.onSwitch(model.id);
    }
  };
  const [pick, setPick] = useMenuKeys(rows, take, props.onClose);
  const offset = hasPicker ? 1 : 0;
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.querySelector(".pick")?.scrollIntoView({ block: "nearest" });
  }, [pick]);
  return (
    <div ref={box} className="completions rich" role="menu" aria-label="Switch model">
      {models.length > 8 ? (
        <input
          className="msearch"
          autoFocus
          placeholder="Search models"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setPick(0);
          }}
        />
      ) : null}
      {hasPicker ? (
        <button
          type="button"
          role="menuitem"
          disabled={props.busy}
          className={pick === 0 ? "pick" : ""}
          onMouseMove={() => setPick(0)}
          onMouseDown={(e) => {
            e.preventDefault();
            props.onPicker();
          }}
        >
          <Icon name="command" size={14} />
          <span className="nm">Choose in the agent's picker…</span>
          <span className="dt">opens its list here</span>
        </button>
      ) : null}
      {shown.map((model, index) => (
        <button
          key={model.id}
          type="button"
          role="menuitem"
          disabled={props.busy}
          className={`${index + offset === pick ? "pick" : ""}${props.current !== "" && model.id.endsWith(props.current) ? " on" : ""}`}
          onMouseMove={() => setPick(index + offset)}
          onMouseDown={(e) => {
            e.preventDefault();
            props.onSwitch(model.id);
          }}
        >
          <span className="nm mono">{model.id}</span>
        </button>
      ))}
      {models.length > 0 && shown.length === 0 ? <div className="none">No match</div> : null}
    </div>
  );
}

/** AttachChips lists the files riding with the draft; × removes one. */
export function AttachChips(props: {
  files: string[];
  previews: Map<string, string>;
  onRemove: (path: string) => void;
}) {
  if (props.files.length === 0) return null;
  return (
    <div className="achips">
      {props.files.map((path) => {
        const preview = props.previews.get(path);
        return (
          <span key={path} className="achip" title={path}>
            {preview ? (
              <img src={preview} alt="" />
            ) : (
              <Icon name={IMAGE_EXT.test(path) ? "image" : "read"} size={14} />
            )}
            <span className="an">{baseName(path)}</span>
            <button type="button" aria-label={`Remove ${baseName(path)}`} onClick={() => props.onRemove(path)}>
              ×
            </button>
          </span>
        );
      })}
    </div>
  );
}
