import { useEffect, useState } from "react";
import { Icon } from "../icons";

/**
 * "Connect an Agent": how any other Agent gets the feedback MCP tool. The server is this app's own `solco-hostd
 * feedback-mcp` (a local stdio process, nothing to download), so the page only formats its path for each client and
 * writes one prompt that lets an Agent register it itself. The path is the bridge's `mcpBin`; outside an app bundle
 * (a dev run) the standard install path stands in. Desktop only: a phone has no Mac path to hand out.
 */

const FALLBACK_BIN = "/Applications/Solco.app/Contents/Helpers/solco-hostd";
const NAME = "solco-feedback";

type Kind = "prompt" | "claude" | "codex" | "json";

const TABS: { id: Kind; label: string; note: string }[] = [
  { id: "prompt", label: "Prompt", note: "Paste this into any Agent. It registers the server itself and starts asking you here." },
  { id: "claude", label: "Claude Code", note: "Run in a terminal; the server is then available in every project." },
  { id: "codex", label: "Codex", note: "Run in a terminal." },
  { id: "json", label: "JSON", note: "For Cursor, Claude Desktop and other clients: add it to their `mcpServers`." },
];

const quote = (bin: string) => `"${bin.replace(/(["\\$`])/g, "\\$1")}"`;

export function mcpText(kind: Kind, binIn: string): string {
  const bin = binIn || FALLBACK_BIN;
  switch (kind) {
    case "claude":
      return `claude mcp add --scope user ${NAME} -- ${quote(bin)} feedback-mcp`;
    case "codex":
      return `codex mcp add ${NAME} -- ${quote(bin)} feedback-mcp`;
    case "json":
      return JSON.stringify({ mcpServers: { [NAME]: { command: bin, args: ["feedback-mcp"] } } }, null, 2);
    case "prompt":
      return [
        `Set up the "${NAME}" MCP server so you can ask me questions through Solco instead of in this chat.`,
        "",
        "It is a local stdio MCP server that is already on this Mac; nothing to download or install:",
        `  name:    ${NAME}`,
        `  command: ${bin}`,
        "  args:    feedback-mcp",
        "",
        "1. Register it in your own MCP configuration the way your client does it, and change nothing else. For example:",
        `   - Claude Code: claude mcp add --scope user ${NAME} -- ${quote(bin)} feedback-mcp`,
        `   - Codex: codex mcp add ${NAME} -- ${quote(bin)} feedback-mcp`,
        `   - Cursor, Claude Desktop and similar: add {"mcpServers": {"${NAME}": {"command": "${bin}", "args": ["feedback-mcp"]}}} to their MCP config file.`,
        "2. Reload or restart your MCP servers if your client needs it, then check that these tools are listed: interactive_feedback, interactive_feedback_start, interactive_feedback_wait, recall_decisions.",
        "3. From now on, when you need a decision, are blocked, or are about to finish, ask me by calling interactive_feedback instead of writing the question in chat. Always pass caller_info (who you are), project_root (the absolute path of the workspace you are in) and a short Markdown description with the context; use single or multiple choice questions where you can. If the call times out, use interactive_feedback_start and then loop interactive_feedback_wait with the returned session_id.",
        "4. Tell me when it is registered and which tools you can see.",
      ].join("\n");
  }
}

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
