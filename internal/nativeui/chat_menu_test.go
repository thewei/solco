package nativeui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func menuIDs(rows []chatMenuEntry) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestChatMenuPlanFollowsTheTarget(t *testing.T) {
	cases := []struct {
		name      string
		in        ChatMenuInput
		quickLook bool
		want      []string
	}{
		{"blank area", ChatMenuInput{}, false, []string{"selectAll", "find"}},
		{"selection", ChatMenuInput{Selection: "x", Message: "m"}, false,
			[]string{"copy", "copyMessage", "", "selectAll", "find"}},
		{"link", ChatMenuInput{Link: "https://example.com/a"}, false,
			[]string{"openLink", "copyLink", "", "selectAll", "find"}},
		{"file chip", ChatMenuInput{Path: "a.go", Message: "m"}, true,
			[]string{"quickLook", "copyPath", "copyMessage", "", "selectAll", "find"}},
		{"unsafe link is not offered", ChatMenuInput{Link: "javascript:alert(1)"}, false,
			[]string{"selectAll", "find"}},
	}
	for _, c := range cases {
		if got := menuIDs(chatMenuPlan(c.in, c.quickLook)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestSafeExternalURLAllowsOnlyWebAndMail(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://example.com":      true,
		"http://example.com/a?b=c": true,
		"mailto:a@example.com":     true,
		"file:///etc/passwd":       false,
		"javascript:alert(1)":      false,
		"data:text/html,x":         false,
		"mygo://localhost/chat":    false,
		"https://":                 false,
		"":                         false,
		"/relative/path":           false,
	} {
		if got := safeExternalURL(raw); got != want {
			t.Errorf("safeExternalURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestChatNavigationNeverFollowsTheLinkInPlace(t *testing.T) {
	if !chatNavigationDecision("https://example.com", true) {
		t.Fatal("a clicked https link opens in the browser")
	}
	if chatNavigationDecision("https://example.com", false) {
		t.Fatal("a navigation the user did not start opens nothing")
	}
	if chatNavigationDecision("mygo://localhost/other", true) {
		t.Fatal("an in-app URL is not handed to the system")
	}
}

func TestResolveChatPathFindsOnlyExistingRegularFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sub", "a.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveChatPath("sub/a.go", "", "/nowhere", dir); !ok || got != file {
		t.Fatalf("relative path: got %q %v", got, ok)
	}
	if got, ok := resolveChatPath(file); !ok || got != file {
		t.Fatalf("absolute path: got %q %v", got, ok)
	}
	for _, miss := range []string{"sub", "missing.go", "", "sub/a.go\x00"} {
		if _, ok := resolveChatPath(miss, dir); ok {
			t.Errorf("%q must not resolve", miss)
		}
	}
	if _, ok := resolveChatPath("sub/a.go"); ok {
		t.Error("a relative path with no base must not resolve")
	}
}
