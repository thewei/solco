#!/bin/sh
# [INPUT]: sandbox HOME and the repo's extension installer
# [OUTPUT]: idempotency, no Herdr file overwrite, foreign-owned file and symlink rejection, safe uninstall
# [POS]: isolated installer contract test, never touches user HOME
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
set -eu
base="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT HUP INT TERM
export HOME="$sandbox/home"
mkdir -p "$HOME/.omp/agent/extensions"
directory="$HOME/.omp/agent/extensions"
src="$base/omp-chat-stream.ts"
installer="$base/install-omp-chat-stream.sh"

if "$installer" --check >/dev/null 2>&1; then echo 'missing extension accepted' >&2; exit 1; fi
"$installer" --install
"$installer" --check
"$installer" --install
cmp "$src" "$directory/solco-chat-stream.ts"

printf '// Managed by Solco, NOT by Herdr.\n// stale\n' > "$directory/solco-chat-stream.ts"
if "$installer" --check >/dev/null 2>&1; then echo 'outdated extension accepted' >&2; exit 1; fi
"$installer" --install
cmp "$src" "$directory/solco-chat-stream.ts"

printf 'unmanaged content\n' > "$directory/solco-chat-stream.ts"
if "$installer" --install >/dev/null 2>&1; then echo 'foreign extension overwritten' >&2; exit 1; fi
grep -q 'unmanaged content' "$directory/solco-chat-stream.ts"
if "$installer" --uninstall >/dev/null 2>&1; then echo 'foreign extension removed' >&2; exit 1; fi

rm "$directory/solco-chat-stream.ts"
ln -s "$src" "$directory/solco-chat-stream.ts"
if "$installer" --install >/dev/null 2>&1; then echo 'symlink destination accepted' >&2; exit 1; fi
rm "$directory/solco-chat-stream.ts"
"$installer" --install
printf 'Herdr-owned extension untouched\n' > "$directory/herdr-omp-agent-state.ts"
"$installer" --uninstall
grep -q 'Herdr-owned extension untouched' "$directory/herdr-omp-agent-state.ts"
[ ! -e "$directory/solco-chat-stream.ts" ]
echo 'OMP extension installer contract: PASS'
