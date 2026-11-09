/**
 * [INPUT]: 依赖 history.AgentID, 用户目录配置 (~/.omp, ~/.claude, ~/.gemini, ~/.codex, ~/.cursor) 与命令行 (agy, omp, pi)
 * [OUTPUT]: 对外提供 Catalog, Entry, Skill, ListSkills, Model, Effort, Load, ModelVerb, EffortVerb, ModelWindow, Classify, IsCompact
 * [POS]: Composer 只读目录与命令能力中心，扫描 agent 自有命令、技能与模型列表
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

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
	"regexp"
	"sort"
	"strconv"
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
	// Efforts are this model's own thinking-effort choices (pi, omp, and
	// Antigravity's "(High)/(Medium)/(Low)" variants); nil = none known.
	Efforts []Effort
	// Reasoning is true when the agent's own listing marks the model as one
	// that thinks (pi's "thinking" column); omp states its levels in Efforts.
	Reasoning bool
	// Pick is the exact prompt text that selects this row when it is not
	// ModelVerb+ID (Antigravity: a model family selects its first variant).
	Pick string
	// Window is the context window the agent's own model listing states
	// (pi, omp); 0 when it states none.
	Window int64
}

// Catalog is the composer's menu for one Agent.
// Effort is one thinking-effort choice. Label is what the panel shows; Send is
// the exact prompt text that applies it (the agent's own typed command), so
// the page and the phone send it through the ordinary path and add no
// vocabulary of their own.
type Effort struct {
	Label string
	Send  string
}

type Catalog struct {
	// Efforts are the choices that do not depend on the model (Claude Code);
	// a Model's own Efforts win for that model.
	Efforts []Effort
	// EffortNote warns that applying an effort has a side effect ("" = none):
	// Claude Code saves it as the model's default effort.
	EffortNote string
	Commands   []Entry
	Skills     []Entry
	Models     []Model
	// ModelVerb is the typed command that switches model for this session
	// ("/model ", "/switch "); empty when the provider only offers a picker.
	ModelVerb string
	// ModelPicker is the picker command ("/model") Chat can structure, "" if none.
	ModelPicker string
	// DefaultModel is the model a fresh session starts on, read from the
	// agent's own config ("" when it names none): what the composer shows
	// before any transcript records a model.
	DefaultModel string
	// ModelNote is a warning shown with the model list when a switch has a
	// side effect the user should know about ("" = none): Antigravity saves
	// the typed choice as its default model for every new session.
	ModelNote string
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
	case history.AgentCursor:
		// Cursor Agent 2026.10.22. Left out on purpose: /max-mode, /fast,
		// /run-everything, /auto-review (raise cost or lower the approval
		// level), /logout, /update, the per-model slash commands and the
		// cosmetic toggles.
		return []Entry{
			resume,
			terminal("/model", "Choose a model"),
			terminal("/add-dir ", "Add a directory to the workspace"),
			terminal("/ask", "Toggle ask mode (read-only Q&A)"),
			terminal("/btw ", "Ask on the side without disrupting the chat"),
			terminal("/changes", "Review changes"),
			terminal("/clear", "Start a new chat session"),
			terminal("/command", "Manage custom commands"),
			terminal("/commit", "Ask the agent to stage and commit changes"),
			terminal("/context", "Show context usage"),
			terminal("/copy-conversation-id", "Copy the conversation id"),
			terminal("/copy-request-id", "Copy the last request id"),
			terminal("/debug ", "Toggle debug mode or send a prompt in it"),
			terminal("/fork", "Fork the chat into a new session"),
			terminal("/goal ", "Start a durable goal"),
			terminal("/help", "Show help"),
			terminal("/jobs", "Open the active tasks list"),
			terminal("/plan ", "Create a plan or show the existing one"),
			terminal("/rename ", "Rename the chat session"),
			terminal("/rewind", "Jump back to a previous message"),
			terminal("/rule", "Manage Cursor rules"),
			terminal("/skills", "Open the skills menu"),
			terminal("/summarize", "Summarize the conversation to reduce context"),
			terminal("/usage", "Show plan and on-demand usage"),
		}
	case history.AgentAntigravity:
		// Antigravity CLI 1.4.12.
		return []Entry{
			resume,
			terminal("/model", "Choose a model"),
			terminal("/add-dir ", "Add a directory to the workspace"),
			terminal("/agents", "List available custom agents"),
			terminal("/btw ", "Ask a side question without interrupting the task"),
			terminal("/clear", "Clear the conversation and start a new one"),
			terminal("/config", "Open the settings panel"),
			terminal("/context", "Visualize current context usage"),
			terminal("/copy", "Copy the last planner response"),
			terminal("/fork", "Branch the conversation at this point"),
			terminal("/goal ", "Run until the goal is finished"),
			terminal("/help", "Show commands and keybindings"),
			terminal("/hooks", "Manage hook configurations"),
			terminal("/mcp", "Manage MCP servers"),
			terminal("/permissions", "Manage tool permissions"),
			terminal("/plan ", "Plan carefully before executing"),
			terminal("/rename ", "Rename the conversation"),
			terminal("/rewind", "Rewind to a previous message"),
			terminal("/skills", "List available skills"),
			terminal("/tasks", "View background tasks"),
			terminal("/usage", "View model quota usage"),
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
	if isEffortText(provider, text) {
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

// IsCompact reports text is the /compact command (optionally with the
// instructions Claude Code accepts). Every agent answers it with a summary
// entry of its own, not with a user row, so Chat has nothing to confirm it by.
func IsCompact(text string) bool {
	first, _, _ := strings.Cut(strings.TrimSpace(text), " ")
	return first == "/compact"
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
	case history.AgentAntigravity:
		// Typed, `/model <display name>` is verified on a real device but
		// Antigravity saves it as the default model (ModelNote says so).
		return "/model "
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
		cat.Efforts = efforts(EffortVerb(provider), claudeEfforts...)
		cat.EffortNote = "Claude Code saves this as the model's default effort."
	case history.AgentCodex:
		cat.Commands = append(cat.Commands, prompts(filepath.Join(codexHome(home), "prompts"), "/prompts:")...)
		cat.Skills = skills("$", filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".agents", "skills"), filepath.Join(codexHome(home), "skills"))
		cat.ModelPicker = "/model"
	case history.AgentPi:
		cat.Commands = append(cat.Commands, prompts(filepath.Join(home, ".pi", "agent", "prompts"), "/")...)
		cat.Skills = skills("/skill:", filepath.Join(root, ".pi", "skills"), filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".pi", "agent", "skills"), filepath.Join(home, ".agents", "skills"))
		cat.ModelVerb = ModelVerb(provider)
		cat.Models = withPiEfforts(piModels(ctx), home)
	case history.AgentOMP:
		cat.Skills = skills("/skill:", filepath.Join(root, ".omp", "skills"), filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".omp", "agent", "skills"), filepath.Join(home, ".agents", "skills"))
		cat.ModelVerb = ModelVerb(provider)
		cat.Models = ompModels(ctx)
	case history.AgentCursor:
		// Skills are slash commands. No model list: the CLI's own picker has
		// 46 base models while `--list-models` has 270+ variants, and a typed
		// `/model <filter>` takes the first match, which can be a dearer one.
		cat.Skills = skills("/",
			filepath.Join(root, ".cursor", "skills"), filepath.Join(root, ".claude", "skills"), filepath.Join(root, ".agents", "skills"),
			filepath.Join(home, ".cursor", "skills"), filepath.Join(home, ".cursor", "skills-cursor"),
			filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills"))
	case history.AgentAntigravity:
		cat.Skills = skills("/",
			filepath.Join(root, ".agents", "skills"),
			filepath.Join(home, ".gemini", "skills"), filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
			filepath.Join(home, ".claude", "skills"))
		cat.ModelVerb = ModelVerb(provider)
		cat.Models = groupAgyEfforts(agyModels(ctx))
		if cat.DefaultModel == "" && len(cat.Models) > 0 {
			if len(cat.Models[0].Efforts) > 0 {
				cat.DefaultModel = cat.Models[0].ID + " (" + cat.Models[0].Efforts[0].Label + ")"
			} else {
				cat.DefaultModel = cat.Models[0].ID
			}
		}
		cat.ModelNote = "Antigravity saves this as the default model for every new session."
	}
	return cat
}

func codexHome(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// Skill is one installed Skill: its name, one-line description and the SKILL.md that defines it.
type Skill struct {
	Name   string
	Detail string
	Path   string
}

// scanSkills reads <dir>/<name>/SKILL.md for each directory; the first directory that defines a name wins.
func scanSkills(dirs ...string) []Skill {
	seen := map[string]bool{}
	var out []Skill
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
			file := filepath.Join(path, "SKILL.md")
			meta, ok := frontmatter(file)
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
			out = append(out, Skill{Name: name, Detail: meta["description"], Path: file})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// skills lists scanSkills as `/` or `$` popover rows.
func skills(prefix string, dirs ...string) []Entry {
	found := scanSkills(dirs...)
	out := make([]Entry, 0, len(found))
	for _, sk := range found {
		out = append(out, Entry{Name: prefix + sk.Name, Insert: prefix + sk.Name + " ", Detail: sk.Detail, Kind: KindSkill})
	}
	return out
}

// ListSkills lists every Skill any supported Agent would find from root (project folders first, then the user's
// own): the union of the directories Load reads per provider. Read-only; Feedback uses it to let the user point an
// Agent at a Skill file.
func ListSkills(root string) []Skill {
	home, _ := os.UserHomeDir()
	var dirs []string
	if root != "" {
		for _, d := range []string{".agents", ".claude", ".cursor", ".omp", ".pi"} {
			dirs = append(dirs, filepath.Join(root, d, "skills"))
		}
	}
	return scanSkills(append(dirs,
		filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".cursor", "skills"), filepath.Join(home, ".cursor", "skills-cursor"),
		filepath.Join(codexHome(home), "skills"), filepath.Join(home, ".omp", "agent", "skills"),
		filepath.Join(home, ".pi", "agent", "skills"), filepath.Join(home, ".gemini", "skills"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills"))...)
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
		window := int64(0)
		if len(fields) >= 3 {
			window = parseTokenCount(fields[2])
		}
		// provider model context max-out thinking images
		models = append(models, Model{ID: fields[0] + "/" + fields[1], Window: window, Reasoning: len(fields) >= 5 && fields[4] == "yes"})
	}
	return models
}

// parseTokenCount reads a listing's "250K" / "1M" / "65.5K" (decimal, as the
// listings print them); 0 when the cell is not a count.
func parseTokenCount(cell string) int64 {
	cell = strings.ToUpper(strings.TrimSpace(cell))
	scale := 1.0
	switch {
	case strings.HasSuffix(cell, "K"):
		scale, cell = 1e3, strings.TrimSuffix(cell, "K")
	case strings.HasSuffix(cell, "M"):
		scale, cell = 1e6, strings.TrimSuffix(cell, "M")
	}
	value, err := strconv.ParseFloat(cell, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return int64(value*scale + 0.5)
}

// ModelWindow is the context window the agent lists for the model id as its
// transcripts spell it, 0 when the agent lists none (only pi and omp list one;
// Claude Code and Codex state theirs in their own logs or not at all). Listed
// ids are "provider/model"; omp's transcripts may drop the provider ("group/glm-flash"
// for the listed "magpie/group/glm-flash"), so a shorter id matches when
// exactly one row ends with it. Uses the same ten-minute cache as the model list.
func ModelWindow(ctx context.Context, provider history.AgentID, id string) int64 {
	var models []Model
	switch provider {
	case history.AgentPi:
		models = piModels(ctx)
	case history.AgentOMP:
		models = ompModels(ctx)
	default:
		return 0
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return 0
	}
	var found, matches int64
	for _, model := range models {
		if model.ID == id {
			return model.Window
		}
		if strings.HasSuffix(model.ID, "/"+id) {
			found, matches = model.Window, matches+1
		}
	}
	if matches == 1 {
		return found
	}
	return 0
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
	contextAt, thinkingAt := -1, -1
	for _, line := range strings.Split(out, "\n") {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "│") {
			cells := strings.Split(strings.Trim(text, "│"), "│")
			id := strings.TrimSpace(cells[0])
			if id == "model" {
				contextAt, thinkingAt = -1, -1
				for at, cell := range cells {
					switch strings.TrimSpace(cell) {
					case "context":
						contextAt = at
					case "thinking":
						thinkingAt = at
					}
				}
			} else if id != "" && provider != "" && len(models) < maxSkills {
				window := int64(0)
				if contextAt > 0 && contextAt < len(cells) {
					window = parseTokenCount(cells[contextAt])
				}
				model := Model{ID: provider + "/" + id, Window: window}
				// The row's "thinking" cell lists the levels it takes ("-" =
				// none); `/effort ` itself also offers off and auto (verified).
				if thinkingAt > 0 && thinkingAt < len(cells) {
					if cell := strings.TrimSpace(cells[thinkingAt]); cell != "" && cell != "-" {
						model.Efforts = efforts("/effort ", append([]string{"off", "auto"}, strings.Split(cell, ",")...)...)
					}
				}
				models = append(models, model)
			}
			continue
		}
		if name, rest, ok := strings.Cut(text, " ("); ok && strings.HasSuffix(rest, ")") && !strings.ContainsAny(name, " ┌└├│") {
			provider = name
		}
	}
	return models
}

// agyModels lists `agy models`: tab-separated "id<TAB>Display Name" rows after
// a progress line. The id is the row's Detail; the display name is what the
// typed `/model` command and the transcripts spell.
func agyModels(ctx context.Context) []Model {
	return cachedModels(ctx, "agy", func(ctx context.Context) []Model {
		return parseAgyModels(string(runList(ctx, "agy", "models")))
	})
}

func parseAgyModels(out string) []Model {
	var models []Model
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		id, name = strings.TrimSpace(id), strings.TrimSpace(name)
		if !ok || id == "" || name == "" || IsMaxMode(id) || len(models) >= maxSkills {
			continue
		}
		models = append(models, Model{ID: name, Detail: id})
	}
	return models
}

// IsMaxMode reports a model id of a Max-mode variant ("…-max", "…-max-fast",
// "…-thinking-max"): the composer never offers them, they bill at the top rate.
func IsMaxMode(id string) bool {
	for _, part := range strings.FieldsFunc(strings.ToLower(id), func(r rune) bool { return r == '-' || r == ' ' || r == '_' }) {
		if part == "max" {
			return true
		}
	}
	return false
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
	case history.AgentAntigravity:
		if m := claudeDefaultModel(read(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))); m != "" {
			return m
		}
		return claudeDefaultModel(read(filepath.Join(home, ".gemini", "settings.json")))
	case history.AgentCursor:
		return cursorDefaultModel(read(filepath.Join(home, ".cursor", "cli-config.json")))
	}
	return ""
}

// cursorDefaultModel reads model.modelId of cli-config.json, the id the chat
// store's lastUsedModel spells.
func cursorDefaultModel(config string) string {
	var settings struct {
		Model struct {
			ID string `json:"modelId"`
		} `json:"model"`
	}
	if json.Unmarshal([]byte(config), &settings) != nil {
		return ""
	}
	return settings.Model.ID
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

// EffortVerb is the typed command that sets the thinking effort of a running
// session ("" = none): pi `/thinking low`, omp and Claude Code `/effort low`
// (verified on real devices 2026-11-01; Antigravity's effort is part of the
// model name, Codex's is the second step of its `/model` picker).
func EffortVerb(provider history.AgentID) string {
	switch provider {
	case history.AgentPi:
		return "/thinking "
	case history.AgentOMP, history.AgentClaudeCode:
		return "/effort "
	}
	return ""
}

// effortLevels is the vocabulary of EffortVerb arguments across pi, omp and
// Claude Code. Classify uses it to tell `/effort low` (an applied level, sent
// as typed) from a bare `/effort` (the agent's own dialog, Terminal).
var effortLevels = map[string]bool{"off": true, "auto": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func isEffortText(provider history.AgentID, text string) bool {
	verb := EffortVerb(provider)
	if verb == "" || !strings.HasPrefix(text, verb) {
		return false
	}
	return effortLevels[strings.TrimSpace(strings.TrimPrefix(text, verb))]
}

// claudeEfforts is the argument list Claude Code prints for `/effort ` itself
// (`[low|medium|high|xhigh|max|auto|ultracode [on|off]]`, claude 2026-11); its
// own model-dependent clamping applies.
var claudeEfforts = []string{"low", "medium", "high", "xhigh", "max", "auto"}

func efforts(verb string, levels ...string) []Effort {
	out := make([]Effort, 0, len(levels))
	for _, level := range levels {
		out = append(out, Effort{Label: level, Send: verb + level})
	}
	return out
}

// piEffortOrder is pi's level order; a model's `thinkingLevelMap` in pi's own
// models.json names the supported ones (null = unsupported). A reasoning model
// without a map offers off…high (verified: the `/thinking ` list on such a model).
var piEffortOrder = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// withPiEfforts returns models with each reasoning model's efforts filled in
// from pi's own models.json (read-only). The cached list is not modified.
func withPiEfforts(models []Model, home string) []Model {
	var file struct {
		Providers map[string]struct {
			Models []struct {
				ID  string             `json:"id"`
				Map map[string]*string `json:"thinkingLevelMap"`
			} `json:"models"`
		} `json:"providers"`
	}
	if data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json")); err == nil && len(data) < 4<<20 {
		_ = json.Unmarshal(data, &file)
	}
	maps := map[string]map[string]*string{}
	for provider, entry := range file.Providers {
		for _, model := range entry.Models {
			if model.Map != nil {
				maps[provider+"/"+model.ID] = model.Map
			}
		}
	}
	out := make([]Model, len(models))
	copy(out, models)
	for index := range out {
		if !out[index].Reasoning {
			continue
		}
		var levels []string
		for _, level := range piEffortOrder {
			if byLevel, ok := maps[out[index].ID]; ok {
				if byLevel[level] != nil {
					levels = append(levels, level)
				}
			} else if level != "xhigh" && level != "max" {
				levels = append(levels, level)
			}
		}
		out[index].Efforts = efforts("/thinking ", levels...)
	}
	return out
}

// groupAgyEfforts folds Antigravity's "Name (High)" / "(Medium)" / "(Low)"
// variants into one family row with the variants as its efforts. Selecting the
// family picks its first variant (Pick); a family needs two variants to be one
// ("(Thinking)" is a model name, not a level).
func groupAgyEfforts(models []Model) []Model {
	type family struct {
		base     string
		variants []Model
		levels   []string
	}
	var order []string
	families := map[string]*family{}
	rows := map[string]Model{}
	for _, model := range models {
		base, level, ok := splitAgyEffort(model.ID)
		if !ok {
			order = append(order, model.ID)
			rows[model.ID] = model
			continue
		}
		fam := families[base]
		if fam == nil {
			fam = &family{base: base}
			families[base] = fam
			order = append(order, base)
		}
		fam.variants = append(fam.variants, model)
		fam.levels = append(fam.levels, level)
	}
	out := make([]Model, 0, len(order))
	for _, key := range order {
		fam := families[key]
		if fam == nil {
			out = append(out, rows[key])
			continue
		}
		if len(fam.variants) < 2 {
			out = append(out, fam.variants[0])
			continue
		}
		row := Model{ID: fam.base, Pick: "/model " + fam.variants[0].ID, Detail: fam.variants[0].Detail}
		for index, variant := range fam.variants {
			row.Efforts = append(row.Efforts, Effort{Label: fam.levels[index], Send: "/model " + variant.ID})
		}
		out = append(out, row)
	}
	return out
}

var agyEffortSuffix = regexp.MustCompile(`^(.+) \((High|Medium|Low|Minimal)\)$`)

func splitAgyEffort(name string) (base, level string, ok bool) {
	match := agyEffortSuffix.FindStringSubmatch(name)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}
