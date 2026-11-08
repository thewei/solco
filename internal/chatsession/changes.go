package chatsession

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 conversation.TimelineStep（edit 类工具路径预览）、gitworkbench.ChangesSnapshot（工作树 +/−）
 * [OUTPUT]: 对外提供 TurnChanges：回合改动卡的文件行
 * [POS]: Chat 桥的改动卡投影；只读现有快照，不新增 Git 调用、不解析 Provider 私有 diff 形状
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// editToolName matches the providers' file-editing tools whose one-line
// input preview is the target path (Claude Edit/MultiEdit/Write/NotebookEdit,
// Pi edit/write, Cursor StrReplace/Write/Delete/ApplyPatch, Antigravity
// edit_file/write_file). It is deliberately an allow-list: "TodoWrite" and shell
// tools must never read as edits.
var editToolName = regexp.MustCompile(`(?i)^(edit|multiedit|write|notebookedit|apply_?patch|str_?replace\w*|edit_file|write_file|delete)$`)

// jsonPathToken finds the first repo-style path (a slash and a file
// extension) inside a JSON-shaped preview. Some agents (omp's hashline edit)
// carry no plain path key, so the one-line preview falls back to the
// arguments' JSON; the path is the only thing read from it.
var jsonPathToken = regexp.MustCompile(`(?:/?[\w@.+~-]+/)+[\w@.+~-]+\.[A-Za-z0-9]{1,8}`)

// Header shapes that name the files a patch-style edit touches:
// Codex's apply_patch ("*** Update File: path") and omp's hashline edit
// ("[path#HASH]"). Only the file names are read, never the hunks.
var (
	patchFileHeader    = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+?)\s*$|^\*\*\* Move to: (.+?)\s*$`)
	hashlineFileHeader = regexp.MustCompile(`(?:^|[\s"])\[([\w@.+~/ -]+)#[0-9A-Za-z]{2,8}\]`)
)

// editedPaths names the files one edit tool call touches. The full input is
// preferred when it carries file headers (several files per call); the
// one-line preview path is the fallback for the single-path tools.
func editedPaths(tool *conversation.ConversationToolCall) []string {
	if tool.Input != nil {
		text := *tool.Input
		if strings.HasPrefix(text, "{") {
			var args map[string]any
			if json.Unmarshal([]byte(text), &args) == nil {
				var parts []string
				for _, value := range args {
					if str, ok := value.(string); ok {
						parts = append(parts, str)
					}
				}
				text = strings.Join(parts, "\n")
			} else {
				text = strings.ReplaceAll(text, `\n`, "\n") // clipped JSON
			}
		}
		var out []string
		for _, m := range patchFileHeader.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1]+m[2])
		}
		for _, m := range hashlineFileHeader.FindAllStringSubmatch(text, -1) {
			out = append(out, strings.TrimSpace(m[1]))
		}
		if len(out) > 0 {
			return out
		}
	}
	path := strings.TrimSpace(tool.InputPreview)
	if strings.HasPrefix(path, "{") {
		path = jsonPathToken.FindString(path)
	}
	if path == "" || strings.ContainsAny(path, "\n\t{") || strings.Contains(path, "  ") {
		return nil
	}
	return []string{path}
}

// TurnChanges lists the files one settled turn's edit tools touched, in
// first-touch order. Data-source contract (user decision 2026-10-30):
//
//   - file names come from the edit tools' own path previews (provider
//     neutral: only a path is read, never a provider's diff payload; a
//     JSON-shaped preview yields its first path-like token);
//   - "+N −M" comes from the live working-tree snapshot, and only for the
//     latest settled turn of a local repository — the numbers describe the
//     files' current change against HEAD, not this turn's increment, so the
//     page titles the card "Changes in working tree";
//   - every other case (older turns, read-only sessions, remote or
//     non-repository panes, files already committed) is a name alone.
//
// Paths are repo-relative when they sit under root, otherwise kept as given.
func TurnChanges(steps []conversation.TimelineStep, root string, snap *gitworkbench.ChangesSnapshot, counted bool) []ChatChange {
	var out []ChatChange
	seen := map[string]bool{}
	for _, step := range steps {
		if step.Kind != conversation.StepTool || step.Tool == nil || !editToolName.MatchString(step.Tool.Name) {
			continue
		}
		for _, path := range editedPaths(step.Tool) {
			if root != "" && filepath.IsAbs(path) {
				if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
					path = filepath.ToSlash(rel)
				}
			}
			if seen[path] {
				continue
			}
			seen[path] = true
			change := ChatChange{Path: path}
			if counted && snap != nil && snap.Root == root && !filepath.IsAbs(path) {
				if file, ok := snap.ByPath(path); ok {
					change.Additions, change.Deletions, change.Counted = file.Additions, file.Deletions, true
				}
			}
			out = append(out, change)
		}
	}
	return out
}
