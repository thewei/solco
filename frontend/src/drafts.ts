/**
 * Per-conversation composer state (the draft text and the files riding with
 * it) in the page's local storage, so a pane switch, a page reload, the app
 * restarting or the phone's WebView being recycled all return to what was
 * typed. An index of last-write times lets entries untouched for two weeks
 * go; an emptied draft removes its entry. Session storage is only read as the
 * fallback an earlier build wrote.
 */

const INDEX = "solco:chat-drafts";
const TTL_MS = 14 * 24 * 3600 * 1000;

function readIndex(): Record<string, number> {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(INDEX) ?? "{}");
    return parsed && typeof parsed === "object" ? (parsed as Record<string, number>) : {};
  } catch {
    return {};
  }
}

function touch(key: string, keep: boolean) {
  const index = readIndex();
  if (keep) index[key] = Date.now();
  else delete index[key];
  localStorage.setItem(INDEX, JSON.stringify(index));
}

export function loadStored(key: string, empty: string): string {
  try {
    return localStorage.getItem(key) ?? sessionStorage.getItem(key) ?? empty;
  } catch {
    return empty;
  }
}

export function saveStored(key: string, value: string, empty: string) {
  try {
    if (value === empty) {
      localStorage.removeItem(key);
      sessionStorage.removeItem(key);
      touch(key, false);
    } else {
      localStorage.setItem(key, value);
      touch(key, true);
    }
  } catch {
    // storage unavailable: the draft just does not outlive the page
  }
}

/** Drops entries untouched for two weeks; runs once per page load. */
export function pruneStored() {
  try {
    const index = readIndex();
    const cutoff = Date.now() - TTL_MS;
    let changed = false;
    for (const [key, at] of Object.entries(index)) {
      if (typeof at === "number" && at >= cutoff) continue;
      localStorage.removeItem(key);
      delete index[key];
      changed = true;
    }
    if (changed) localStorage.setItem(INDEX, JSON.stringify(index));
  } catch {
    // nothing to prune
  }
}

pruneStored();
