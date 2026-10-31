---
name: solco-release
description: "Cut and publish a Solco release end to end: bump the version in mygo.config.ts, commit, tag, push, watch the release workflow, verify the published assets and updater manifests. Use whenever the user says 发版 / 发布新版本 / 出个新版本 / cut a release / release Solco / publish a new version / 打tag发布. The tag is the release — CI builds and signs it; nothing is built or published from the local machine."
---

# Solco Release

One script drives the whole gated flow — sync gates, version bump, tag,
push, CI watch, published-asset verification. Run it from anywhere inside
the repository; do not hand-run the stages.

## Run

```sh
bash .agents/skills/solco-release/scripts/release.sh [major|minor|patch|x.y.z] [--dry-run] [--redo]
```

Default bump is patch. `--dry-run` runs every read-only gate and prints the
plan without writing anything — prefer it first when the tree looks odd.

## Stages (each gates the next, `==> N/8`)

1. **preflight** — on `main`, `gh` authenticated, `mygo.config.ts` present.
2. **sync** — refuses when `main` is behind `origin/main`; warns that dirty
   worktree files are NOT in the release (CI builds the tag commit).
3. **version** — reads the current version from `mygo.config.ts` and
   computes the target the same way the workflow validates it.
4. **tag guard** — a tag that already exists locally is stale and needs
   `--redo`; a tag already on the remote is never moved.
5. **bump + commit** — rewrites the one version line (verified: exactly one
   hit), commits only `mygo.config.ts`. Skipped when the file already says
   the target version.
6. **tag + push** — tags HEAD, pushes `main` and the tag. The tag push is
   what triggers `.github/workflows/release.yml`.
7. **watch** — polls the `release` workflow run for the tag until it
   completes (25 min cap); a failure names the run URL.
8. **verify** — the release is not a draft and carries both macOS DMGs
   (arm64 + amd64), the Windows installer and all five `update-*.json`
   updater manifests the installed apps' updaters read.

## Reading the result

- `done: Solco <version> released at <url>` — the release is live, the
  updater feeds serve it, and the site badge (GitHub API, 1 h cache) shows
  the new version without any site change.
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
- Release notes come from `--generate-notes` on the draft release; edit
  them on GitHub afterwards if a curated changelog is wanted.
