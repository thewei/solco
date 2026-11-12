/**
 * The answer form's work in progress — drafts, picked context and the outbox of answers that could not be sent —
 * kept in the WebView's own localStorage so a page that is torn down (a network switch on the phone, the host
 * rebuilding its WebView, a reload) comes back with it. Page-local: nothing here is sent anywhere, and an entry
 * nobody touched for `KEEP_MS` is dropped on load.
 */

export type Draft = { selected: string[]; custom: string };
export type Drafts = Record<string, Record<string, Draft>>;
export type Ref = { kind: string; name: string };
/** An answer the Host could not be reached for: frozen at the moment Send was pressed. */
export type Queued<R extends Ref = Ref> = { at: number; answers: Record<string, Draft>; refs: R[] };

export interface Persisted<R extends Ref = Ref> {
  drafts: Drafts;
  refs: Record<string, R[]>;
  outbox: Record<string, Queued<R>>;
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
  return { drafts: {}, refs: {}, outbox: {}, seen: {} };
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
    const queued = isObj(raw.outbox) ? raw.outbox[ask] : undefined;
    if (isObj(queued) && typeof queued.at === "number" && isObj(queued.answers)) {
      const answers: Record<string, Draft> = {};
      for (const [q, d] of Object.entries(queued.answers)) if (isDraft(d)) answers[q] = d;
      out.outbox[ask] = { at: queued.at, answers, refs: (Array.isArray(queued.refs) ? queued.refs.filter(isRef) : []) as R[] };
    }
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
  for (const ask of new Set([...Object.keys(drafts), ...Object.keys(refs), ...Object.keys(p.outbox)])) seen[ask] = p.seen[ask] ?? Date.now();
  try {
    store?.setItem(DRAFT_KEY, JSON.stringify({ drafts, refs, outbox: p.outbox, seen }));
  } catch {
    /* storage unavailable or full: the work stays in page memory */
  }
}

/** The bridge's marker for "the Host could not be reached" (the phone's controller prefixes it; the page strips it for display). */
export const OFFLINE_MARK = "offline: ";

export function isOffline(e: unknown): boolean {
  return (e instanceof Error ? e.message : String(e)).startsWith(OFFLINE_MARK);
}
