import { useCallback, useEffect, useState, type ReactNode } from "react";
import { assistants, type AssistantsAgent, type MCPServerState, type PersonaView } from "../mygo";

/**
 * Work style (Assistants 3.1 U2): how this Assistant works. Three honest
 * cards — its instructions (versioned; an edit reaches Agents started after
 * it), the Agent/Model/Effort new starts use (beside the Agents that really
 * run), and the MCP servers it may use. MCP keeps four facts apart: found in
 * the person's own config, granted to this Assistant, handed to a launch
 * (bound), and — only after Check — confirmed connected by the Agent itself.
 */

type Act = (call: () => Promise<unknown>) => Promise<void>;

const rid = () => crypto.randomUUID();

function when(ms: number): string {
  return ms ? new Date(ms).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="as-field">
      <span>{label}</span>
      {children}
    </label>
  );
}

const SURFACE = (s: string) => (s === "coordinator" ? "Coordinator" : s.startsWith("group:") ? "Group agent" : s === "task" ? "Task agent" : s);

export function WorkStyle({ id, agents, act }: { id: string; agents: AssistantsAgent[]; act: Act }) {
  const [view, setView] = useState<PersonaView | null>(null);
  const [failed, setFailed] = useState("");
  const load = useCallback(() => {
    assistants
      .persona(id)
      .then((v) => {
        setView(v);
        setFailed("");
      })
      .catch((e) => setFailed(e instanceof Error ? e.message.replace(/^[a-z_]+: /, "") : String(e)));
  }, [id]);
  useEffect(() => {
    setView(null);
    load();
    const timer = window.setInterval(() => !document.hidden && load(), 6000);
    return () => window.clearInterval(timer);
  }, [load]);
  const run: Act = useCallback(async (call) => act(call).then(load), [act, load]);
  if (!view) return <p className="as-meta">{failed || "Loading…"}</p>;
  return (
    <>
      <Instructions key={`${id}:${view.instructions.revision}`} id={id} view={view} run={run} />
      <Runtime key={`rt:${id}:${view.runtime.profile.revision}`} id={id} view={view} agents={agents} run={run} />
      <MCP id={id} view={view} run={run} />
    </>
  );
}

function Instructions({ id, view, run }: { id: string; view: PersonaView; run: Act }) {
  const ins = view.instructions;
  const [text, setText] = useState(ins.body);
  const dirty = text.trim() !== ins.body.trim();
  const behind = ins.applied.filter((a) => a.revision < ins.revision);
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>Instructions</strong>
        <span className="as-chip">rev {ins.revision}</span>
      </div>
      <p className="as-meta">How this Assistant works and talks. Agents started after you save use the new text; one already running keeps the version it started with.</p>
      <Field label="Working style">
        <textarea rows={9} value={text} maxLength={8192} onChange={(e) => setText(e.target.value)} />
      </Field>
      <div className="as-row end">
        {dirty ? (
          <button type="button" className="as-link" onClick={() => setText(ins.body)}>
            Discard
          </button>
        ) : null}
        <button type="button" className="as-btn primary" disabled={!dirty || text.trim() === ""} onClick={() => run(() => assistants.saveInstructions(rid(), id, text.trim(), ins.revision))}>
          Save
        </button>
      </div>
      {behind.length > 0 ? (
        <p className="as-meta">Running with an older version until it restarts: {behind.map((a) => `${SURFACE(a.surface)} (rev ${a.revision})`).join(", ")}.</p>
      ) : null}
      {ins.history.length > 1 ? (
        <details>
          <summary className="as-meta">History</summary>
          {ins.history.map((h) => (
            <div key={h.revision} className="as-row" title={h.body}>
              <span className="as-meta">
                rev {h.revision} · {h.source} · {when(h.createdAt)}
              </span>
              <span className="as-grow" />
              {h.revision === ins.revision ? (
                <span className="as-chip active">current</span>
              ) : (
                <button type="button" className="as-link" onClick={() => run(() => assistants.restoreInstructions(rid(), id, h.revision, ins.revision))}>
                  Restore
                </button>
              )}
            </div>
          ))}
        </details>
      ) : null}
    </section>
  );
}

