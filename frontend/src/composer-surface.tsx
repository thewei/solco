import { forwardRef, useEffect, type ReactNode, type RefObject, type TextareaHTMLAttributes } from "react";

/**
 * One Chat composer presentation for a live Agent and a not-yet-started
 * Assistant. Transport, authorization, attachments, context and model choices
 * belong to the caller, not to this visual component.
 */
export function ChatComposerBox({ rest = false, className = "", children }: { rest?: boolean; className?: string; children: ReactNode }) {
  return <div className={["cmp", rest ? "rest" : "", className].filter(Boolean).join(" ")}>{children}</div>;
}

export function ChatComposerToolbar({ children }: { children: ReactNode }) {
  return <div className="cbar">{children}</div>;
}

export const ChatComposerTextarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  function ChatComposerTextarea({ rows = 1, spellCheck = true, ...props }, ref) {
    return <textarea {...props} ref={ref} rows={rows} aria-label={props["aria-label"] ?? "Chat prompt"} spellCheck={spellCheck} />;
  },
);

/** Same text growth rule for pre-launch and live Chat. */
export function useComposerAutoSize(ref: RefObject<HTMLTextAreaElement>, value: string) {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = String(Math.min(el.scrollHeight, Math.floor(window.innerHeight * 0.4))) + "px";
  }, [ref, value]);
}
