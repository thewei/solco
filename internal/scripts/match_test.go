package scripts

import (
	"reflect"
	"testing"
)

// The fixtures mirror the live processes the alignment was built against
// (2026-11-03 probes of the default Herdr session): bun.native wrappers,
// the node+pnpm.cjs interpreter chain, and devspace/mcp-feedback bodies.
func matchEntries() []CommandEntry {
	return []CommandEntry{
		{Name: "start", Command: "pnpm run start", Body: "node dist/cli.js serve", Source: "package.json"},
		{Name: "dev", Command: "pnpm run dev", Body: "tsx scripts/dev.ts", Source: "package.json"},
		{Name: "dev:seed", Command: "pnpm run dev:seed", Body: "tsx scripts/dev-state.ts seed", Source: "package.json"},
		{Name: "dev:reset", Command: "pnpm run dev:reset", Body: "tsx scripts/dev-state.ts reset", Source: "package.json"},
		{Name: "build", Command: "pnpm run build", Body: "pnpm clean && pnpm build:app && tsc -p tsconfig.build.json", Source: "package.json"},
		{Name: "build:app", Command: "pnpm run build:app", Body: "vite build", Source: "package.json"},
		{Name: "daemon", Command: "bun run daemon", Body: "bun run src/daemon.ts", Source: "package.json"},
		{Name: "watch", Command: "bun run dev", Body: "bun run --watch src/daemon.ts", Source: "package.json"},
		{Name: "server.up", Command: "npm run start", Body: "npm run start", Source: "script", User: true},
		{Name: "caddy.up", Command: "caddy run --config Caddyfile", Body: "caddy run --config Caddyfile", Source: "script", User: true},
		{Name: "make.up", Command: "make build", Body: "make build", Source: "script", User: true},
	}
}

func TestMatchArgvTypedWrappers(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"bun wrapper with aliased binary path", []string{"/Users/x/.pm/bun/1.3/bun/bin/bun.native", "run", "daemon"}, "daemon"},
		{"pnpm through the node interpreter", []string{"node", "/Users/x/.pm/pnpm/11/pnpm/bin/pnpm.cjs", "run", "start"}, "start"},
		{"omitted run keyword", []string{"pnpm", "start"}, "start"},
		{"bun runs a script by bare name", []string{"bun", "daemon"}, "daemon"},
		{"trailing passthrough extras", []string{"pnpm", "run", "start", "--", "--port", "9"}, "start"},
		{"make target", []string{"/opt/homebrew/bin/make", "build"}, "make.up"},
		{"user definition typed line", []string{"npm", "run", "start"}, "server.up"},
		{"absolute config path matches the definition's relative one", []string{"/opt/homebrew/bin/caddy", "run", "--config", "/Users/me/Workspaces/tools/jenkins-mtls/Caddyfile"}, "caddy.up"},
	}
	for _, tc := range cases {
		entry, tier := MatchArgv(matchEntries(), [][]string{tc.argv})
		if tier != MatchTyped || entry.Name != tc.want {
			t.Errorf("%s: got %q/%v, want typed %q", tc.name, entry.Name, tier, tc.want)
		}
	}
}

func TestMatchArgvBodyChildren(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"devspace server child", []string{"node", "dist/cli.js", "serve"}, "start"},
		{"child carries an absolute path", []string{"node", "/Users/me/Workspaces/acme/devspace/dist/cli.js", "serve"}, "start"},
		{"seed disambiguates from reset", []string{"tsx", "scripts/dev-state.ts", "seed"}, "dev:seed"},
		{"reset disambiguates from seed", []string{"tsx", "scripts/dev-state.ts", "reset"}, "dev:reset"},
		{"simple dev body", []string{"tsx", "scripts/dev.ts"}, "dev"},
		{"bun watch body with wrapper alias", []string{"/pm/bun/bin/bun.native", "run", "--watch", "src/daemon.ts"}, "watch"},
		{"direct vite invocation", []string{"/node/.bin/vite", "build"}, "build:app"},
	}
	for _, tc := range cases {
		entry, tier := MatchArgv(matchEntries(), [][]string{tc.argv})
		if tier != MatchBody || entry.Name != tc.want {
			t.Errorf("%s: got %q/%v, want body %q", tc.name, entry.Name, tier, tc.want)
		}
	}
}

func TestMatchArgvTierPrecedence(t *testing.T) {
	// The wrapper explains the pane even though the child's body belongs to
	// another entry: `pnpm run build` runs `vite build` (build:app's body),
	// yet the pane's title is build.
	entry, tier := MatchArgv(matchEntries(), [][]string{
		{"node", "/pm/pnpm/bin/pnpm.cjs", "run", "build"},
		{"/node/.bin/vite", "build"},
	})
	if tier != MatchTyped || entry.Name != "build" {
		t.Fatalf("got %q/%v, want typed build", entry.Name, tier)
	}

	// Without the wrapper the body child resolves to the specific script.
	entry, tier = MatchArgv(matchEntries(), [][]string{{"/node/.bin/vite", "build"}})
	if tier != MatchBody || entry.Name != "build:app" {
		t.Fatalf("got %q/%v, want body build:app", entry.Name, tier)
	}
}

