# Local patch over mygo v0.8.1

Five deltas, re-applied by hand after every pin bump. They live outside
Solco and stay as small as deletion allows. The numbering keeps its gap:
the former Delta 2 (a field focuses its input) is gone — upstream #793
ships the same delegation (`ui/input.go` `Focus` hands the focus to
`focusTo`, set in `ui/combobox.go` `field`), and the vendored copy took
upstream's version wholesale.

## Delta 6: the tray popover follows the tray anchor's space (2026-11-04)

macOS only. A focusable tray flyout shows as an `NSPopover` whose window
AppKit creates at `showRelativeToRect` and we adopt (`adoptPopoverWindow`).
With Delta 3's secondary-display anchoring the anchor is the auxiliary
`trayAnchorWin` (`MoveToActiveSpace` + full-screen auxiliary), placed on
the active space of the display the icon was clicked on. `showPopover`
also activates the app, and that activation can switch the clicked
display's space (a fullscreen app occupies it, the app's window lives in
another). The adopted popover window carried the default collection
behavior, so it stayed behind on the space it was placed in while the
activation flipped the display: the panel vanished and reappeared
continuously (Solco 2026-11-04 multi-display tray flicker report; user
reproduces only with a fullscreen app on the clicked display). The fix
gives the adopted window of a tray flyout the same triple the non-popover
flyout panel already gets in `createFlyout` — `MoveToActiveSpace`,
`IgnoresCycle`, full-screen auxiliary — so it re-homes with the anchor
when the space switches. Parent-anchored popovers keep the default
behavior, which is correct for a popover of a visible window.

Site: one guarded `setCollectionBehavior:` in `adoptPopoverWindow`
(`internal/darwin/popover.go`).

## Delta 5: the page's bridge posts through the scheme handler (2026-11-04)

macOS only. `WKScriptMessage.body` is deserialized by WebKit inside Solco's
own process, in a JavaScriptCore VM whose "JSC Heap Collector Thread" traps
(`EXC_BREAKPOINT` at one fixed JavaScriptCore address) after a few tens of
thousands of messages; a flood of page→Go calls reproduces it in 3–12
minutes and it ended long-running sessions after about an hour (five
reports on 2026-11-04). `JSC_useConcurrentGC=0` did not change it. A page
served from the app's own `mygo:` origin therefore sends each bridge message
as one serial `fetch("/__mygo/msg", {method:"POST"})` that the existing
scheme handler delivers (the body never enters that VM); a refusal falls
back to `postMessage` for good, so a build without the Go side keeps
working. Only the page→Go direction changes — replies, events and channels
still travel through `evaluateJavaScript` — and every message still passes
the per-window secret check in `handleMessage` and runs on the main thread.

Sites: `protocol.go` (`serveBridgeMessage`, called from `serveScheme`),
`packages/bridge/src/bridge.ts` (`viaFetch`) and its generated
`internal/bridge/bridge.js` (the fetch transport). Evidence:
`docs/debug/2026-11-04-jsc-heap-collector-crash.md`.

## Delta 4: terminal OSC 7501 program status (2026-10-15)

`plugins/terminal`: adds `Options.OnProgramStatus func(report []byte)` —
the body bytes of one OSC 7501 report (Program Status Protocol, spec rev
0.4), framed by a small scanner (`osc7501.go`) that rides the same output
bytes libghostty-vt reads. The emulator core ignores the sequence as an
unknown OSC; the scanner knows only the framing (OSC `7501;` through ST,
4096-byte cap, chunk-safe). With the callback set the terminal answers the
protocol's feature-detection query `OSC 7501 ; ? ST` with the full fixed
state and kind lists; without it, support is never claimed. The fork also
carries `Options.OnProgramStatusControl` (the authoritative OSC `133;A`
prompt and control reports, with `ProgramStatusControl`) — recorded here
since 2026-11-07; it predates this file's v0.6.4 record. Pairs, records,
lifetime and merge policy live in the embedding app (herdr-client
`internal/agentstatus`), which owns the protocol semantics.

Reason: Solco agent panes — Claude Code 2.1.295+ emits native status
reports (working/blocked/done/idle/error) that must reach the pane header
without hooks; agents without the protocol keep the hook fallback.

Sites: `plugins/terminal/osc7501.go` (new scanner + tests), `Options`
fields and the doc comments in `plugins/terminal/terminal.go`, the `events`
struct, the feed in `Terminal.write`, and the dispatch in `Terminal.fire`.

## Delta 1: terminal `Options.LocalDragSelect`

`plugins/terminal`: adds `Options.LocalDragSelect` — with it set, the
primary-button drag and the right click stay local to the view (text
selection, context menu) even when the program takes the mouse; the wheel
keeps reporting to the program.

Reason: Solco F146/F147 — scrollback-backed panes (shells, agy) need
plain-drag selection and a right-click menu while the daemon's attach
renderer force-enables mouse tracking. Alt-screen agent TUIs (pi) keep the
option unset and report everything.

Sites: `plugins/terminal/terminal.go` (`Options` field) and a 4-line guard
at the top of `view.pointerEvent`.

## Delta 3: multi-display tray flyout anchoring on macOS (2026-10-12)

`internal/darwin`: in multi-monitor setups, macOS status items (`NSStatusItem`)
reside physically in the primary screen's window (`screens[0]`). When the user
clicks the tray icon while operating on a secondary display, the click event's
mouse position identifies the target display; `tray.recordClick` computes the
corresponding menu-bar anchor on that display, `tray.Bounds` projects that anchor,
and `w.popoverAnchor` binds an auxiliary transparent anchor window on that display
so that the native `NSPopover` (or `PlaceFlyout`) renders directly on the operated
screen without jumping to the primary display and stealing focus.

