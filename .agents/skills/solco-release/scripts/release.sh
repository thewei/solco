#!/usr/bin/env bash
#
# Cut a Solco release end to end: bump the version in mygo.config.ts, commit,
# tag, push, watch the release workflow, verify the published assets.
#
# The tag is the release. CI (.github/workflows/release.yml) checks out the
# tag's tree, validates it against mygo.config.ts, builds all three platforms
# with `go tool mygo build -upload` and publishes the signed release with the
# updater manifests. Nothing here builds or signs locally.
#
# Usage: release.sh [major|minor|patch|x.y.z] [--dry-run] [--redo]
#   --dry-run  run every read-only gate, print the plan, write nothing
#   --redo     move an existing LOCAL tag to the new commit; a tag that is
#              already on the remote is never moved
set -euo pipefail

step() { printf '\n==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

REPO="$(git rev-parse --show-toplevel 2>/dev/null)" || die "run from inside the solco repository"
cd "$REPO"

DRY_RUN=0; REDO=0; BUMP=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --redo) REDO=1 ;;
    major|minor|patch) BUMP="$arg" ;;
    v[0-9]*) BUMP="${arg#v}" ;;
    [0-9]*) BUMP="$arg" ;;
    *) die "unknown argument: $arg (want major|minor|patch|x.y.z, --dry-run, --redo)" ;;
  esac
done
BUMP="${BUMP:-patch}"
case "$BUMP" in
  major|minor|patch) ;;
  *) printf '%s' "$BUMP" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || die "version must look like x.y.z (got: $BUMP)" ;;
esac
step "1/8 preflight"
branch="$(git branch --show-current)"
[ "$branch" = "main" ] || die "release from main, not '$branch'"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated (gh auth login)"
[ -f mygo.config.ts ] || die "mygo.config.ts missing"

step "2/8 sync"
git fetch origin main --quiet
behind="$(git rev-list --count main..origin/main)"
[ "$behind" -eq 0 ] || die "main is $behind commits behind origin/main — pull first"
dirty="$(git status --porcelain | wc -l | tr -d ' ')"
if [ "$dirty" -gt 0 ]; then
  printf 'NOTE: %s dirty paths in the worktree; they are NOT part of the release (CI builds the tag commit)\n' "$dirty"
fi

step "3/8 version"
current="$(sed -n 's/.*version: "\([^"]*\)".*/\1/p' mygo.config.ts | head -1)"
[ -n "$current" ] || die "cannot read the version from mygo.config.ts"
major="${current%%.*}"; rest="${current#*.}"; minor="${rest%%.*}"; patch="${rest##*.}"
case "$BUMP" in
  major) target="$((major+1)).0.0" ;;
  minor) target="$major.$((minor+1)).0" ;;
  patch) target="$major.$minor.$((patch+1))" ;;
  *) target="$BUMP" ;;
esac
if [ "$target" = "$current" ]; then
  printf 'version already %s (no bump commit; the tag carries it)\n' "$current"
else
  printf '%s -> %s\n' "$current" "$target"
fi

step "4/8 tag guard"
tag="v$target"
remote_tag="$(git ls-remote --tags origin "refs/tags/$tag" | wc -l | tr -d ' ')"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  if [ "$remote_tag" -gt 0 ]; then
    die "$tag is already on the remote — a published tag is never moved"
  fi
  [ "$REDO" -eq 1 ] || die "tag $tag already exists locally (at $(git rev-parse --short "$tag")) and is stale — rerun with --redo to move it onto the new commit, or pick a higher version"
  printf 'moving stale local tag %s onto the new commit\n' "$tag"
elif [ "$remote_tag" -gt 0 ]; then
  die "$tag already exists on the remote — pick a higher version"
fi

if [ "$DRY_RUN" -eq 1 ]; then
  step "dry-run plan"
  printf 'would set version %s, commit mygo.config.ts, tag %s at HEAD, push main + %s, watch the release workflow, require the %s assets\n' "$target" "$tag" "$tag" "$tag"
  printf 'dry-run: nothing was written\n'
  exit 0
fi

step "5/8 bump + commit"
if [ "$target" != "$current" ]; then
  perl -pi -e 's/^(\s*version:\s*)"[^"]*"/${1}"'"$target"'"/' mygo.config.ts
  hits="$(grep -c "version: \"$target\"" mygo.config.ts || true)"
  [ "$hits" -eq 1 ] || die "version rewrite landed $hits times, expected 1 — inspect mygo.config.ts"
  git add mygo.config.ts
  git commit -m "chore(release): v$target"
else
  printf 'no bump commit needed\n'
fi

step "6/8 tag + push"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  git tag -f "$tag" HEAD
else
  git tag "$tag" HEAD
fi
git push origin main
git push origin "refs/tags/$tag"

step "7/8 watch the release workflow"
run_id=""
deadline=$(( $(date +%s) + 1500 ))
while :; do
  run_id="$(gh run list --workflow=release --branch "$tag" --limit 1 --json databaseId --jq '.[0].databaseId // empty' 2>/dev/null || true)"
  [ -n "$run_id" ] && break
  [ "$(date +%s)" -lt "$deadline" ] || die "no release run appeared for $tag within 25 min (check the actions tab)"
  sleep 10
done
printf 'run %s\n' "$(gh run view "$run_id" --json url --jq .url)"
conclusion=""
while :; do
  read -r status conclusion <<<"$(gh run view "$run_id" --json status,conclusion --jq '"\(.status) \(.conclusion // "-")"' 2>/dev/null || echo 'running -')"
  [ "$status" = "completed" ] && break
  [ "$(date +%s)" -lt "$deadline" ] || die "release run $run_id still running after 25 min: $(gh run view "$run_id" --json url --jq .url)"
  sleep 15
done
[ "$conclusion" = "success" ] || die "release run failed ($conclusion): $(gh run view "$run_id" --json url --jq .url)"

step "8/8 verify the published release"
draft="$(gh release view "$tag" --json isDraft --jq .isDraft)"
[ "$draft" = "false" ] || die "$tag is still a draft"
assets="$(gh release view "$tag" --json assets --jq '[.assets[].name] | join("\n")')"
for want in \
  "Solco.$target.arm64.dmg" \
  "Solco.$target.amd64.dmg" \
  "Solco.Setup.$target.amd64.exe" \
  update-darwin-arm64.json \
  update-darwin-amd64.json \
  update-windows-amd64.json \
  update-linux-amd64.json \
  update-linux-arm64.json; do
  grep -qx "$want" <<<"$assets" || die "missing release asset: $want"
done

printf '\ndone: Solco %s released at https://github.com/thewei/solco/releases/tag/%s\n' "$target" "$tag"
printf 'updater feeds are live; the site badge shows %s within its 1 h cache\n' "$tag"