func TestMatchArgvCompoundBodyIsSegmentTier(t *testing.T) {
	// A compound body's child claims one segment; the tier says so without
	// promoting the compound script above a whole-body match.
	entry, tier := MatchArgv(matchEntries(), [][]string{{"pnpm", "clean"}})
	if tier != MatchSegment || entry.Name != "build" {
		t.Fatalf("got %q/%v, want segment build", entry.Name, tier)
	}
}

func TestMatchArgvUserWinsCommandTie(t *testing.T) {
	entries := []CommandEntry{
		{Name: "detected.dev", Command: "npm run dev", Body: "vite", Source: "package.json"},
		{Name: "my.dev", Command: "npm run dev", Body: "vite", Source: "script", User: true},
	}
	entry, tier := MatchArgv(entries, [][]string{{"npm", "run", "dev"}})
	if tier != MatchTyped || entry.Name != "my.dev" {
		t.Fatalf("got %q/%v, want typed my.dev", entry.Name, tier)
	}
}

func TestMatchArgvAmbiguityAndHonestFallback(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		// devspace's test and test:package-install share the whole flag
		// prefix; only the unexpandable glob distinguishes them, and a
		// glob-expanded argv matches neither exactly.
		{"glob-expanded test argv", []string{"tsx", "--test", "--test-concurrency=1", "src/a.test.ts", "src/b.test.ts"}},
		{"unrelated process", []string{"python3", "-m", "http.server"}},
		{"prefix that extends a token", []string{"make", "buildall"}},
		{"empty argv", nil},
	}
	for _, tc := range cases {
		entry, tier := MatchArgv(matchEntries(), [][]string{tc.argv})
		if tier != MatchNone || entry.Name != "" {
			t.Errorf("%s: got %q/%v, want none", tc.name, entry.Name, tier)
		}
	}
}

func TestMatchArgvLexesShellShapes(t *testing.T) {
	entries := []CommandEntry{
		{Name: "lint", Command: "pnpm run lint", Body: `NODE_ENV=test eslint . > out.log 2>&1`, Source: "package.json"},
		{Name: "deploy", Command: "pnpm run deploy", Body: `vite build --outDir $OUT && up "$NAME"`, Source: "package.json"},
		{Name: "clean", Command: "pnpm run clean", Body: `node -e "require('node:fs').rmSync('dist', { recursive: true, force: true })"`, Source: "package.json"},
	}
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"env prefix and redirects dropped", []string{"eslint", "."}, "lint"},
		{"wildcard expansion matches anything", []string{"vite", "build", "--outDir", "/tmp/whatever"}, "deploy"},
		{"quoted strings stay one token", []string{"node", "-e", "require('node:fs').rmSync('dist', { recursive: true, force: true })"}, "clean"},
	}
	for _, tc := range cases {
		entry, tier := MatchArgv(entries, [][]string{tc.argv})
		// deploy's body is compound (… && up …): its segment claims the
		// Segment tier; lint and clean have single-command bodies.
		if entry.Name != tc.want {
			t.Errorf("%s: got %q/%v, want %q", tc.name, entry.Name, tier, tc.want)
		}
		if entry.Name == "deploy" && tier != MatchSegment {
			t.Errorf("deploy compound body must claim Segment, got %v", tier)
		}
		if (entry.Name == "lint" || entry.Name == "clean") && tier != MatchBody {
			t.Errorf("%s single body must claim Body, got %v", entry.Name, tier)
		}
	}
}

func TestLexCommandSegments(t *testing.T) {
	got := lexCommand(`pnpm clean && pnpm build:app || npm run fallback; vite build | tee log`)
	want := [][]string{
		{"pnpm", "clean"},
		{"pnpm", "build:app"},
		{"npm", "run", "fallback"},
		{"vite", "build"},
		{"tee", "log"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	if got := lexCommand(`echo done # trailing comment > dropped`); !reflect.DeepEqual(got, [][]string{{"echo", "done"}}) {
		t.Fatalf("comment handling = %v", got)
	}
	if got := lexCommand(``); got != nil {
		t.Fatalf("empty line = %v", got)
	}
}

func TestUnwrapInterpreter(t *testing.T) {
	cases := [][]string{
		{"node", "/pm/pnpm/bin/pnpm.cjs", "run", "start"},
		{"bun", "/pm/yarn/bin/yarn.cjs", "install"},
	}
	wants := [][]string{
		{"pnpm", "run", "start"},
		{"yarn", "install"},
	}
	for i, argv := range cases {
		if got := unwrapInterpreter(argv); !reflect.DeepEqual(got, wants[i]) {
			t.Errorf("case %d: unwrap = %v, want %v", i, got, wants[i])
		}
	}
	// Non-runner children pass through untouched.
	keep := []string{"node", "dist/cli.js", "serve"}
	if got := unwrapInterpreter(keep); !reflect.DeepEqual(got, keep) {
		t.Fatalf("plain child rewritten: %v", got)
	}
}
