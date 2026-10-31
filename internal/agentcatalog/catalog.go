// Package agentcatalog lists what the composer can offer for one Agent: its
// Skills, custom prompt commands, a short hand-verified table of built-in
// commands and the models it can switch to. Everything is read from the
// provider's own files or CLI; nothing is written and no Provider command is
// ever executed here (only the read-only model listings of pi and omp run).
package agentcatalog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/usage"
)

// Kind says what happens when the entry is sent.
type Kind string

const (
	// KindText is a command whose effect lands in the transcript.
	KindText Kind = "text"
	// KindPicker opens a list picker that Chat turns into a Question card.
	KindPicker Kind = "picker"
	// KindTerminal opens a picker Chat cannot structure; Chat hands over to Terminal.
	KindTerminal Kind = "terminal"
	// KindSkill invokes a Skill by name.
	KindSkill Kind = "skill"
	// KindPrompt is a user-written prompt command.
	KindPrompt Kind = "prompt"
)

// Entry is one row of the `/` and `$` popover.
type Entry struct {
	Name   string // what the user reads, e.g. "/compact"
	Insert string // what lands in the composer, e.g. "/compact "
	Detail string
	Kind   Kind
}

// Model is one switchable model.
type Model struct {
	ID     string
	Detail string
}

// Catalog is the composer's menu for one Agent.
type Catalog struct {
	Commands []Entry
	Skills   []Entry
	Models   []Model
	// ModelVerb is the typed command that switches model for this session
	// ("/model ", "/switch "); empty when the provider only offers a picker.
	ModelVerb string
	// ModelPicker is the picker command ("/model") Chat can structure, "" if none.
	ModelPicker string
	// DefaultModel is the model a fresh session starts on, read from the
	// agent's own config ("" when it names none): what the composer shows
	// before any transcript records a model.
	DefaultModel string
}

// maxSkills bounds a runaway skills folder.
const maxSkills = 500

