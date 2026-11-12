# design/
> L2 | Parent: ../CLAUDE.md

The one source of the desktop / Chat / phone design language (decision 2026-11-07 "Desktop and phone design language", batch authorised 2026-11-08). Edit here, run `node design/gen.mjs`, commit the generated files; `--check` is the read-only drift check (Go test `TestGeneratedDesignFilesAreCurrent`, gate lane `design-drift`, phone `pnpm check:tokens`).

Members:
- `tokens.json` — `tiles` (item-type tile colours, Vault only) and the 19+ colour roles of both appearances (+ `mutedOnMaterial`), the CSS alias table that names them for the Chat page, the pointer/touch tiers of type, shape (radii), control heights and space, and the UI font stacks.
- `icons.json` — the icon registry: id = product meaning, `go` = nativeui vars, `chat` = Chat page keys, `tabler` = the glyph; `glyphs` holds the Tabler outline children (MIT); `stroke` is 1.5 on a pointer, 2 on touch.
- `gen.mjs` — the generator (node, no dependencies; gofmt for Go output). Outputs: `internal/nativeui/design_gen.go` + `icons_gen.go`, `frontend/src/tokens.generated.css` + `icons.generated.ts`, and — when `$SOLCO_MOBILE` or a sibling checkout exists — solco-mobile's `src/theme/tokens.generated.ts` + `icons.generated.ts`, and (`$SOLCO_GIT` or a sibling) solco-git's `internal/nativeui/design_gen.go` (palette and tiers only; its `theme.go` / `design_system.go` are copies of Solco's), and (`$SOLCO_VAULT` or a sibling) solco-vault's `internal/kit/design_gen.go` (the same palette under package `kit` plus the item-type tile colours of `tokens.json` → `tiles`, which no other client reads).

Rules: a hex, radius, type size or icon path written outside these files is a second palette. Brand marks (provider and technology logos in `agent_visual.go` / `proc_brands.go`) are logos, not registry glyphs. The sidebar's opaque non-vibrancy fallback and the tree line stay Go constants (`theme.go`).

[PROTOCOL]: 变更时更新此头部，然后检查 ../CLAUDE.md