function Runtime({ id, view, agents, run }: { id: string; view: PersonaView; agents: AssistantsAgent[]; run: Act }) {
  const p = view.runtime.profile;
  const [agent, setAgent] = useState(p.agent);
  const [model, setModel] = useState(p.model);
  const [effort, setEffort] = useState(p.effort);
  const effective = agent || "claude";
  const supported = effective === "claude";
  const dirty = agent !== p.agent || model !== p.model || effort !== p.effort;
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>Agent</strong>
      </div>
      <p className="as-meta">What new conversations and tasks start on. A running Agent is never restarted by a change here.</p>
      <Field label="Agent">
        <select value={agent} onChange={(e) => setAgent(e.target.value)}>
          <option value="">Default</option>
          {agents.map((x) => (
            <option key={x.id} value={x.id}>
              {x.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Model (optional)">
        <input value={model} maxLength={120} onChange={(e) => setModel(e.target.value)} placeholder="Agent's own default" />
      </Field>
      <Field label="Thinking effort (optional)">
        <input value={supported ? effort : ""} disabled={!supported} maxLength={24} onChange={(e) => setEffort(e.target.value.trim())} placeholder={supported ? "e.g. low, medium, high" : "Not available for this Agent"} />
      </Field>
      <div className="as-row end">
        <button type="button" className="as-btn primary" disabled={!dirty} onClick={() => run(() => assistants.saveRuntime(rid(), id, agent, model.trim(), supported ? effort : "", p.revision))}>
          Save
        </button>
      </div>
      <strong className="as-meta">Running now</strong>
      {view.runtime.running.length === 0 ? <p className="as-meta">No Agent is running for this Assistant.</p> : null}
      {view.runtime.running.map((r) => (
        <div key={r.taskId} className="as-row">
          <span>{SURFACE(r.surface)}</span>
          <span className="as-meta">
            {r.agent}
            {r.model ? ` · ${r.model}` : ""}
          </span>
          <span className="as-grow" />
          <span className={`as-chip ${r.state}`}>{r.state}</span>
        </div>
      ))}
    </section>
  );
}

function MCP({ id, view, run }: { id: string; view: PersonaView; run: Act }) {
  const mcp = view.mcp;
  const [checking, setChecking] = useState(false);
  const granted = mcp.servers.filter((s) => s.granted).length;
  return (
    <section className="as-card">
      <div className="as-row">
        <strong>MCP servers</strong>
        <span className="as-chip">{granted} allowed</span>
      </div>
      <p className="as-meta">The servers from your own Claude and Codex setup. Allowed servers are the only ones this Assistant&apos;s Claude Code agents load. Changes apply to the next start.</p>
      {!mcp.supported ? <p className="as-meta">{mcp.unsupported}</p> : null}
      {mcp.servers.length === 0 ? <p className="as-meta">No MCP servers found in your configuration.</p> : null}
      {mcp.servers.map((s) => (
        <Server key={s.id} server={s} id={id} disabled={!mcp.supported} run={run} />
      ))}
      <div className="as-row end">
        <button
          type="button"
          className="as-btn"
          disabled={!mcp.supported || granted === 0 || checking}
          onClick={() => {
            setChecking(true);
            void run(() => assistants.verifyMCP(id)).finally(() => setChecking(false));
          }}
        >
          {checking ? "Checking…" : "Check connections"}
        </button>
      </div>
      <p className="as-meta">Check starts a headless Claude Code agent and reads which servers it connects to; it sends no prompt to a model.</p>
    </section>
  );
}

function Server({ server: s, id, disabled, run }: { server: MCPServerState; id: string; disabled: boolean; run: Act }) {
  const where = s.agents.includes("claude") ? "" : "Codex only";
  return (
    <div className="as-mcp">
      <label className="as-row">
        <input type="checkbox" checked={s.granted} disabled={disabled || (!s.granted && where !== "")} onChange={(e) => run(() => assistants.grantMCP(rid(), id, s.id, e.target.checked))} />
        <strong>{s.name}</strong>
        <span className="as-meta">{s.transport ? `${s.transport}${s.summary ? ` · ${s.summary}` : ""}` : ""}</span>
      </label>
      <div className="as-row as-mcp-state">
        {!s.configured ? <span className="as-chip failed">not in your config</span> : null}
        {where ? <span className="as-chip">{where}</span> : null}
        {s.bound.length > 0 ? <span className="as-chip running">handed to {s.bound.map(SURFACE).join(", ")}</span> : null}
        {s.verified ? <span className={`as-chip ${s.verified === "connected" ? "active" : "failed"}`}>{s.verified === "connected" ? "connected" : s.verified}</span> : null}
      </div>
    </div>
  );
}