// builtins is the table of each agent's own built-in commands, read from the
// agent's own `/` popup (Claude Code 2.1.308, Codex 0.164, pi 1.4, omp 18.14).
// Only /compact and /init (their effect lands in the transcript), the model
// and permission pickers (a Question card) and omp's /switch are handled in
// Chat; every other command is KindTerminal — sent as typed, then Chat hands
// over to Terminal, where the agent shows its own output or dialog. Commands
// that end the agent process (/exit, /quit, /logout, /delete) are left out.
func builtins(provider history.AgentID) []Entry {
	compact := Entry{"/compact", "/compact", "Summarize the conversation to free context", KindText}
	resume := Entry{"/resume", "/resume", "Switch to another session", KindTerminal}
	switch provider {
	case history.AgentClaudeCode:
		compact.Insert = "/compact "
		return []Entry{
			compact,
			{"/init", "/init", "Write a CLAUDE.md for this project", KindText},
			{"/model", "/model", "Choose a model", KindPicker},
			resume,
			terminal("/add-dir ", "Add a working directory"),
			terminal("/background", "Send this session to the background"),
			terminal("/branch", "Branch the conversation at this point"),
			terminal("/btw ", "Ask a quick side question"),
			terminal("/cd ", "Move this session to another directory"),
			terminal("/config", "Open settings"),
			terminal("/context", "Show current context usage"),
			terminal("/copy", "Copy Claude's last response"),
			terminal("/effort ", "Set the effort level"),
			terminal("/export", "Export the conversation"),
			terminal("/fast", "Toggle fast mode"),
			terminal("/goal ", "Set a goal Claude checks before stopping"),
			terminal("/help", "Show help and available commands"),
			terminal("/hooks", "View hook configurations"),
			terminal("/ide", "Manage IDE integrations"),
			terminal("/insights", "Report on your Claude Code sessions"),
			terminal("/keybindings", "Open your keyboard shortcuts file"),
			terminal("/loop ", "Run a prompt on an interval"),
			terminal("/mcp", "Manage MCP servers"),
			terminal("/output-style", "Choose an output style"),
			terminal("/permissions", "Manage allow and deny rules"),
			terminal("/plan", "Enable plan mode or view the plan"),
			terminal("/recap", "One-line session recap"),
			terminal("/release-notes", "View release notes"),
			terminal("/reload-plugins", "Activate pending plugin changes"),
			terminal("/rename ", "Rename the conversation"),
			terminal("/rewind", "Restore code or conversation to an earlier point"),
			terminal("/sandbox", "Configure the sandbox"),
			terminal("/status", "Version, model, account and connectivity"),
			terminal("/theme", "Change the theme"),
			terminal("/usage", "Session cost, plan usage and activity"),
		}
	case history.AgentCodex:
		return []Entry{
			compact,
			{"/init", "/init", "Write an AGENTS.md for this project", KindText},
			{"/model", "/model", "Choose a model and effort", KindPicker},
			{"/permissions", "/permissions", "Choose what Codex may do without asking", KindPicker},
			resume,
			terminal("/agents", "Open the agent command center"),
			terminal("/archive", "Archive this session"),
			terminal("/cd ", "Change the working directory"),
			terminal("/config", "Configure Codex preferences"),
			terminal("/copy", "Copy the last response"),
			terminal("/diff", "Show git diff, untracked files included"),
			terminal("/experimental", "Toggle experimental features"),
			terminal("/export", "Export the conversation as Markdown"),
			terminal("/fork", "Fork the current chat"),
			terminal("/goal ", "Set or view the goal of a long task"),
			terminal("/hooks", "View and manage lifecycle hooks"),
			terminal("/ide", "Include IDE selection and open files"),
			terminal("/keymap", "Remap TUI shortcuts"),
			terminal("/memories", "Configure memory use and generation"),
			terminal("/mention ", "Mention a file"),
			terminal("/new", "Start a new chat"),
			terminal("/plan", "Switch to Plan mode"),
			terminal("/pwd", "Show the working directory"),
			terminal("/recap", "Summarize the conversation now"),
			terminal("/rename ", "Rename the thread"),
			terminal("/review", "Review the current changes"),
			terminal("/side", "Start a side conversation"),
			terminal("/skills", "Browse and use skills"),
			terminal("/status", "Session configuration and token usage"),
			terminal("/usage", "Account usage and limits"),
			terminal("/vim", "Toggle Vim mode for the composer"),
			terminal("/warnings", "View retained warnings"),
			terminal("/worktree", "Continue in a new worktree"),
		}
	case history.AgentPi:
		return []Entry{
			compact,
			{"/thinking", "/thinking ", "Set the thinking level", KindText},
			resume,
			terminal("/changelog", "Show changelog entries"),
			terminal("/clone", "Duplicate the session at this position"),
			terminal("/copy", "Copy the last agent message"),
			terminal("/export ", "Export the session (HTML or JSONL)"),
			terminal("/fork", "Fork from a previous user message"),
			terminal("/hotkeys", "Show all keyboard shortcuts"),
			terminal("/import ", "Import a session from a JSONL file"),
			terminal("/model", "Choose a model"),
			terminal("/name ", "Set the session name"),
			terminal("/new", "Start a new session"),
			terminal("/reload", "Reload keybindings, extensions and skills"),
			terminal("/scoped-models", "Choose models for Ctrl+P cycling"),
			terminal("/session", "Show session info and stats"),
			terminal("/settings", "Open the settings menu"),
			terminal("/share", "Share the session"),
			terminal("/tree", "Navigate the session tree"),
			terminal("/trust", "Save the project trust decision"),
		}
	case history.AgentOMP:
		return []Entry{
			compact,
			resume,
			{"/model", "/model", "Choose a model or role", KindTerminal},
			terminal("/advisor", "Configure the advisor"),
			terminal("/agents", "Open the agents hub"),
			terminal("/changelog", "Show changelog entries"),
			terminal("/context", "Show context usage"),
			terminal("/copy", "Pick text or code to copy"),
			terminal("/dump", "Copy the session transcript"),
			terminal("/effort ", "Set the thinking level"),
			terminal("/export", "Export the session to HTML"),
			terminal("/extensions", "Open the Extension Control Center"),
			terminal("/fast", "Toggle fast mode"),
			terminal("/git", "Open the git UI"),
			terminal("/goal ", "Set or view the goal"),
			terminal("/handoff", "Summarize into a handoff document and compact"),
			terminal("/hotkeys", "Show all keyboard shortcuts"),
			terminal("/jobs", "Show background jobs"),
			terminal("/loop ", "Run a prompt in a loop"),
			terminal("/new", "Start a new session"),
			terminal("/open", "Open the last link in the browser"),
			terminal("/plan", "Toggle plan mode"),
			terminal("/queue ", "Queue a message for after the agent yields"),
			terminal("/session", "Session management"),
			terminal("/settings", "Open the settings menu"),
			terminal("/setup", "Open provider setup"),
			terminal("/share", "Share the session through an encrypted link"),
			terminal("/stats", "Launch the local stats dashboard"),
			terminal("/todo", "Show todos"),
			terminal("/tools", "Show active tools"),
			terminal("/usage", "Show provider usage and limits"),
		}
	}
	return nil
}

// terminal builds a KindTerminal row; a trailing space in cmd marks a command
// that takes an argument (the composer leaves the caret after it).
func terminal(cmd, detail string) Entry {
	return Entry{Name: strings.TrimSpace(cmd), Insert: cmd, Detail: detail, Kind: KindTerminal}
}

