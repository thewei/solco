/**
 * Pure helpers of the answer form (no DOM, no state): quoting a passage of the Agent's message into the reply, which
 * choice question a number key belongs to, and when a message is long enough to fold.
 */

/** A passage as a Markdown quote: every line prefixed, blank lines kept as a bare marker. */
export function quoteBlock(text: string): string {
  return text
    .trim()
    .split(/\r?\n/)
    .map((line) => (line.trim() === "" ? ">" : `> ${line}`))
    .join("\n");
}

/** The reply text with the quote added after what is already typed (a blank line between, one after the quote). */
export function appendQuote(existing: string, passage: string): string {
  const quote = quoteBlock(passage);
  if (quote === ">" || quote === "") return existing;
  return existing.trim() === "" ? `${quote}\n\n` : `${existing.replace(/\s+$/, "")}\n\n${quote}\n\n`;
}

type Choice = { id: string; type: string; options?: string[] | null };

/**
 * The question the number key `n` (1-9) picks in: the one the person last touched when it has an n-th option, else the
 * first choice question that has one (the old rule, which a form with one choice question still gets).
 */
export function choiceFor<Q extends Choice>(questions: Q[], touched: string | null, n: number): Q | undefined {
  const has = (q: Q) => q.type !== "text" && (q.options?.length ?? 0) >= n;
  const own = touched ? questions.find((q) => q.id === touched) : undefined;
  return own && has(own) ? own : questions.find(has);
}

/** A message that would fill a phone screen: more than 420 characters or 8 lines. */
export function isLongText(text: string): boolean {
  return text.length > 420 || text.split("\n").length > 8;
}
