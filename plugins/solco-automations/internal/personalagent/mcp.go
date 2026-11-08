package personalagent

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Per-Assistant MCP (3.1 U2). The servers are the person's own — read from the
// same files Claude Code and Codex read; Solco never installs, edits or hosts
// one. Per Assistant the person grants whole servers (a tool-level allowlist is
// not enforceable through a launch flag, so none is offered). Binding is only
// real where an Agent can be told which servers to load: Claude Code takes
// `--mcp-config <file> --strict-mcp-config`, so what its agent loads is exactly
// the granted servers. Every other Agent reports itself unsupported rather than
// pretending. The four states are kept apart on purpose:
//
//	configured – the server exists in a config file the person owns
//	granted    – the person allowed this Assistant to use it
//	bound      – the last launch of a surface was handed it
//	verified   – a headless probe saw the Agent connect to it
//
// "Bound" never claims "works": only a probe's own answer is verified.

const mcpProbeSurface = "probe"

var mcpIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// MCPServer is one configured server as the wire shows it: names, transport and
// the program or host only — never arguments, environment, headers or a URL
// path, which is where credentials live.
type MCPServer struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Summary   string   `json:"summary"`
	Sources   []string `json:"sources"`
	Agents    []string `json:"agents"`
}

type mcpRaw struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	URL     string `json:"url"`
}

