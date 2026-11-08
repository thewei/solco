package nativeui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The native fill under a WebView, the page's --content tokens, the inline
// first-paint style in index.html, the native Content token under the header
// band and the window's own base colour (main.go) are one colour pair; if any
// drifts the page flashes a different shade as it loads, or a seam shows
// between the native band and the page.
func TestWebviewBackgroundMatchesPageTokens(t *testing.T) {
	m := regexp.MustCompile(`^light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\)$`).FindStringSubmatch(webviewBackground)
	if m == nil {
		t.Fatalf("webviewBackground %q is not light-dark(#rrggbb, #rrggbb)", webviewBackground)
	}
	light, dark := m[1], m[2]

	theme, err := os.ReadFile("../../frontend/src/tokens.generated.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(theme)
	rootBlock := css[:strings.Index(css, ":root[data-theme=\"dark\"]")]
	darkBlock := css[strings.Index(css, ":root[data-theme=\"dark\"]"):]
	if !strings.Contains(rootBlock, "--content: "+light+";") {
		t.Errorf("tokens.generated.css light --content is not %s", light)
	}
	if !strings.Contains(darkBlock[:strings.Index(darkBlock, "}")], "--content: "+dark+";") {
		t.Errorf("tokens.generated.css dark --content is not %s", dark)
	}

	index, err := os.ReadFile("../../frontend/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "background: "+light+";") || !strings.Contains(string(index), "background: "+dark+";") {
		t.Errorf("index.html first-paint style does not carry %s / %s", light, dark)
	}

	mobile, err := os.ReadFile("../../frontend/mobile.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mobile), "background: "+dark+";") {
		t.Errorf("mobile.html first-paint style does not carry the dark %s", dark)
	}

	// The native band above the page, and the window under everything, draw the same pair.
	if got := designTokens(false).Content; got != ui.Hex(light) {
		t.Errorf("designTokens(light).Content = %v, want the page's %s", got, light)
	}
	if got := designTokens(true).Content; got != ui.Hex(dark) {
		t.Errorf("designTokens(dark).Content = %v, want the page's %s", got, dark)
	}
	mainGo, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainGo), `BackgroundColor:      "`+webviewBackground+`"`) {
		t.Errorf("main.go window BackgroundColor is not %s", webviewBackground)
	}
}
