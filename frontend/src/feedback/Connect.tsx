import { useEffect, useState } from "react";
import { Icon } from "../icons";
import { mcpText, TABS, type Kind } from "./connect-text";

/**
 * "Connect an Agent": how any other Agent gets the feedback MCP tool. The server is this app's own `solco-hostd
 * feedback-mcp` (a local stdio process, nothing to download); connect-text.ts formats its path for each client and
 * writes the one prompt that lets an Agent register it itself and ask in blocking, long-lived mode. The path is the
 * bridge's `mcpBin`; outside an app bundle (a dev run) the standard install path stands in. Desktop only: a phone has
 * no Mac path to hand out.
 */

export function ConnectDialog({ bin, onClose }: { bin: string; onClose: () => void }) {
  const [kind, setKind] = useState<Kind>("prompt");
  const [copied, setCopied] = useState("");
  const text = mcpText(kind, bin);
  const tab = TABS.find((t) => t.id === kind)!;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const copy = () => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(kind);
      window.setTimeout(() => setCopied((c) => (c === kind ? "" : c)), 1600);
    });
  };

  return (
    <div className="fb-scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="fb-dialog" role="dialog" aria-modal="true" aria-label="Connect an Agent">
        <header className="fb-dh">
          <div className="fb-dt">Connect an Agent</div>
          <button className="fb-x" aria-label="Close" onClick={onClose}>
            <Icon name="x" size={16} />
          </button>
        </header>
        <p className="fb-dn">
          Any Agent that can use MCP tools can ask you here. They all run the same local server, <code>solco-hostd feedback-mcp</code>.
        </p>
        <div className="fb-seg" role="tablist">
          {TABS.map((t) => (
            <button key={t.id} role="tab" aria-selected={kind === t.id} className={kind === t.id ? "on" : ""} onClick={() => setKind(t.id)}>
              {t.label}
            </button>
          ))}
        </div>
        <p className="fb-dn sub">{tab.note.split("`").map((p, i) => (i % 2 ? <code key={i}>{p}</code> : p))}</p>
        <pre className="fb-code">{text}</pre>
        <div className="fb-send">
          <span className="fb-hint">{bin ? "" : "Using the standard install path."}</span>
          <button className="fb-primary" onClick={copy}>
            {copied === kind ? "Copied" : kind === "prompt" ? "Copy prompt" : "Copy"}
          </button>
        </div>
      </div>
    </div>
  );
}
