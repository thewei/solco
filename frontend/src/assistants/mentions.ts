/**
 * Who a Group message wakes. The core (`checkMentions` in personalagent) is the
 * authority; this mirrors its token rules so the page asks for what the core
 * will accept:
 *   - each `@` belongs to the longest member name that follows it, and a name
 *     ending in an ASCII word character must end where the word ends (so
 *     `@Bobby` never reads as `@Bo`; CJK names need no space after them);
 *   - members that share a name are told apart only by what the person picked
 *     from the popover, so one `@Ada` token wakes at most one of two Adas.
 */
import type { GroupMember } from "../mygo";

const ascii = (c: string) => /^[A-Za-z0-9_]$/.test(c);

/** The member names standing as `@Name` tokens in the text, in order. */
export function mentionNames(text: string, members: GroupMember[]): string[] {
  const out: string[] = [];
  for (let i = 0; i < text.length; i++) {
    if (text[i] !== "@") continue;
    if (i > 0 && (ascii(text[i - 1]) || text[i - 1] === "@")) continue;
    const rest = text.slice(i + 1);
    let best = "";
    for (const m of members) {
      if (!m.name || !rest.startsWith(m.name) || m.name.length <= best.length) continue;
      const last = m.name[m.name.length - 1];
      const next = rest[m.name.length];
      if (ascii(last) && next !== undefined && ascii(next)) continue;
      best = m.name;
    }
    if (best) {
      out.push(best);
      i += best.length;
    }
  }
  return out;
}

export interface Resolved {
  ids: string[];
  /** Names that stand for more than one member and were not picked apart. */
  ambiguous: string[];
}

/**
 * The ids a message wakes. `picks` are the members the person chose from the
 * popover (in order); they decide between members that share a name.
 */
export function resolveMentions(text: string, members: GroupMember[], picks: GroupMember[]): Resolved {
  const ids: string[] = [];
  const ambiguous: string[] = [];
  const taken = new Set<string>();
  const left = [...picks];
  for (const name of mentionNames(text, members)) {
    const same = members.filter((m) => m.name === name);
    let chosen: GroupMember | undefined;
    if (same.length === 1) chosen = same[0];
    else {
      const at = left.findIndex((p) => p.name === name && !taken.has(p.assistantId));
      if (at >= 0) chosen = left.splice(at, 1)[0];
    }
    if (!chosen) {
      if (!ambiguous.includes(name)) ambiguous.push(name);
      continue;
    }
    if (!taken.has(chosen.assistantId)) {
      taken.add(chosen.assistantId);
      ids.push(chosen.assistantId);
    }
  }
  return { ids, ambiguous };
}