// discoverMCP reads the person's MCP servers. raw holds the Claude-format
// definitions (needed to write a bound config) and never leaves the process.
func discoverMCP(home string) ([]MCPServer, map[string]json.RawMessage) {
	servers := map[string]*MCPServer{}
	raw := map[string]json.RawMessage{}
	add := func(source, agent string, name string, r mcpRaw, def json.RawMessage) {
		if !mcpIDRe.MatchString(name) {
			return
		}
		key := name
		sv, ok := servers[key]
		if !ok {
			transport := r.Type
			if transport == "" {
				transport = "stdio"
				if r.URL != "" && r.Command == "" {
					transport = "http"
				}
			}
			summary := ""
			switch transport {
			case "stdio":
				summary = filepath.Base(r.Command)
				if r.Command == "" {
					summary = ""
				}
			default:
				if u, err := url.Parse(r.URL); err == nil {
					summary = u.Host
				}
			}
			sv = &MCPServer{ID: name, Name: name, Transport: transport, Summary: summary}
			servers[key] = sv
		}
		sv.Sources = appendUnique(sv.Sources, source)
		sv.Agents = appendUnique(sv.Agents, agent)
		if def != nil {
			if _, have := raw[name]; !have {
				raw[name] = def
			}
		}
	}
	for _, f := range []struct{ file, source string }{
		{filepath.Join(home, ".claude.json"), "claude"},
		{filepath.Join(home, ".mcp.json"), "mcp.json"},
	} {
		b, err := os.ReadFile(f.file)
		if err != nil {
			continue
		}
		var doc struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		for name, def := range doc.MCPServers {
			var r mcpRaw
			if json.Unmarshal(def, &r) != nil {
				continue
			}
			add(f.source, "claude", name, r, def)
		}
	}
	// Codex keeps its servers in TOML; only names and the program/host are read
	// (a plain scan of the table headers — Codex's file is never written).
	if b, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
		cur := ""
		var r mcpRaw
		flush := func() {
			if cur != "" {
				add("codex", "codex", cur, r, nil)
			}
			cur, r = "", mcpRaw{}
		}
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "[") {
				flush()
				name := strings.Trim(line, "[] ")
				if rest, ok := strings.CutPrefix(name, "mcp_servers."); ok && !strings.Contains(rest, ".") {
					cur = strings.Trim(rest, `"`)
				}
				continue
			}
			if cur == "" {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.TrimSpace(k) {
				case "command":
					r.Command = v
				case "url":
					r.URL = v
				}
			}
		}
		flush()
	}
	out := make([]MCPServer, 0, len(servers))
	for _, sv := range servers {
		sort.Strings(sv.Sources)
		sort.Strings(sv.Agents)
		out = append(out, *sv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, raw
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// MCPServerState is one server's four states for one Assistant.
type MCPServerState struct {
	MCPServer
	Configured bool     `json:"configured"`
	Granted    bool     `json:"granted"`
	Bound      []string `json:"bound"`
	// Verified is the probe's own word ("connected", "failed", …); empty =
	// never probed (or not part of the last probe).
	Verified   string `json:"verified"`
	VerifiedAt int64  `json:"verifiedAt"`
}

// MCPBinding is what one surface's last launch was handed.
type MCPBinding struct {
	Surface     string   `json:"surface"`
	Agent       string   `json:"agent"`
	Servers     []string `json:"servers"`
	Strict      bool     `json:"strict"`
	Unsupported string   `json:"unsupported,omitempty"`
	BoundAt     int64    `json:"boundAt"`
}

// MCPView is an Assistant's MCP picture.
type MCPView struct {
	AssistantID string `json:"assistantId"`
	// Agent is the Agent kind new launches use (the runtime profile's).
	Agent       string           `json:"agent"`
	Supported   bool             `json:"supported"`
	Unsupported string           `json:"unsupported,omitempty"`
	Servers     []MCPServerState `json:"servers"`
	Bindings    []MCPBinding     `json:"bindings"`
}

const mcpUnsupportedReason = "This Agent takes its MCP servers from its own configuration; Solco does not rewrite it, so grants cannot be enforced for it."

func mcpSupported(agent string) (bool, string) {
	if agent == "" || agent == "claude" {
		return true, ""
	}
	return false, mcpUnsupportedReason
}

func mcpHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// GetMCP reads the Assistant's MCP picture.
func (s *Store) GetMCP(assistantID string) (MCPView, error) {
	if _, err := s.GetAssistant(assistantID); err != nil {
		return MCPView{}, err
	}
	agent := s.profileOf(s.db, assistantID).Agent
	if agent == "" {
		agent = DefaultCoordinatorAgent
	}
	ok, why := mcpSupported(agent)
	view := MCPView{AssistantID: assistantID, Agent: agent, Supported: ok, Unsupported: why, Servers: []MCPServerState{}, Bindings: []MCPBinding{}}

	registry, _ := discoverMCP(mcpHome())
	have := map[string]MCPServer{}
	for _, sv := range registry {
		have[sv.ID] = sv
	}
	granted := map[string]bool{}
	rows, err := s.db.Query(`SELECT server_id FROM mcp_grants WHERE assistant_id=?`, assistantID)
	if err != nil {
		return MCPView{}, wrapBusy(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return MCPView{}, err
		}
		granted[id] = true
	}
	_ = rows.Close()

	bound := map[string][]string{}
	verified := map[string]string{}
	var verifiedAt int64
	brows, err := s.db.Query(`SELECT surface,agent_kind,servers,strict,unsupported,bound_at,verified_state,verified_at FROM mcp_bindings WHERE assistant_id=? ORDER BY surface`, assistantID)
	if err != nil {
		return MCPView{}, wrapBusy(err)
	}
	for brows.Next() {
		var b MCPBinding
		var servers, vstate string
		var strict int
		var vat int64
		if err := brows.Scan(&b.Surface, &b.Agent, &servers, &strict, &b.Unsupported, &b.BoundAt, &vstate, &vat); err != nil {
			_ = brows.Close()
			return MCPView{}, err
		}
		b.Strict = strict == 1
		_ = json.Unmarshal([]byte(servers), &b.Servers)
		if b.Servers == nil {
			b.Servers = []string{}
		}
		if b.Surface == mcpProbeSurface {
			if vstate != "" {
				var ps []MCPProbeStatus
				if json.Unmarshal([]byte(vstate), &ps) == nil {
					for _, p := range ps {
						verified[p.Name] = p.Status
					}
					verifiedAt = vat
				}
			}
			continue
		}
		for _, id := range b.Servers {
			bound[id] = append(bound[id], b.Surface)
		}
		view.Bindings = append(view.Bindings, b)
	}
	_ = brows.Close()

	ids := map[string]bool{}
	for id := range have {
		ids[id] = true
	}
	for id := range granted {
		ids[id] = true
	}
	order := make([]string, 0, len(ids))
	for id := range ids {
		order = append(order, id)
	}
	sort.Strings(order)
	for _, id := range order {
		sv, configured := have[id]
		if !configured {
			sv = MCPServer{ID: id, Name: id, Transport: "", Sources: []string{}, Agents: []string{}}
		}
		if sv.Sources == nil {
			sv.Sources = []string{}
		}
		st := MCPServerState{MCPServer: sv, Configured: configured, Granted: granted[id], Bound: bound[id]}
		if st.Bound == nil {
			st.Bound = []string{}
		}
		if granted[id] {
			st.Verified = verified[id]
			if st.Verified != "" {
				st.VerifiedAt = verifiedAt
			}
		}
		view.Servers = append(view.Servers, st)
	}
	return view, nil
}

// SetMCPGrant allows or withdraws one server for the Assistant. It affects the
// next launch: nothing already running is touched.
func (s *Store) SetMCPGrant(requestID, assistantID, serverID string, granted bool) (MCPView, bool, error) {
	if !mcpIDRe.MatchString(serverID) {
		return MCPView{}, false, fmt.Errorf("%w: server id", ErrInvalid)
	}
	payload := map[string]any{"a": assistantID, "server": serverID, "granted": granted}
	_, replayed, err := execute(s, requestID, "assistant.mcp.grant", payload, func(tx *sql.Tx) (bool, error) {
		if _, err := getAssistantTx(tx, assistantID); err != nil {
			return false, err
		}
		if granted {
			if _, err := tx.Exec(`INSERT INTO mcp_grants(assistant_id,server_id,revision,updated_at) VALUES(?,?,1,?)
				ON CONFLICT(assistant_id,server_id) DO NOTHING`, assistantID, serverID, s.nowMS()); err != nil {
				return false, err
			}
		} else if _, err := tx.Exec(`DELETE FROM mcp_grants WHERE assistant_id=? AND server_id=?`, assistantID, serverID); err != nil {
			return false, err
		}
		return granted, nil
	})
	if err != nil {
		return MCPView{}, false, err
	}
	v, err := s.GetMCP(assistantID)
	return v, replayed, err
}

func (s *Store) mcpDir() string { return filepath.Join(filepath.Dir(s.path), "assistants-mcp") }

// bindMCP writes the Assistant's granted servers into its own 0600 config file
// and returns the launch arguments for an Agent of this kind, recording what
// that surface was handed. For Claude Code the arguments make its MCP world
// exactly the granted servers (none when nothing is granted); for any other
// Agent nothing is passed and the binding says why.
func (s *Store) bindMCP(assistantID, surface, agent string) []string {
	args, bound, strict, unsupported := s.prepareMCP(assistantID, agent)
	blob, _ := json.Marshal(bound)
	_, _ = s.db.Exec(`INSERT INTO mcp_bindings(assistant_id,surface,agent_kind,servers,strict,unsupported,bound_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(assistant_id,surface) DO UPDATE SET agent_kind=excluded.agent_kind, servers=excluded.servers, strict=excluded.strict,
		unsupported=excluded.unsupported, bound_at=excluded.bound_at`,
		assistantID, surface, agent, string(blob), strict, unsupported, s.nowMS())
	return args
}

// prepareMCP writes the config file and returns the arguments, the servers
// actually handed over, strictness and the unsupported reason. It records nothing.
func (s *Store) prepareMCP(assistantID, agent string) (args []string, bound []string, strict int, unsupported string) {
	if agent == "" {
		agent = DefaultCoordinatorAgent
	}
	granted := []string{}
	if rows, err := s.db.Query(`SELECT server_id FROM mcp_grants WHERE assistant_id=? ORDER BY server_id`, assistantID); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				granted = append(granted, id)
			}
		}
		_ = rows.Close()
	}
	bound = []string{}
	if ok, why := mcpSupported(agent); !ok {
		unsupported = why
	} else {
		_, raw := discoverMCP(mcpHome())
		doc := map[string]json.RawMessage{}
		for _, id := range granted {
			if def, found := raw[id]; found {
				doc[id] = def
				bound = append(bound, id)
			}
		}
		if path, err := s.writeMCPConfig(assistantID, doc); err != nil {
			unsupported = "The MCP configuration could not be written, so no MCP servers are bound."
			bound = []string{}
		} else {
			args = []string{"--strict-mcp-config", "--mcp-config", path}
			strict = 1
		}
	}
	return args, bound, strict, unsupported
}

