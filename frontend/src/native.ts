import { useEffect, useState } from "react";

/**
 * Safari-style status pill: the href of the link under the pointer ("" when
 * none). Shared by the chat page and the Files preview.
 */
export function useLinkHint(): string {
  const [hint, setHint] = useState("");
  useEffect(() => {
    const link = (event: MouseEvent) => (event.target as HTMLElement | null)?.closest?.("a[href]");
    const over = (event: MouseEvent) => setHint(link(event)?.getAttribute("href") ?? "");
    const out = (event: MouseEvent) => {
      if (link(event)) setHint("");
    };
    document.addEventListener("mouseover", over);
    document.addEventListener("mouseout", out);
    return () => {
      document.removeEventListener("mouseover", over);
      document.removeEventListener("mouseout", out);
    };
  }, []);
  return hint;
}

/** Select everything inside `root` (the page's "Select All"). */
export function selectContents(root: Element | null | undefined) {
  const selection = window.getSelection();
  if (!root || !selection) return;
  const range = document.createRange();
  range.selectNodeContents(root);
  selection.removeAllRanges();
  selection.addRange(range);
}
