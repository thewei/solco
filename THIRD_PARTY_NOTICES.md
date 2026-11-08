# Third-Party Notices

Solco is licensed under the GNU General Public License v3.0 (see [LICENSE](LICENSE)). This file retains the notices required by third-party material distributed with the application; embedded third-party material remains governed by its own upstream license as noted below.

## Ghostty / libghostty-vt

- Project: `ghostty-org/ghostty` (terminal semantics library, extracted as a static archive; see [`vendor/ghostty-vt/PIN.md`](vendor/ghostty-vt/PIN.md))
- License: MIT
- Use in Solco: vendored `libghostty-vt.a` provides the terminal model, VT parser, key/mouse encoders, and selection semantics that back the hosted terminal surface.

## GPUI

- Project: `zed-industries/zed` `gpui` crate v0.2.2, vendored under [`vendor/gpui/`](vendor/gpui/) with a local frame-pacing patch ([`vendor/gpui/PACING-PATCH.md`](vendor/gpui/PACING-PATCH.md))
- License: Apache-2.0 (with notices retained in the vendored tree)
- Use in Solco: native macOS application framework (windowing, rendering, input).

## MIT-derived history implementation

- Copyright: © 2026 Corey Chiu
- License: MIT
- Use in Solco: portions of the read-only Agent-history models, adapters, scanner/catalog/watcher strategy, and resume CLI semantics.

Source files containing these portions retain an SPDX MIT marker and the upstream copyright notice.

## GPUI Component Assets

- Project/package: `longbridge/gpui-component` / `gpui-component-assets` v0.5.1
- License: Apache-2.0
- Use in Solco: default embedded icon bundle for `gpui-component::IconName`.

## Lucide

- Project/package: Lucide / `lucide-static` v0.462.0
- License: ISC, with Feather-derived portions under MIT
- Use in Solco: Solco-owned embedded interface SVG assets.

Copied SVGs retain their upstream license notice.

## Lobe Icons

- Project: `lobehub/lobe-icons`
- Copyright: © 2023 LobeHub
- License: MIT
- Use in Solco: embedded Agent brand artwork.

Agent names, logos, and trademarks remain the property of their respective owners. Solco does not claim ownership of those marks.

## Markdown preview components

Go modules linked into the app for the rendered Markdown view (raw HTML, math, diagrams), and the fonts two of them embed. Each keeps its own license; the full texts ship in the module sources at the paths named.

- `github.com/go-tex/math` v0.50.0 — BSD-3-Clause, © 2026 the go-tex/math authors (typesets TeX math). It embeds **STIX Two Math**, SIL Open Font License 1.1, © 2001–2021 The STIX Fonts Project Authors (<https://github.com/stipub/stixfonts>); text: `STIXTwoMath-OFL.txt` in the module.
- `tuios.dev/sketch` v0.4.0 — MIT, © 2026 kepano (pele, the TypeScript original) and Gaurav Gosain (the Go port) (draws Mermaid diagrams). Its `raster` package embeds subsets of **Arimo** 1.33, SIL Open Font License 1.1, © 2026 The Arimo Project Authors (<https://github.com/googlefonts/Arimo>), and **Go Mono**, © 2016 Bigelow & Holmes Inc., under the Go project's BSD-3-Clause license; texts: `raster/fonts/NOTICE` in the module.
- `golang.org/x/net` v0.60.0 (the `html` parser) — BSD-3-Clause, © The Go Authors.

## External runtime dependencies

The following are runtime dependencies installed separately and are not distributed with this repository: `herdr` (the backend runtime) and `lazygit` (used by the right-panel Git surface).