Reason: Solco multi-monitor workflow — clicking the status tray while working
in an editor on a secondary monitor must display the quick panel on that monitor
rather than dragging the window and cursor focus over to the main screen.

2026-11-04 extension: `recordClick` records the primary screen's clicks too
(the loop used to start at screen 1), and on a multi-display Mac the popover
always anchors to `trayAnchorWin`. The real status-item button's hosting
window is AppKit-owned and gets re-homed across displays and off-screen
strips as focus, fullscreen and mirroring states change; a popover anchored
to it lands wherever that window currently sits — the panel jumped to the
clicked display's neighbour's top-left corner, partially off-screen
(Solco 2026-11-04 report: content on the secondary screen, tray clicked on
the primary, panel seen at the secondary's corner; window server sampled it
at the status-bar layer, off the display's corner). Anchoring to our own
recorded position on every display makes the panel appear under the icon
that was clicked and stay there.

Sites: `clickScreen` and `clickAnchor` tracking + `recordClick` in
`internal/darwin/menu.go`; `trayAnchorWin` and secondary-display anchoring +
dismissal cleanup in `internal/darwin/popover.go`; `trayAnchorWin` field in
`internal/darwin/window.go`.

## Upgrade path

Rebased onto v0.8.1 on 2026-11-07 via a three-way merge (ours =
v0.6.4+delta, base = pristine v0.6.4, theirs = pristine v0.8.1; 421
upstream commits). Upstream shipped Delta 2 as #793, so the vendored
copy drops it; upstream's `focusTo` declaration in `ui/element.go`'s
a11y field group replaces ours in the text group (the duplicate the
merge kept by accident was deleted by hand). Conflict resolutions:

- `ui/input.go`, `ui/layoutcache_test.go` — took upstream wholesale:
  ours' only change was the Delta 2 delegation and the `focusTo` entry
  in `TestLayoutInputs`' other list.
- `internal/darwin/window.go` — the window struct keeps ours'
  `trayAnchorWin` beside upstream's new `popoverHeld`.
- `internal/darwin/menu.go` — the tray struct keeps ours' `clickScreen`/
  `clickAnchor` beside upstream's `clicked`/`of`; `mygoTrayClicked`
  calls `t.recordClick()` and `t.noteClick(ev)` together; `Bounds`
  returns the recorded anchor first and upstream's `t.frame()` (the
  clicked copy) otherwise; upstream's `secondaryClick` (Control-click on
  the tray icon) rides along.
- `protocol.go` — additive: ours' `serveBridgeMessage` early return
  ahead of upstream's caller/page request context.
- `internal/bridge/bridge.js` and `internal/windows/keys.js` —
  regenerated with `cd packages/bridge && bun run build.ts` after the
  merge of `packages/bridge/src/bridge.ts`.

`internal/darwin/popover.go`, the `plugins/terminal` files and
`bridge.ts` auto-merged cleanly; all five deltas were verified present
in the merged tree before commit. Environment note: upstream's
`TestAssetCatalog` (`cmd/mygo`) fails in any extracted checkout — it
fails identically on pristine v0.8.1 (it audits embedded inputs against
the full repo shape) — not a delta regression; the v0.6.4 era also listed
`TestListBuildInputs`, which passes at v0.8.1.

When an upstream mygo release ships these fixes, delete the
`replace github.com/egoist/mygo => ./third_party/mygo` directive in `go.mod`,
bump the pin, and delete this checkout. Solco code already uses only the
public option and needs no changes.

Delta vs v0.8.1: an `Options` field + a 4-line guard in `view.pointerEvent`;
multi-display tray anchoring in `internal/darwin/{menu,popover,window}.go`
plus the popover's guarded collection behavior; the OSC 7501 scanner files,
two `Options` fields, the `events` fields and the feed/fire hooks in
`plugins/terminal`; `serveBridgeMessage` in `protocol.go` and the fetch
transport in `bridge.ts`/`internal/bridge/bridge.js`.

## Standing rule for the replace (moved word for word from AGENTS.md and CLAUDE.md, 2026-11-11)

- `go.mod` carries a temporary, user-approved `replace github.com/egoist/mygo => ./third_party/mygo` (v0.8.1 plus five vendored deltas re-applied on top: F146/F147 terminal `Options.LocalDragSelect`, multi-display tray flyout anchoring, the tray popover following the tray anchor's space (2026-11-04 fullscreen-display flicker), OSC 7501 program-status (`Options.OnProgramStatus`), and the page bridge posting through the scheme handler instead of `WKScriptMessageHandler` (2026-11-04, JavaScriptCore heap-collector crash)). Do not remove the replace or add a second framework source; bump the pin and delete `third_party/mygo` only once upstream ships all five (the former field-focus delegation, WIX-021, is upstream already). See `third_party/mygo/LOCAL-PATCH.md`. Upstream v0.5.x additions stay unadopted until a task takes them: the Flyout API backs the quick panel (tray + header Agent popover), while `ui.WebView` (official Native-UI-embeddable WebView) does not by itself unfreeze the WebView presentation freeze — that needs an explicit product decision.

go.mod + go.sum + mygo.config.ts - Go toolchain (1.27.3), MyGo pin (v0.8.1 + replace ./third_party/mygo), the shared Git/Files module `github.com/thewei/solco-gitkit` (packages gitworkbench + filesview; a sibling repository, required at a tagged version, linked locally by an uncommitted `go.work`), packaging policy, signed updater feed (thewei/solco), and the chat WebView frontend (N19)