// Classify reports what Chat does with a sent text: KindPicker for exactly a
// built-in picker command, KindTerminal for a terminal-handled built-in
// (arguments allowed), "" for everything else. A typed model switch of the
// provider ("/model x" for pi) is never handed over.
func Classify(provider history.AgentID, text string) Kind {
	text = strings.TrimSpace(text)
	if verb := ModelVerb(provider); verb != "" && strings.HasPrefix(text, verb) {
		return ""
	}
	first, _, _ := strings.Cut(text, " ")
	for _, entry := range builtins(provider) {
		switch {
		case entry.Kind == KindPicker && strings.TrimSpace(entry.Insert) == text:
			return KindPicker
		case entry.Kind == KindTerminal && entry.Name == first:
			return KindTerminal
		}
	}
	return ""
}

// ModelVerb is the typed command that switches model for provider, "" when
// the provider only offers a picker. Claude Code has one but is left out on
// purpose: typed, it saves the model as the user's default, while its picker
// takes `s` for "this session only".
func ModelVerb(provider history.AgentID) string {
	switch provider {
	case history.AgentPi:
		return "/model "
	case history.AgentOMP:
		return "/switch "
	}
	return ""
}

// Load builds the catalog for a provider. root is the project root ("" when unknown).
func Load(ctx context.Context, provider history.AgentID, root string) Catalog {
	home, _ := os.UserHomeDir()
	cat := Catalog{Commands: builtins(provider), DefaultModel: defaultModel(provider, home)}
	switch provider {
	case history.AgentClaudeCode:
		cat.Commands = append(cat.Commands, prompts(filepath.Join(home, ".claude", "commands"), "/")...)
		if root != "" {
			cat.Commands = append(cat.Commands, prompts(filepath.Join(root, ".claude", "commands"), "/")...)
		}
		cat.Skills = skills("/", filepath.Join(root, ".claude", "skills"), filepath.Join(home, ".claude", "skills"))
		cat.ModelPicker = "/model"
	case history.AgentCodex:
		cat.Commands = append(cat.Commands, prompts(filepath.Join(codexHome(home), "prompts"), "/prompts:")...)
		cat.Skills = skills("$", filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".agents", "skills"), filepath.Join(codexHome(home), "skills"))
		cat.ModelPicker = "/model"
	case history.AgentPi:
		cat.Commands = append(cat.Commands, prompts(filepath.Join(home, ".pi", "agent", "prompts"), "/")...)
		cat.Skills = skills("/skill:", filepath.Join(root, ".pi", "skills"), filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".pi", "agent", "skills"), filepath.Join(home, ".agents", "skills"))
		cat.ModelVerb = ModelVerb(provider)
		cat.Models = piModels(ctx)
	case history.AgentOMP:
		cat.Skills = skills("/skill:", filepath.Join(root, ".omp", "skills"), filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".omp", "agent", "skills"), filepath.Join(home, ".agents", "skills"))
		cat.ModelVerb = ModelVerb(provider)
		cat.Models = ompModels(ctx)
	}
	return cat
}

func codexHome(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// skills reads <dir>/<name>/SKILL.md for each directory; the first directory
// that defines a name wins.
func skills(prefix string, dirs ...string) []Entry {
	seen := map[string]bool{}
	var out []Entry
	for _, dir := range dirs {
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			if strings.HasPrefix(item.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, item.Name())
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				continue
			}
			meta, ok := frontmatter(filepath.Join(path, "SKILL.md"))
			if !ok {
				continue
			}
			name := meta["name"]
			if name == "" {
				name = item.Name()
			}
			if seen[name] || len(out) >= maxSkills {
				continue
			}
			seen[name] = true
			out = append(out, Entry{Name: prefix + name, Insert: prefix + name + " ", Detail: meta["description"], Kind: KindSkill})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// prompts reads <dir>/**.md as user prompt commands ("a/b.md" -> "a:b").
func prompts(dir, prefix string) []Entry {
	var out []Entry
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" || len(out) >= maxSkills {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		name := strings.ReplaceAll(strings.TrimSuffix(rel, ".md"), string(filepath.Separator), ":")
		meta, _ := frontmatter(path)
		out = append(out, Entry{Name: prefix + name, Insert: prefix + name + " ", Detail: meta["description"], Kind: KindPrompt})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// frontmatter reads the leading `---` block's simple `key: value` lines
// (quoted or plain, or a `>`/`|` block folded to one line). ok is false when
// the file cannot be read.
func frontmatter(path string) (map[string]string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	meta := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return meta, true
	}
	var key string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if key != "" {
				meta[key] = strings.TrimSpace(meta[key] + " " + strings.TrimSpace(line))
			}
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			key = ""
			continue
		}
		key = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if value == ">" || value == "|" || value == ">-" || value == "|-" {
			value = ""
		}
		meta[key] = strings.Trim(value, `"'`)
	}
	return meta, true
}

