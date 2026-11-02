package remotehost

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// browseHarness points Workspace w1 at a temp folder holding a small known tree.
func browseHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	root := t.TempDir()
	files := map[string]string{
		"README.md":       "# Solco\n",
		"src/app.go":      "package app\n",
		"src/util.go":     "package app\n",
		".env":            "TOKEN=1\n",
		"node_modules/x":  "ignored by name\n",
		"assets/blob.bin": "a\x00b",
		"assets/dot.svg":  `<svg width="1" height="1"></svg>`,
	}
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	h.srv.opts.Files = true
	h.back.mu.Lock()
	h.back.proj.Projects[0].CWD = root
	h.back.mu.Unlock()
	return h
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFilesAbsentWhenOff(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v3/workspaces/w1/files", "/api/v3/workspaces/w1/files/content?path=a"} {
		if resp := h.get(path, h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "files_unavailable" {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
}

func TestFilesNeedAuth(t *testing.T) {
	h := browseHarness(t)
	if resp := h.get("/api/v3/workspaces/w1/files", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}

func TestFilesListRootDirsFirstIgnoredHidden(t *testing.T) {
	h := browseHarness(t)
	list := decodeBody[FilesList](t, h.get("/api/v3/workspaces/w1/files", h.token, nil))
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Kind+":"+e.Name)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for _, n := range []string{"dir:assets", "dir:src", "symlink:escape", "file:.env", "file:README.md"} {
		if !got[n] {
			t.Fatalf("missing %s in %v", n, names)
		}
	}
	if got["dir:node_modules"] {
		t.Fatalf("an ignored folder was listed: %v", names)
	}
	if len(names) != 5 || names[3] != "file:.env" || names[4] != "file:README.md" {
		t.Fatalf("folders (and the folder link) do not come first: %v", names)
	}
}

func TestFilesListSubfolderCarriesRelativePaths(t *testing.T) {
	h := browseHarness(t)
	list := decodeBody[FilesList](t, h.get("/api/v3/workspaces/w1/files?dir=src", h.token, nil))
	if list.Dir != "src" || len(list.Entries) != 2 || list.Entries[0].Path != "src/app.go" || list.Entries[0].Kind != "file" {
		t.Fatalf("list %+v", list)
	}
}

func TestFilesRefuseEscapes(t *testing.T) {
	h := browseHarness(t)
	for _, q := range []string{
		"/api/v3/workspaces/w1/files?dir=..",
		"/api/v3/workspaces/w1/files?dir=../..",
		"/api/v3/workspaces/w1/files?dir=/etc",
		"/api/v3/workspaces/w1/files?dir=escape",
		"/api/v3/workspaces/w1/files/content?path=escape/secret.txt",
		"/api/v3/workspaces/w1/files/content?path=../README.md",
		"/api/v3/workspaces/w1/files/content?path=/etc/hosts",
		"/api/v3/workspaces/w1/files/content?path=src%00x",
	} {
		if resp := h.get(q, h.token, nil); resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "bad_request" {
			t.Fatalf("%s: status %d", q, resp.StatusCode)
		}
	}
}

func TestFilesMissingAndWrongKind(t *testing.T) {
	h := browseHarness(t)
	if resp := h.get("/api/v3/workspaces/w1/files?dir=nope", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_file" {
		t.Fatalf("missing dir: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/workspaces/w1/files?dir=README.md", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a file as folder: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/workspaces/w1/files/content?path=src", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a folder as file: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/workspaces/w9/files", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_workspace" {
		t.Fatalf("ghost workspace: %d", resp.StatusCode)
	}
}

func TestFileContentFlavors(t *testing.T) {
	h := browseHarness(t)
	md := decodeBody[FileContent](t, h.get("/api/v3/workspaces/w1/files/content?path=README.md", h.token, nil))
	if md.Flavor != "markdown" || md.Content != "# Solco\n" || md.Name != "README.md" || md.Path != "README.md" {
		t.Fatalf("markdown %+v", md)
	}
	code := decodeBody[FileContent](t, h.get("/api/v3/workspaces/w1/files/content?path=src/app.go", h.token, nil))
	if code.Flavor != "text" || code.Content != "package app\n" {
		t.Fatalf("text %+v", code)
	}
	bin := decodeBody[FileContent](t, h.get("/api/v3/workspaces/w1/files/content?path=assets/blob.bin", h.token, nil))
	if bin.Flavor != "binary" || bin.Content != "" || bin.Size != 3 {
		t.Fatalf("binary %+v", bin)
	}
	img := decodeBody[FileContent](t, h.get("/api/v3/workspaces/w1/files/content?path=assets/dot.svg", h.token, nil))
	if img.Flavor != "image" || img.DataURL == "" || img.TooLarge {
		t.Fatalf("image %+v", img)
	}
}

func TestFileContentCapsAndTruncates(t *testing.T) {
	h := browseHarness(t)
	root := h.back.proj.Projects[0].CWD
	big := make([]byte, 1<<20+10)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	huge := make([]byte, maxPhoneImage+1)
	if err := os.WriteFile(filepath.Join(root, "huge.png"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	txt := decodeBody[FileContent](t, h.get("/api/v3/workspaces/w1/files/content?path=big.txt", h.token, nil))
	if !txt.Truncated || len(txt.Content) != 1<<20 {
		t.Fatalf("text truncated=%v len=%d", txt.Truncated, len(txt.Content))
	}
	img := decodeBody[FileContent](t, h.get("/api/v3/workspaces/w1/files/content?path=huge.png", h.token, nil))
	if !img.TooLarge || img.DataURL != "" {
		t.Fatalf("image %+v", img)
	}
}

func TestFilesBootstrapAdvertisesCapability(t *testing.T) {
	h := browseHarness(t)
	body := decodeBody[struct {
		Capabilities map[string]bool `json:"capabilities"`
	}](t, h.get("/api/v3/bootstrap", h.token, nil))
	if !body.Capabilities["files"] {
		t.Fatalf("capabilities %v", body.Capabilities)
	}
}
