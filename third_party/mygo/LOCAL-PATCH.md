# Local patch over mygo v0.6.4

Six deltas, re-applied by hand after every pin bump. They live outside
Solco and stay as small as deletion allows.

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
`internal/bridge/bridge.js` (`Z`). Evidence: `docs/debug/2026-11-04-jsc-heap-collector-crash.md`.

## Delta 4: terminal OSC 7501 program status (2026-10-15)

`plugins/terminal`: adds `Options.OnProgramStatus func(report []byte)` —
the body bytes of one OSC 7501 report (Program Status Protocol, spec rev
0.4), framed by a small scanner (`osc7501.go`) that rides the same output
bytes libghostty-vt reads. The emulator core ignores the sequence as an
unknown OSC; the scanner knows only the framing (OSC `7501;` through ST,
4096-byte cap, chunk-safe). With the callback set the terminal answers the
protocol's feature-detection query `OSC 7501 ; ? ST` with the full fixed
state and kind lists; without it, support is never claimed. Pairs,
records, lifetime and merge policy live in the embedding app
(herdr-client `internal/agentstatus`), which owns the protocol semantics.

Reason: Solco agent panes — Claude Code 2.1.295+ emits native status
reports (working/blocked/done/idle/error) that must reach the pane header
without hooks; agents without the protocol keep the hook fallback.

Sites: `plugins/terminal/osc7501.go` (new scanner + tests), `Options`
field and the doc comment in `plugins/terminal/terminal.go`, the `events`
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

## Delta 2: a field focuses its input (2026-10-07)

`ui.field` (the shared wrapper of `SearchField`, `Combobox`, `TokenField`
and the prompt input): the wrapper keeps its inner input in `focusTo`, and
`node.Focus` hands the focus to it. `AutoFocus` on a field wrapper now
reaches the editor, which alone takes text — before, `AutoFocus` focused
the wrapper row, where typing, the caret and IME did nothing (the runtime
delivers text only to the focused element's editor).

Reason: Solco command palette (WIX-021) — the palette's `SearchField`
must take the keyboard the frame the dialog opens, keyboard-first like
Spotlight/Raycast. Regression: `TestSearchField` now types without a click.

Sites: an unexported `node.focusTo` field in `ui/element.go`, a delegation
branch in `node.Focus` after its nil/invalid-state guard in `ui/input.go`, one line in `field`
(`ui/combobox.go`), and a `focusTo` declaration in the `other` field list of
upstream's new `TestLayoutInputs` (`ui/layoutcache_test.go`, v0.6.x).

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

Rebased onto v0.6.4 on 2026-10-25 via a three-way merge (ours = v0.5.5+delta,
base = pristine v0.5.5, theirs = pristine v0.6.4). The four deltas keep their
size: terminal.go +36, view.go +6, `ui/` 12 lines in three files, `internal/darwin/`
84 lines in three files, and the OSC 7501 scanner files copy over unchanged.
Two conflicts needed hand resolution:
- `internal/darwin/menu.go` — upstream added the `tray.dark` field at the same
  slot as our clickScreen/clickAnchor fields; both kept (dark first, then ours).
- `internal/darwin/popover.go` — upstream rewrote `tray.pressed()` around
  currentEvent window matching; ours' clickScreen anchor hit-test is kept in
  front of the new body, so secondary-display clicks hit-test against the
  recorded anchor rect before any primary-window logic.
Upstream v0.6.x also added `TestLayoutInputs` (ui/layoutcache_test.go), which
requires every node field to be declared; `focusTo` is declared in the `other`
list (layout never reads it). One more environment note: upstream's
`TestAssetCatalog`/`TestListBuildInputs` (cmd/mygo) fail in any proxy-zip
extraction (they need the full repo shape), so they fail identically on
pristine v0.6.4 — not a delta regression.
When an upstream mygo release ships these fixes, delete the
`replace github.com/egoist/mygo => ./third_party/mygo` directive in `go.mod`,
bump the pin, and delete this checkout. Solco code already uses only the
public option and needs no changes.

Delta vs v0.6.4: an `Options` field + a 4-line guard in `view.pointerEvent`;
an unexported `node.focusTo` field, a delegation branch after the nil-safe
`hasState` guard in `node.Focus`, and one line in `field`; multi-display tray
anchoring in `internal/darwin/{menu,popover,window}.go`; the OSC 7501 scanner
files, an `Options` field, the `events` fields and the feed/fire hooks in
`plugins/terminal`. Preserve the upstream
`TestOptionalElementInput/nil` behavior when rebasing this patch.