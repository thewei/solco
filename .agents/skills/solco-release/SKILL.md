---
name: solco-release
description: "Cut and publish a Solco release end to end: draft and curate the changelog, bump the version in mygo.config.ts, commit, tag, push, watch the release workflow, publish the draft release, verify the published assets and updater manifests. Use whenever the user says 发版 / 发布新版本 / 出个新版本 / cut a release / release Solco / publish a new version / 打tag发布. The tag is the release — CI builds and signs it; nothing is built or published from the local machine."
---

# Solco Release

Two scripts drive the whole gated flow — changelog draft, sync gates,
version bump, tag, push, CI watch, publish, curated notes, published-asset
verification. Run them from anywhere inside the repository; do not hand-run
the stages.

## Run

```sh
bash .agents/skills/solco-release/scripts/changelog.sh   # 1. draft the section
# 2. curate CHANGELOG.md (see below), remove the draft marker, commit
bash .agents/skills/solco-release/scripts/release.sh [major|minor|patch|x.y.z] [--dry-run] [--redo]
```

Default bump is patch. `--dry-run` runs every read-only gate and prints the
plan without writing anything — prefer it first when the tree looks odd.

## Changelog (the agent curates it)

`changelog.sh` drafts the next version's section at the top of
`CHANGELOG.md` from conventional commits since the last tag, grouped by
prefix, with a `<!-- draft -->` marker. Curation is the deliverable — a
draft must never ship:

- Sections in order: **New** (`feat`), **Fixed** (`fix`), **Improved**
  (`ux`/`refactor`/`perf`), **Internal** (everything else).
- User-facing language: no commit prefixes, no file paths or symbol names;
  lead with the area ("Chat: …"). Merge sibling commits that ship one
  feature into one bullet; drop pure noise (probes, gofmt-only churn).
- ~110 chars per bullet, no trailing periods, biggest impact first.
- Remove the marker when done, commit the file, then run `release.sh`.

`release.sh` extracts the target version's section as the release notes,
snapshots it into `internal/changelog/snapshot.md` (shown by Settings →
About), refuses a missing section, and refuses a section that still carries
the draft marker.

## Stages (each gates the next, `==> N/9`)

1. **preflight** — on `main`, `gh` authenticated, `mygo.config.ts` present.
2. **sync** — refuses when `main` is behind `origin/main`; warns that dirty
   worktree files are NOT in the release (CI builds the tag commit).
3. **version** — reads the current version from `mygo.config.ts` and
   computes the target the same way the workflow validates it.
4. **changelog** — requires a curated `## vX.Y.Z` section in
   `CHANGELOG.md` (no draft marker), snapshots it for the About page, and
   keeps it as the release notes.
5. **tag guard** — a tag that already exists locally is stale and needs
   `--redo`; a tag already on the remote is never moved.
6. **bump + commit** — rewrites the one version line (verified: exactly one
   hit), commits `mygo.config.ts`, `CHANGELOG.md` and the snapshot. Skipped
   when the file already says the target version; in that case a dirty
   snapshot is an error (the tag must carry what About shows).
7. **tag + push** — tags HEAD, pushes `main` and the tag. The tag push is
   what triggers `.github/workflows/release.yml`.
8. **watch** — polls the `release` workflow run for the tag until it
   completes (25 min cap); a failure names the run URL.
9. **publish and verify** — CI uploads every asset to a **draft** release;
   the script checks the required assets first, then publishes the draft
   and replaces the generated notes with the curated section.

Required assets: both macOS DMGs (arm64 + amd64), the Windows installer and
all five `update-*.json` updater manifests the installed apps' updaters read.

## Reading the result

- `done: Solco <version> released at <url>` — the release is live with the
  curated notes, the updater feeds serve it, and the site badge (GitHub
  API, 1 h cache) shows the new version without any site change.
- Any `ERROR:` line names the failed gate. Report it verbatim and stop;
  the script never patches around a failed gate.

## Rules

- The tag is the release: CI owns building, signing
  (`MYGO_UPDATER_PRIVATE_KEY`) and publishing. Never build a release
  artifact locally and never upload one by hand.
- A published tag is immutable. `--redo` only moves a local tag that was
  never pushed; the script refuses when the remote already has it.
- The script pushes `main` — every commit on it ships in the release tree.
  Say so when reporting the result.
- Release notes come from `CHANGELOG.md` (the script replaces the
  workflow's generated notes). Edit the changelog, never the release page.
