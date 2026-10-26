module github.com/thewei/solco

go 1.27.3

tool github.com/egoist/mygo/cmd/mygo

// LOCAL FRAMEWORK PATCH (F146/F147 + WIX-021 + OSC 7501, user-approved):
// the vendored fork under third_party/mygo is v0.6.4 plus four local deltas
// — terminal Options.LocalDragSelect (scrollback panes select text and get
// a right-click menu while the wheel keeps reporting), field focus
// delegation (AutoFocus on a SearchField/Combobox reaches its inner
// input), multi-display tray flyout anchoring on macOS, and the OSC 7501
// program-status scanner (Options.OnProgramStatus). Remove this replace,
// bump the pin, and delete third_party/mygo once upstream ships all four
// — Solco code needs no changes. See
// third_party/mygo/LOCAL-PATCH.md.
replace github.com/egoist/mygo => ./third_party/mygo

require github.com/egoist/mygo v0.6.4

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
