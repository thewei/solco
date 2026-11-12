/**
 * The answer form's work in progress — drafts and picked context — kept in the WebView's own localStorage so a page
 * that is torn down (a network switch on the phone, the host rebuilding its WebView, a reload) comes back with it.
 * Page-local: nothing here is sent anywhere, and an entry nobody touched for `KEEP_MS` is dropped on load. The answers
 * that could not be sent are not here: on the phone the app's native layer queues and sends them (the snapshot's
 * `queued` / `unsent`), whatever page is open.
 */

export type Draft = { selected: string[]; custom: string };
export type Drafts = Record<string, Record<string, Draft>>;
export type Ref = { kind: string; name: string };

export interface Persisted<R extends Ref = Ref> {
  drafts: Drafts;
  refs: Record<string, R[]>;
  /** askID → when the form last changed (ms): what pruning goes by. */
  seen: Record<string, number>;
}

export const DRAFT_KEY = "solco.feedback.work";
export const KEEP_MS = 3 * 24 * 3600_000;

type Store = Pick<Storage, "getItem" | "setItem">;

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const isDraft = (v: unknown): v is Draft => isObj(v) && Array.isArray(v.selected) && v.selected.every((s) => typeof s === "string") && typeof v.custom === "string";
const isRef = (v: unknown): v is Ref => isObj(v) && typeof v.kind === "string" && typeof v.name === "string";

export function emptyPersisted<R extends Ref = Ref>(): Persisted<R> {
  return { drafts: {}, refs: {}, seen: {} };
}

/** Reads the saved work; anything malformed or older than KEEP_MS is left out (never throws). */
export function loadPersisted<R extends Ref = Ref>(store: Store | undefined, now = Date.now()): Persisted<R> {
  const out = emptyPersisted<R>();
  let raw: unknown;
  try {
    raw = JSON.parse(store?.getItem(DRAFT_KEY) ?? "null");
  } catch {
    return out;
  }
  if (!isObj(raw) || !isObj(raw.seen)) return out;
  for (const [ask, at] of Object.entries(raw.seen)) {
    if (typeof at !== "number" || now - at > KEEP_MS) continue;
    out.seen[ask] = at;
    const drafts = isObj(raw.drafts) ? raw.drafts[ask] : undefined;
    if (isObj(drafts)) {
      const kept: Record<string, Draft> = {};
      for (const [q, d] of Object.entries(drafts)) if (isDraft(d)) kept[q] = d;
      out.drafts[ask] = kept;
    }
    const refs = isObj(raw.refs) ? raw.refs[ask] : undefined;
    if (Array.isArray(refs)) out.refs[ask] = refs.filter(isRef) as R[];
  }
  return out;
}

/** Writes the work (never throws: without storage it simply stays in page memory). Empty drafts are not written. */
export function savePersisted<R extends Ref>(store: Store | undefined, p: Persisted<R>): void {
  const drafts: Drafts = {};
  for (const [ask, qs] of Object.entries(p.drafts)) {
    const used = Object.entries(qs).filter(([, d]) => d.selected.length > 0 || d.custom !== "");
    if (used.length > 0) drafts[ask] = Object.fromEntries(used);
  }
  const refs: Record<string, R[]> = {};
  for (const [ask, list] of Object.entries(p.refs)) if (list.length > 0) refs[ask] = list;
  const seen: Record<string, number> = {};
  for (const ask of new Set([...Object.keys(drafts), ...Object.keys(refs)])) seen[ask] = p.seen[ask] ?? Date.now();
  try {
    store?.setItem(DRAFT_KEY, JSON.stringify({ drafts, refs, seen }));
  } catch {
    /* storage unavailable or full: the work stays in page memory */
  }
}

/** Forgets the work of Asks that are over (answered or ended; an expired one still takes a late answer). True when anything went. */
export function forgetClosed<R extends Ref>(p: Persisted<R>, asks: { id: string; state: string }[]): boolean {
  let changed = false;
  for (const a of asks) {
    if (a.state !== "answered" && a.state !== "ended") continue;
    if (a.id in p.drafts || a.id in p.refs || a.id in p.seen) changed = true;
    delete p.drafts[a.id];
    delete p.refs[a.id];
    delete p.seen[a.id];
  }
  return changed;
}

/** The phone controller's marker for "the Mac was not reachable, the phone kept the answer" (the page freezes the form). */
export const QUEUED_MARK = "queued: ";

export function isQueued(e: unknown): boolean {
  return (e instanceof Error ? e.message : String(e)).startsWith(QUEUED_MARK);
}
