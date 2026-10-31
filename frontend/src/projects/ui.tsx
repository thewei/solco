import { useEffect, useRef, useState, type ReactNode } from "react";

/** Shared small pieces of the Projects page: status mark, menu, segmented tabs. */

export type Tone = "attention" | "ready" | "working" | "idle" | string;

/** One status mark for every task state: spinner = working, ring = idle, dot = the rest. */
export function Mark({ tone, size = 14 }: { tone: Tone; size?: number }) {
  if (tone === "working") return <span className="pj-mark spin" style={{ width: size, height: size }} aria-label="working" />;
  if (tone === "idle") return <span className="pj-mark ring" style={{ width: size, height: size }} aria-label="idle" />;
  return <span className={`pj-mark dot ${tone}`} style={{ width: size, height: size }} aria-label={tone} />;
}

export function Dots() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <circle cx="5" cy="12" r="1.7" />
      <circle cx="12" cy="12" r="1.7" />
      <circle cx="19" cy="12" r="1.7" />
    </svg>
  );
}

export interface MenuItem {
  label: string;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
}

/** A button that opens a small list; closes on outside click, Esc or a pick. */
export function Menu({ items, label = "More", icon }: { items: (MenuItem | "-")[]; label?: string; icon?: ReactNode }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", key);
    };
  }, [open]);
  return (
    <div className="pj-menu" ref={box}>
      <button type="button" className="pj-icon" aria-label={label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {icon ?? <Dots />}
      </button>
      {open ? (
        <div className="pj-pop" role="menu">
          {items.map((item, i) =>
            item === "-" ? (
              <div key={`s${i}`} className="pj-sep" />
            ) : (
              <button
                key={item.label}
                type="button"
                role="menuitem"
                className={item.danger ? "danger" : ""}
                disabled={item.disabled}
                onClick={() => {
                  setOpen(false);
                  item.onSelect();
                }}
              >
                {item.label}
              </button>
            ),
          )}
        </div>
      ) : null}
    </div>
  );
}

export interface TabSpec<T extends string> {
  id: T;
  label: string;
  count?: number;
}

/** The aside's tab strip (one face for project and task tabs). */
export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: TabSpec<T>[]; value: T; onChange: (id: T) => void }) {
  return (
    <div className="pj-tabs" role="tablist">
      {tabs.map((t) => (
        <button key={t.id} type="button" role="tab" aria-selected={value === t.id} className={value === t.id ? "on" : ""} onClick={() => onChange(t.id)}>
          {t.label}
          {t.count !== undefined ? <span className="n">{t.count}</span> : null}
        </button>
      ))}
    </div>
  );
}

export function Segmented<T extends string>({
  options, value, onChange, label,
}: { options: { id: T; label: string }[]; value: T; onChange: (id: T) => void; label: string }) {
  return (
    <div className="pj-seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.id} type="button" aria-pressed={value === o.id} className={value === o.id ? "on" : ""} onClick={() => onChange(o.id)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** A modal sheet over the page; Esc and the scrim close it. */
export function Sheet({ title, onClose, children, wide }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose]);
  return (
    <div className="pj-scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`pj-sheet${wide ? " wide" : ""}`} role="dialog" aria-label={title}>
        <h2>{title}</h2>
        {children}
      </div>
    </div>
  );
}