func (s *Store) writeMCPConfig(assistantID string, servers map[string]json.RawMessage) (string, error) {
	dir := s.mcpDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, assistantID+".json")
	tmp, err := os.CreateTemp(dir, ".mcp-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// MCPProbeStatus is one server's state as the Agent itself reported it.
type MCPProbeStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// MCPProber starts an Agent headless with the bound arguments and returns the
// MCP servers' statuses it reports at startup. A test seam; the default runs
// Claude Code and stops it before it makes a model request.
var MCPProber = probeClaudeMCP

func probeClaudeMCP(ctx context.Context, args []string) ([]MCPProbeStatus, error) {
	bin := ""
	if p, err := exec.LookPath("claude"); err == nil {
		bin = p
	}
	if bin == "" {
		home := mcpHome()
		for _, c := range []string{filepath.Join(home, ".local", "bin", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				bin = c
				break
			}
		}
	}
	if bin == "" {
		return nil, errors.New("claude is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	full := append([]string{"-p", "ok", "--output-format", "stream-json", "--verbose", "--max-turns", "1"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = os.TempDir()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var m struct {
			Type       string           `json:"type"`
			Subtype    string           `json:"subtype"`
			MCPServers []MCPProbeStatus `json:"mcp_servers"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Type == "system" && m.Subtype == "init" {
			return m.MCPServers, nil
		}
	}
	if ctx.Err() != nil {
		return nil, errors.New("the probe timed out")
	}
	return nil, errors.New("the Agent exited before reporting its MCP servers")
}

// VerifyMCP probes what an Agent of the Assistant's kind would load right now
// (the same arguments a launch gets) and records the Agent's own per-server
// status. It starts no pane and sends no prompt to a model.
func (s *Store) VerifyMCP(ctx context.Context, assistantID string) (MCPView, error) {
	if _, err := s.GetAssistant(assistantID); err != nil {
		return MCPView{}, err
	}
	agent := s.profileOf(s.db, assistantID).Agent
	if agent == "" {
		agent = DefaultCoordinatorAgent
	}
	if ok, why := mcpSupported(agent); !ok {
		return MCPView{}, &CodedError{Code: "mcp_unsupported", Message: why}
	}
	args, _, _, _ := s.prepareMCP(assistantID, agent)
	if len(args) == 0 {
		return MCPView{}, &CodedError{Code: "mcp_unsupported", Message: "no MCP configuration could be prepared"}
	}
	statuses, err := MCPProber(ctx, args)
	if err != nil {
		return MCPView{}, &CodedError{Code: "mcp_probe_failed", Message: err.Error()}
	}
	if statuses == nil {
		statuses = []MCPProbeStatus{}
	}
	blob, _ := json.Marshal(statuses)
	now := s.nowMS()
	// The probe's answer is its own row; GetMCP shows it only for servers still
	// granted and part of that answer.
	if _, err := s.db.Exec(`INSERT INTO mcp_bindings(assistant_id,surface,agent_kind,servers,strict,bound_at,verified_state,verified_at) VALUES(?,?,?,?,1,?,?,?)
		ON CONFLICT(assistant_id,surface) DO UPDATE SET agent_kind=excluded.agent_kind, bound_at=excluded.bound_at, verified_state=excluded.verified_state, verified_at=excluded.verified_at`,
		assistantID, mcpProbeSurface, agent, "[]", now, string(blob), now); err != nil {
		return MCPView{}, wrapBusy(err)
	}
	return s.GetMCP(assistantID)
}
