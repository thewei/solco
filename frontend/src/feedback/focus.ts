/**
 * Whether focusing this element raises a soft keyboard: a text field, not a button. The answer form scrolls itself
 * above the phone keyboard only for these, so choosing an option never moves the page.
 */
export function takesKeyboard(el: { tagName?: string } | null | undefined): boolean {
  const tag = el?.tagName;
  return tag === "TEXTAREA" || tag === "INPUT";
}
