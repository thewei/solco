/**
 * [INPUT]: stdlib path/filepath/strings; CommandEntry (detect.go)
 * [OUTPUT]: MatchTier（None/Segment/Body/Typed）、MatchArgv
 * [POS]: 服务标题对齐的纯匹配层（2026-11-03 用户拍板）：把 pane 前台进程的 argv
 *   归一化后与命令目录条目做 token 匹配，命中的条目名即侧栏 Services 行的标题。
 *   两层语义：Typed = argv 是条目输入行的 runner wrapper，留在前台进程组
 *   （npm/pnpm/bun/yarn/make/just，真机已验证）；Body/Segment = argv 是条目
 *   body 运行出的子进程，body 全序列命中为 Body，复合命令（&&/;）的单段命中为
 *   Segment。优先级 Typed > Body > Segment；同层同分歧义时不猜（返回 none）。
 *   纯函数、无 IO、无状态；归一化规则集中在 runnerAlias/pairMatch/lexCommand。
 *   词法器是 POSIX-lite（引号、运算符、env 前缀、重定向、$VAR 通配），不是完整
 *   shell——展开不可逆的（glob 展开、命令替换的值）匹配失败即诚实降级为进程名。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package scripts

import (
	"path/filepath"
	"strings"
)

// MatchTier grades how strongly one CommandEntry explains a live process:
// the typed wrapper outranks the body it spawned, which outranks one
// segment of a compound body. MatchNone means no entry explains the argv.
type MatchTier int

const (
	MatchNone MatchTier = iota
	// MatchSegment: one segment of a compound body (… && vite build) runs.
	MatchSegment
	// MatchBody: the entry's whole body runs as this process.
	MatchBody
	// MatchTyped: the entry's typed line itself is in the foreground group
	// (the package-manager/make/just wrapper Solco or the user typed).
	MatchTyped
)

// segmentMarker is lexTokens' token for any operator run (&& || ; | &):
// a segment boundary, never a real token.
const segmentMarker = "\x00seg"

func (t MatchTier) String() string {
	switch t {
	case MatchSegment:
		return "segment"
	case MatchBody:
		return "body"
	case MatchTyped:
		return "typed"
	default:
		return "none"
	}
}

// wildToken stands for a shell expansion the lexer cannot evaluate ($VAR,
// ${VAR}, $(…), `…`): it matches any single argv token.
const wildToken = "\x00any"

// MatchArgv resolves the command-menu entry a pane's foreground processes
// belong to. argvs are the measured argv lines of the pane's foreground
// process group (wrappers and children alike). Returns the best entry and
// its tier; a zero entry with MatchNone when nothing explains the argvs —
// callers keep the process name then, never guessing a title.
func MatchArgv(entries []CommandEntry, argvs [][]string) (CommandEntry, MatchTier) {
	normalized := make([][]string, 0, len(argvs))
	for _, argv := range argvs {
		if unwrapped := unwrapInterpreter(argv); len(unwrapped) > 0 {
			normalized = append(normalized, unwrapped)
		}
	}
	if len(normalized) == 0 {
		return CommandEntry{}, MatchNone
	}

	best := CommandEntry{}
	bestTier, bestScore := MatchNone, 0
	for _, entry := range entries {
		if entry.Name == "" || (entry.Command == "" && entry.Body == "") {
			continue
		}
		for _, tier := range []MatchTier{MatchTyped, MatchBody} {
			sequences, fullBody := entrySequences(entry, tier)
			if len(sequences) == 0 {
				continue
			}
			score := 0
			for _, argv := range normalized {
				for _, seq := range sequences {
					if got, ok := seqMatch(seq, argv); ok && got > score {
						score = got
					}
				}
			}
			if score == 0 {
				continue
			}
			// A compound body (… && …) only claims the Segment tier: one
			// of its commands runs, not the whole script. A single-segment
			// body is the whole script and stays Body.
			if tier == MatchBody && !fullBody {
				tier = MatchSegment
			}
			if betterCandidate(tier, score, entry.User, bestTier, bestScore, best.User) {
				best, bestTier, bestScore = entry, tier, score
			}
			break // one entry claims its strongest tier only
		}
	}
	if bestTier == MatchNone {
		return CommandEntry{}, MatchNone
	}
	return best, bestTier
}

// betterCandidate applies the documented precedence: tier first, then the
// longer (more specific) token match, then a user definition over a
// detected command — matching MergeCommands' menu semantics. Equal on all
// three keeps the first candidate; genuinely ambiguous equals (two user
// definitions with the same command) stay with the first rather than
// guessing differently per scan.
func betterCandidate(tier MatchTier, score int, user bool, bestTier MatchTier, bestScore int, bestUser bool) bool {
	if bestTier == MatchNone {
		return true
	}
	if tier != bestTier {
		return tier > bestTier
	}
	if score != bestScore {
		return score > bestScore
	}
	return user && !bestUser
}

// entrySequences normalizes one entry for a tier: the typed line's
// segments for MatchTyped, the body's for MatchBody. fullBody reports
// whether the body is a single command (the whole script runs as one
// process) rather than a compound command.
// reports whether the body is a single command (the whole script runs as
// one process) rather than a compound command.
func entrySequences(entry CommandEntry, tier MatchTier) ([][]string, bool) {
	switch tier {
	case MatchTyped:
		return lexCommand(entry.Command), false
	case MatchBody, MatchSegment:
		if entry.Body == "" {
			return nil, false
		}
		segments := lexCommand(entry.Body)
		return segments, len(segments) == 1
	default:
		return nil, false
	}
}

// unwrapInterpreter strips the interpreter around a runner script:
// [node, /…/pnpm.cjs, run, start] → [pnpm, run, start] (verified live
// 2026-11-03: pnpm/yarn run through node; bun runs directly). Non-runner
// children ([node, dist/cli.js, serve]) pass through untouched.
func unwrapInterpreter(argv []string) []string {
	if len(argv) < 3 {
		return argv
	}
	switch runnerAlias(argv[0]) {
	case "node", "bun", "deno", "tsx":
	default:
		return argv
	}
	switch runnerAlias(argv[1]) {
	case "pnpm", "yarn", "npm":
		return append([]string{runnerAlias(argv[1])}, argv[2:]...)
	default:
		return argv
	}
}

// runnerAlias reduces a token to its runner identity: basename first, then
// the known binary spellings of the same tool (vite-plus's bun ships as
// bun.native; pnpm/yarn/npm are .cjs/.js scripts). Unknown tokens return
// their plain basename so exact matches still work. Extend this map only
// with evidence from a real device.
func runnerAlias(token string) string {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(token)), ".exe")
	switch base {
	case "bun.native":
		return "bun"
	case "pnpm.cjs", "pnpm.js", "pnpm.mjs":
		return "pnpm"
	case "yarn.cjs", "yarn.js":
		return "yarn"
	case "npm-cli.cjs", "npm-cli.js":
		return "npm"
	default:
		return base
	}
}

// seqMatch reports whether argv is an instance of the entry's token
// sequence. Pair rules: literal equality, wildcard (unexpanded $VAR on the
// entry side), runner alias (bun.native ↔ bun), and path leniency (one
// side carries a directory: basenames or a path suffix compare — body
// `node dist/cli.js` matches argv `/abs/…/dist/cli.js`). The entry's
// literal `run` token is optional (`pnpm start` ≡ `pnpm run start`, `bun
// daemon` ≡ `bun run daemon`). Trailing argv tokens are extras (`pnpm
// start -- --port 9`); the score counts consumed entry tokens so the
// longer, more specific entry wins a shared prefix.
func seqMatch(entry, argv []string) (int, bool) {
	consumed := 0
	i, j := 0, 0
	for i < len(entry) {
		token := entry[i]
		if token == "run" && (j >= len(argv) || argv[j] != "run") {
			i++ // runner keyword omitted (`pnpm start`, `bun daemon`)
			continue
		}
		if j >= len(argv) {
			return 0, false
		}
		if !pairMatch(token, argv[j]) {
			return 0, false
		}
		consumed++
		i++
		j++
	}
	return consumed, true
}

// pairMatch compares one entry token against one argv token. Pairs that
// involve a directory are compared by pathTokensMatch; plain tokens fall
// back to the runner alias (bun ↔ bun.native), and only then fail.
func pairMatch(entry, arg string) bool {
	if entry == wildToken || entry == arg {
		return true
	}
	if strings.Contains(entry, "/") || strings.Contains(arg, "/") {
		return pathTokensMatch(entry, arg)
	}
	return runnerAlias(entry) == runnerAlias(arg)
}

// pathTokensMatch compares tokens that involve directories: equal paths,
// alias-equal basenames (bun.native ↔ bun under any directory), or one
// path ending at a component boundary with the other (`dist/cli.js` ↔
// `/repo/dist/cli.js`). Basename-level equality is deliberate: entries are
// already scoped to the pane's project root, so a same-named binary in
// another directory is the same tool for title purposes.
func pathTokensMatch(entry, arg string) bool {
	if entry == arg {
		return true
	}
	e, a := runnerAlias(entry), runnerAlias(arg)
	if e == a {
		return true
	}
	return strings.HasSuffix(a, "/"+e) || strings.HasSuffix(e, "/"+a)
}

// lexCommand turns one shell line into command segments of tokens — the
// shapes a process group can actually show. POSIX-lite on purpose:
//
//   - quotes group tokens (`"a b"` is one token), never evaluated
//   - && || ; | & separate segments; a `#` ends the line
//   - leading KEY=VALUE assignments are stripped (the shell consumes them)
//   - redirects (>, >>, 2>, &>, …) drop the operator and its file target
//   - $VAR, ${VAR}, $(…), `…` become wildcards (the argv side carries the
//     expanded value; only "matches anything" is honest)
func lexCommand(line string) [][]string {
	tokens := lexTokens(line)
	var (
		segments [][]string
		current  []string
	)
	flush := func() {
		if len(current) > 0 {
			segments = append(segments, current)
			current = nil
		}
	}
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == segmentMarker {
			flush()
			continue
		}
		if isRedirect(token) {
			// A bare operator swallows its target; a fused `2>&1` stands alone.
			if !fusedRedirect(token) && i+1 < len(tokens) &&
				tokens[i+1] != segmentMarker && !isRedirect(tokens[i+1]) {
				i++
			}
			continue
		}
		if isEnvAssignment(token) && len(current) == 0 {
			continue
		}
		current = append(current, token)
	}
	flush()
	return segments
}

// lexTokens splits one shell line into raw tokens: whitespace separates,
// quotes group, comments end the line, and an operator run (&& || ; | &)
// collapses to one segmentMarker. Operators glued to a word (`a&&b`) stay
// literal — an honest mismatch instead of a wrong split.
func lexTokens(line string) []string {
	var (
		tokens  []string
		current strings.Builder
		quoted  rune
	)
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, expandWildcard(current.String()))
			current.Reset()
		}
	}
	for _, r := range line {
		switch {
		case quoted != 0:
			if r == quoted {
				quoted = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quoted = r
		case r == '#':
			flush()
			return tokens
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		case r == '&' || r == '|' || r == ';':
			// A `&` glued onto a redirect (`2>&1`) belongs to the token,
			// not to the segment boundary.
			if current.Len() > 0 && strings.HasSuffix(current.String(), ">") {
				current.WriteRune(r)
			} else {
				flush()
				for len(tokens) > 0 && tokens[len(tokens)-1] == segmentMarker {
					tokens = tokens[:len(tokens)-1]
				}
				tokens = append(tokens, segmentMarker)
			}
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// expandWildcard marks unshellable expansions as wildcards.
func expandWildcard(token string) string {
	if strings.ContainsAny(token, "$`") {
		return wildToken
	}
	return token
}

// isEnvAssignment reports leading VAR=… tokens (the shell strips them
// before the child sees argv).
func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range token[:eq] {
		if i == 0 && !(r == '_' || isAlpha(r)) {
			return false
		}
		if i > 0 && !(r == '_' || isAlpha(r) || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func isAlpha(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// isRedirect reports redirect operators and fused forms (>, >>, 2>, &>,
// 2>&1); lexCommand drops them — they never appear in a child's argv.
func isRedirect(token string) bool {
	for _, r := range token {
		if r == '<' {
			return true
		}
		if r == '>' {
			// `a>b` is a comparison in some tools, not a shell redirect;
			// only operator-shaped tokens (no leading word characters)
			// count. Flags like `--port>` never occur in argv, and a
			// fused target (`out.txt` after `>`) is handled by the caller.
			return true
		}
	}
	return false
}

// fusedRedirect reports self-contained redirects (`2>&1`) that have no
// separate file target to swallow.
func fusedRedirect(token string) bool {
	return strings.Contains(token, "&") && strings.Contains(token, ">")
}
