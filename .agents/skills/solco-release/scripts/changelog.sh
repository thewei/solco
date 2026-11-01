#!/usr/bin/env bash
#
# Draft the CHANGELOG.md section for the next release from conventional
# commits. The draft is a starting point, never the deliverable: curate it
# by hand (merge sibling commits, reword for users, drop noise), remove the
# draft marker, commit — release.sh refuses a section that still carries
# the marker and refuses to ship without a section at all.
#
# Usage: changelog.sh [x.y.z]
#   x.y.z   the version to draft for (default: current + patch, the same
#           computation release.sh uses)
set -euo pipefail

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
REPO="$(git rev-parse --show-toplevel 2>/dev/null)" || die "run from inside the solco repository"
cd "$REPO"

[ -f mygo.config.ts ] || die "mygo.config.ts missing"
current="$(sed -n 's/.*version: "\([^"]*\)".*/\1/p' mygo.config.ts | head -1)"
[ -n "$current" ] || die "cannot read the version from mygo.config.ts"
if [ $# -ge 1 ]; then
  target="$1"
else
  major="${current%%.*}"; rest="${current#*.}"; minor="${rest%%.*}"; patch="${rest##*.}"
  target="$major.$minor.$((patch+1))"
fi
base="$(git describe --tags --abbrev=0 2>/dev/null || true)"
[ -n "$base" ] || die "no tag to diff from — nothing is released yet"
if grep -q "^## v$(printf '%s' "$target" | sed 's/[.]/\\./g') " CHANGELOG.md 2>/dev/null; then
  die "CHANGELOG.md already has a section for v$target — curate it, or pass a different version"
fi

subjects() { git log --format='%s' "$base"..HEAD | grep -v '^Merge'; }
bullets() { # prefix regex: strip the conventional prefix, bullet the rest
  local pattern="$1"
  subjects | grep -E "^(${pattern})(\([^)]*\))?: " | sed -E "s/^(${pattern})(\([^)]*\))?: //" | sort -u | sed 's/^/- /'
}
section_body="$(mktemp)"
trap 'rm -f "$section_body"' EXIT
{
  printf '## v%s — %s\n\n' "$target" "$(date +%F)"
  printf '<!-- draft: curate by hand (merge, reword, drop noise), then remove this line. release.sh refuses while it is present. -->\n'
  printf '\n### New\n\n';      bullets 'feat' | grep . || true
  printf '\n### Fixed\n\n';    bullets 'fix' | grep . || true
  printf '\n### Improved\n\n'; bullets 'ux|refactor|perf' | grep . || true
  printf '\n### Internal\n\n'
  subjects | grep -vE '^(feat|fix|ux|refactor|perf|Merge)(\([^)]*\))?: ' | sed -E 's/^[a-z]+(\([^)]*\))?: //' | sort -u | sed 's/^/- /' | grep . || true
  printf '\n'
} > "$section_body"

if [ ! -f CHANGELOG.md ]; then
  printf '# Changelog\n\nCurated by hand before every release — grouped into New / Fixed / Improved /\nInternal from the release'"'"'s commits (see `.agents/skills/solco-release`). The\ncurrent release'"'"'s section is snapshotted into `internal/changelog/snapshot.md`\nat release time and shown in Settings → About.\n\n' > CHANGELOG.md
fi
awk -v sect="$section_body" '
  BEGIN { while ((getline line < sect) > 0) section[++n] = line }
  /^## v/ && !done { for (i = 1; i <= n; i++) print section[i]; print ""; done = 1 }
  { print }
  END { if (!done) { for (i = 1; i <= n; i++) print section[i] } }
' CHANGELOG.md > CHANGELOG.md.next && mv CHANGELOG.md.next CHANGELOG.md

printf 'drafted %s into CHANGELOG.md from %s..HEAD\n' "v$target" "$base"
printf 'now curate the section (merge, reword, drop the <!-- draft --> marker), commit, then run release.sh\n'