var (
	modelMu    sync.Mutex
	modelCache = map[string]modelEntry{}
)

type modelEntry struct {
	at     time.Time
	models []Model
}

// cachedModels runs list at most once per ten minutes per provider.
func cachedModels(ctx context.Context, key string, list func(context.Context) []Model) []Model {
	modelMu.Lock()
	hit, ok := modelCache[key]
	modelMu.Unlock()
	if ok && time.Since(hit.at) < 10*time.Minute {
		return hit.models
	}
	models := list(ctx)
	modelMu.Lock()
	modelCache[key] = modelEntry{at: time.Now(), models: models}
	modelMu.Unlock()
	return models
}

func runList(ctx context.Context, tool string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	path, err := usage.FindTool(tool)
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = usage.ToolEnv(path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if stdout.Len() > 0 {
		return stdout.Bytes()
	}
	return stderr.Bytes()
}

// runInLoginShell runs a command line in the user's login shell, so a CLI
// whose `#!/usr/bin/env bun` shebang depends on the user's own PATH order
// (omp wants the newest bun) sees the same runtime as in their terminal.
func runInLoginShell(ctx context.Context, line string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "/bin/zsh", "-lic", line).Output()
	return out
}

// piModels lists `pi --list-models`.
func piModels(ctx context.Context) []Model {
	return cachedModels(ctx, "pi", func(ctx context.Context) []Model {
		return parsePiModels(string(runList(ctx, "pi", "--list-models")))
	})
}

// parsePiModels reads "provider model context …" rows under a header line.
func parsePiModels(out string) []Model {
	var models []Model
	for index, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if index == 0 || len(fields) < 2 || len(models) >= maxSkills {
			continue
		}
		models = append(models, Model{ID: fields[0] + "/" + fields[1]})
	}
	return models
}

// ompModels lists `omp models`.
func ompModels(ctx context.Context) []Model {
	return cachedModels(ctx, "omp", func(ctx context.Context) []Model {
		return parseOmpModels(string(runInLoginShell(ctx, "omp models 2>&1")))
	})
}

// parseOmpModels reads a "provider (N)" heading followed by table rows.
func parseOmpModels(out string) []Model {
	var models []Model
	provider := ""
	for _, line := range strings.Split(out, "\n") {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "│") {
			cells := strings.Split(strings.Trim(text, "│"), "│")
			id := strings.TrimSpace(cells[0])
			if id != "" && id != "model" && provider != "" && len(models) < maxSkills {
				models = append(models, Model{ID: provider + "/" + id})
			}
			continue
		}
		if name, rest, ok := strings.Cut(text, " ("); ok && strings.HasSuffix(rest, ")") && !strings.ContainsAny(name, " ┌└├│") {
			provider = name
		}
	}
	return models
}

// defaultModel reads the model a new session of provider starts on from the
// agent's own config, in the spelling its transcripts use.
func defaultModel(provider history.AgentID, home string) string {
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 1<<20 {
			return ""
		}
		return string(data)
	}
	switch provider {
	case history.AgentOMP:
		return ompDefaultModel(read(filepath.Join(home, ".omp", "agent", "config.yml")))
	case history.AgentCodex:
		return codexDefaultModel(read(filepath.Join(codexHome(home), "config.toml")))
	case history.AgentClaudeCode:
		return claudeDefaultModel(read(filepath.Join(home, ".claude", "settings.json")))
	}
	return ""
}

// ompDefaultModel reads modelRoles.default ("provider/id[:level]") and returns
// "id": the transcript names the model without its provider or level.
func ompDefaultModel(config string) string {
	inRoles := false
	for _, line := range strings.Split(config, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inRoles = strings.HasPrefix(line, "modelRoles:")
			continue
		}
		if !inRoles {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(key) != "default" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if cut := strings.LastIndex(value, ":"); cut > strings.LastIndex(value, "/") {
			value = value[:cut]
		}
		if _, id, ok := strings.Cut(value, "/"); ok {
			return id
		}
		return value
	}
	return ""
}

// codexDefaultModel reads the top-level `model = "…"` of config.toml.
func codexDefaultModel(config string) string {
	for _, line := range strings.Split(config, "\n") {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "[") {
			return ""
		}
		if key, value, ok := strings.Cut(text, "="); ok && strings.TrimSpace(key) == "model" {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// claudeDefaultModel reads the top-level "model" of settings.json.
func claudeDefaultModel(config string) string {
	var settings struct {
		Model string `json:"model"`
	}
	if json.Unmarshal([]byte(config), &settings) != nil {
		return ""
	}
	return settings.Model
}
