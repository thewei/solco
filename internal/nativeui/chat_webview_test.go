package nativeui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The native fill under a WebView, the page's --content tokens and the
// inline first-paint style in index.html are one colour pair; if any drifts
// the page flashes a different shade as it loads.
func TestWebviewBackgroundMatchesPageTokens(t *testing.T) {
	m := regexp.MustCompile(`^light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\)$`).FindStringSubmatch(webviewBackground)
	if m == nil {
		t.Fatalf("webviewBackground %q is not light-dark(#rrggbb, #rrggbb)", webviewBackground)
	}
	light, dark := m[1], m[2]

	theme, err := os.ReadFile("../../frontend/src/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(theme)
	rootBlock := css[:strings.Index(css, ":root[data-theme=\"dark\"]")]
	darkBlock := css[strings.Index(css, ":root[data-theme=\"dark\"]"):]
	if !strings.Contains(rootBlock, "--content: "+light+";") {
		t.Errorf("theme.css light --content is not %s", light)
	}
	if !strings.Contains(darkBlock[:strings.Index(darkBlock, "}")], "--content: "+dark+";") {
		t.Errorf("theme.css dark --content is not %s", dark)
	}

	index, err := os.ReadFile("../../frontend/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "background: "+light+";") || !strings.Contains(string(index), "background: "+dark+";") {
		t.Errorf("index.html first-paint style does not carry %s / %s", light, dark)
	}
}
