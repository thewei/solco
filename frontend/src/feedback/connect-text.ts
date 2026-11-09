/**
 * The texts "Connect an Agent" hands out: the one place that knows how each client registers the feedback MCP server
 * and how an Agent must ask. The server is this app's own `solco-hostd feedback-mcp` (a local stdio process). Asking is
 * a blocking, long-lived call: the user needs minutes to hours to notice, read and answer, so every client must be set
 * up to hold the call open (Codex's 60 s default tool_timeout_sec would cut each question short) and an Agent whose
 * client cuts it anyway falls back to start + wait. Pure, so a test can pin the wording.
 */

export const FALLBACK_BIN = "/Applications/Solco.app/Contents/Helpers/solco-hostd";
export const SERVER_NAME = "solco-feedback";
/** One day, the server's own ceiling for a single question (internal/feedback/mcpshim clamps timeout_seconds to 24 h). */
export const TOOL_TIMEOUT_SEC = 86400;

export type Kind = "prompt" | "claude" | "codex" | "json";

export const TABS: { id: Kind; label: string; note: string }[] = [
  { id: "prompt", label: "Prompt", note: "Paste this into any Agent. It registers the server itself and keeps every question open until you answer." },
  { id: "claude", label: "Claude Code", note: "Run in a terminal. Claude Code already lets a call run for about a day." },
  { id: "codex", label: "Codex", note: "Add to `~/.codex/config.toml`. Keep `tool_timeout_sec`: Codex cuts a tool call at 60 s without it." },
  { id: "json", label: "JSON", note: "For Cursor, Claude Desktop and others: add to their `mcpServers`. Set the client's per-call tool timeout to its maximum if it has one." },
];

const quote = (bin: string) => `"${bin.replace(/(["\\$`])/g, "\\$1")}"`;
const tomlString = (s: string) => JSON.stringify(s);

export function claudeCommand(bin: string): string {
  return `claude mcp add --scope user ${SERVER_NAME} -- ${quote(bin)} feedback-mcp`;
}

export function codexToml(bin: string): string {
  return [`[mcp_servers.${SERVER_NAME}]`, `command = ${tomlString(bin)}`, `args = ["feedback-mcp"]`, "startup_timeout_sec = 30", `tool_timeout_sec = ${TOOL_TIMEOUT_SEC}`].join("\n");
}

export function mcpText(kind: Kind, binIn: string): string {
  const bin = binIn || FALLBACK_BIN;
  switch (kind) {
    case "claude":
      return claudeCommand(bin);
    case "codex":
      return `# ~/.codex/config.toml\n${codexToml(bin)}`;
    case "json":
      return JSON.stringify({ mcpServers: { [SERVER_NAME]: { command: bin, args: ["feedback-mcp"] } } }, null, 2);
    case "prompt":
      return [
        `Set up the "${SERVER_NAME}" MCP server, then use it to ask me questions instead of asking in this chat.`,
        "",
        "Server (a local stdio process that is already on this Mac; nothing to download or install):",
        `  name:    ${SERVER_NAME}`,
        `  command: ${bin}`,
        "  args:    feedback-mcp",
        "",
        "1. Register it in your own MCP configuration and change nothing else:",
        `   - Claude Code: ${claudeCommand(bin)}`,
        `   - Codex: add this to ~/.codex/config.toml. Keep tool_timeout_sec: Codex cuts every tool call at 60 s without it, and my answers take far longer.`,
        ...codexToml(bin)
          .split("\n")
          .map((l) => `       ${l}`),
        `   - Cursor, Claude Desktop and similar: add {"mcpServers": {"${SERVER_NAME}": {"command": "${bin}", "args": ["feedback-mcp"]}}} to their MCP config file. If the client lets you set a per-call tool timeout, set it to the maximum.`,
        "2. Reload or restart your MCP servers if your client needs it, then check that these tools are listed: interactive_feedback, interactive_feedback_start, interactive_feedback_wait, recall_decisions.",
        "3. How to ask: BLOCKING and long-lived. I am a person: I may need minutes or hours to notice a question, read it and answer. Call interactive_feedback and keep that one call open until it returns; the server keeps the connection alive and returns only when I answer. Do not poll, do not shorten timeout_seconds (the default is right), do not give up, and never carry on with an assumed answer. Always pass caller_info (who you are), project_root (the absolute path of the workspace you are in) and a short Markdown description with the context; use single or multiple choice questions where you can. Ask when you need a decision, are blocked, or are about to finish.",
        '4. Read the result: only status "answered" is an answer. "timeout" means I did not answer in time: ask again, unless ended_by_user is true (I closed it on purpose: stop asking about that). Take "late_answers" into account when present.',
        '5. If your client cuts a long call short (a tool timeout or a cancellation error), that is not an answer. Ask again with interactive_feedback_start, then call interactive_feedback_wait with the returned session_id again and again until the status is no longer "pending". Set wait_seconds below your client\'s per-call limit (for example 45 for a 60 s limit; the maximum is 300).',
        "6. Tell me when it is registered and which tools you can see.",
      ].join("\n");
  }
}
