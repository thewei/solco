/**
 * [INPUT]: internal/scripts（Detect/MergeCommands/MatchArgv/MatchTier/CommandEntry）、Shell.scriptStore、paneServiceActivity（service_index.go 的扫描产物）
 * [OUTPUT]: 对外提供 Shell.resolveServiceOrigins（仅扫描 lane 调用）、boundedArgv；serviceOrigins 字段供 render 读取
 * [POS]: 服务标题对齐的推断层（2026-11-03 用户拍板，scripts.MatchArgv 之上）：
 *   扫描 lane 把每个 Pane 前台进程组的 argv 与该目录的命令目录做匹配，paneID→
 *   条目名随 serviceIndex 一起在 win.Update 发布；render 只读 s.serviceOrigins，
 *   零 IO。定义用 scriptTitleFor 同款目录包含规则（List("") + dirContains），
 *   检测命令用 scripts.Detect(root)；per-root 缓存 30s TTL、有界条目数，mutex
 *   防两次扫描并发。远程盘 Detect 返回 nil → 无推断 → 保持进程名，与 Run 菜单
 *   的本地守卫一致。推断无状态、每次扫描全量重算，重启后无需持久化。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"sort"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/scripts"
)

// Bounds for one pane's argv capture: the observed foreground groups are
// small (shell + runner wrapper + server), the caps just keep a runaway
// process tree or a monster flag from bloating the scan result.
const (
	maxServiceArgvs     = 8
	maxArgvTokens       = 16
	maxArgvTokenChars   = 240
	originCacheTTL      = 30 * time.Second // same cadence as the run menu's cache
	originCacheMaxRoots = 32
)

// originRootEntries is one cached root's merged command menu.
type originRootEntries struct {
	entries []scripts.CommandEntry
	at      time.Time
}

// boundedArgv copies one process's argv under the capture caps. Trailing
// tokens go first (`pnpm run start -- --port 9` keeps its prefix), then
// per-token width.
func boundedArgv(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	if len(argv) > maxArgvTokens {
		argv = argv[:maxArgvTokens]
	}
	out := make([]string, len(argv))
	for i, token := range argv {
		if len(token) > maxArgvTokenChars {
			token = token[:maxArgvTokenChars]
		}
		out[i] = token
	}
	return out
}

// resolveServiceOrigins infers which command-menu entry each pane's
// running service belongs to. Called on the scan lane only (it reads
// project files through the cached resolver); the result crosses to the
// UI lane inside scanPaneServices' win.Update.
func (s *Shell) resolveServiceOrigins(panes []herdr.Pane, acts map[string]paneServiceActivity) map[string]string {
	out := make(map[string]string, len(acts))
	for _, pane := range panes {
		act := acts[pane.ID]
		if len(act.Argvs) == 0 || pane.CWD == "" {
			continue
		}
		entries := s.originEntriesFor(pane.CWD)
		if len(entries) == 0 {
			continue
		}
		if entry, tier := scripts.MatchArgv(entries, act.Argvs); tier != scripts.MatchNone && entry.Name != "" {
			out[pane.ID] = entry.Name
		}
	}
	return out
}

// originEntriesFor returns the merged command menu for root (user script
// definitions whose project contains root + detected commands), cached per
// root with a TTL: Detect reads package.json/Makefile/justfile and runs
// once per root per TTL, not once per pane per scan.
func (s *Shell) originEntriesFor(root string) []scripts.CommandEntry {
	s.originMu.Lock()
	defer s.originMu.Unlock()
	if cached, ok := s.originCache[root]; ok && time.Since(cached.at) < originCacheTTL {
		return cached.entries
	}
	var defs []scripts.ScriptDefinition
	if s.scriptStore != nil {
		for _, def := range s.scriptStore.List("") {
			if def.ProjectPath == root || dirContains(def.ProjectPath, root) {
				defs = append(defs, def)
			}
		}
	}
	entries := scripts.MergeCommands(defs, scripts.Detect(root))
	if len(s.originCache) >= originCacheMaxRoots {
		s.pruneOriginCache()
	}
	s.originCache[root] = originRootEntries{entries: entries, at: time.Now()}
	return entries
}

// pruneOriginCache drops the oldest cached roots when the cache hits its
// cap; called with originMu held.
func (s *Shell) pruneOriginCache() {
	type stale struct {
		root string
		at   time.Time
	}
	stales := make([]stale, 0, len(s.originCache))
	for root, cached := range s.originCache {
		if time.Since(cached.at) >= originCacheTTL {
			delete(s.originCache, root)
			continue
		}
		stales = append(stales, stale{root: root, at: cached.at})
	}
	if len(s.originCache) < originCacheMaxRoots {
		return
	}
	sort.Slice(stales, func(i, j int) bool { return stales[i].at.Before(stales[j].at) })
	for _, entry := range stales {
		delete(s.originCache, entry.root)
		if len(s.originCache) < originCacheMaxRoots {
			return
		}
	}
}

// serviceOriginName is the render-side read: the inferred entry name for a
// pane's service, "" when the scan matched nothing (the row keeps the
// process name then).
func (s *Shell) serviceOriginName(paneID string) string {
	return s.serviceOrigins[paneID]
}
