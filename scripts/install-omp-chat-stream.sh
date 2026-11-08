#!/bin/sh
# [INPUT]: repo-owned scripts/omp-chat-stream.ts, the user's ~/.omp/agent/extensions directory
# [OUTPUT]: --check / --install / --uninstall of Solco's separate OMP companion extension
# [POS]: explicit, idempotent extension management; never edits Herdr-managed files or a running OMP session
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
set -eu

src="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)/omp-chat-stream.ts"
dst_dir="${HOME:?HOME must be set}/.omp/agent/extensions"
dst="$dst_dir/solco-chat-stream.ts"
mode="${1:---check}"

is_ours() {
  [ ! -L "$dst" ] && [ -f "$dst" ] &&
    grep -Fq '// Managed by Solco, NOT by Herdr.' "$dst"
}

case "$mode" in
  --check)
    if [ ! -e "$dst" ] && [ ! -L "$dst" ]; then
      printf 'missing: %s\n' "$dst"
      exit 1
    fi
    if ! is_ours; then
      printf 'foreign extension at %s; refusing to manage\n' "$dst" >&2
      exit 2
    fi
    if cmp -s "$src" "$dst"; then
      printf 'current: %s\n' "$dst"
    else
      printf 'outdated: %s\n' "$dst"
      exit 1
    fi
    ;;
  --install)
    if [ -L "$dst_dir" ]; then
      echo 'extension directory is a symlink; refusing to install' >&2
      exit 2
    fi
    mkdir -p "$dst_dir"
    if [ -e "$dst" ] || [ -L "$dst" ]; then
      if ! is_ours; then
        printf 'foreign extension at %s; refusing to overwrite\n' "$dst" >&2
        exit 2
      fi
      if cmp -s "$src" "$dst"; then
        printf 'already current: %s\n' "$dst"
        exit 0
      fi
    fi
    tmp="$(mktemp "$dst_dir/.solco-chat-stream.XXXXXX")"
    trap 'rm -f "$tmp"' EXIT HUP INT TERM
    cp "$src" "$tmp"
    chmod 0644 "$tmp"
    mv -f "$tmp" "$dst"
    trap - EXIT HUP INT TERM
    printf 'installed: %s\nRestart OMP to load the companion extension.\n' "$dst"
    ;;
  --uninstall)
    if [ ! -e "$dst" ] && [ ! -L "$dst" ]; then
      echo 'not installed'
      exit 0
    fi
    if ! is_ours; then
      printf 'foreign extension at %s; refusing to remove\n' "$dst" >&2
      exit 2
    fi
    rm -- "$dst"
    printf 'removed: %s\n' "$dst"
    ;;
  *)
    echo 'Usage: scripts/install-omp-chat-stream.sh [--check|--install|--uninstall]' >&2
    exit 2
    ;;
esac
