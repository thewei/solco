import { expect, test } from "bun:test";
import { FALLBACK_BIN, mcpText, TOOL_TIMEOUT_SEC } from "../src/feedback/connect-text";

const bin = "/Users/me/Applications/Solco.app/Contents/Helpers/solco-hostd";

test("the prompt asks for a blocking, long-lived call with the start/wait fallback", () => {
  const p = mcpText("prompt", bin);
  expect(p).toContain(bin);
  expect(p).toContain("BLOCKING and long-lived");
  expect(p).toContain("keep that one call open");
  expect(p).toContain("never carry on with an assumed answer");
  expect(p).toContain("interactive_feedback_start");
  expect(p).toContain("interactive_feedback_wait");
  expect(p).toContain(`tool_timeout_sec = ${TOOL_TIMEOUT_SEC}`);
});

test("Codex gets a long tool timeout, since its default of 60 s cuts every question", () => {
  const t = mcpText("codex", bin);
  expect(t).toContain("[mcp_servers.solco-feedback]");
  expect(t).toContain(`command = "${bin}"`);
  expect(t).toContain("tool_timeout_sec = 86400");
});

test("a path with spaces or quotes stays one argument", () => {
  const odd = '/Users/me x/"x"/solco-hostd';
  expect(mcpText("claude", odd)).toBe('claude mcp add --scope user solco-feedback -- "/Users/me x/\\"x\\"/solco-hostd" feedback-mcp');
  expect(JSON.parse(mcpText("json", odd)).mcpServers["solco-feedback"].command).toBe(odd);
});

test("outside an app bundle the standard install path stands in", () => {
  expect(mcpText("json", "")).toContain(FALLBACK_BIN);
});
