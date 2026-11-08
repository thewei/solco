package remotehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco-gitkit/filesview"
)

// searchHarness is browseHarness with its folder made a Git repository, so the index comes from Git rather than
// from an optional ripgrep; it returns the folder too.
func searchHarness(t *testing.T) (*harness, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := browseHarness(t)
	h.back.mu.Lock()
	root := h.back.proj.Projects[0].CWD
	h.back.mu.Unlock()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return h, root
}

func searchURL(query string) string {
	return "/api/v3/workspaces/w1/files/search?q=" + url.QueryEscape(query)
}

// swapSearchIndex replaces the index for one test.
func swapSearchIndex(t *testing.T, fn func(context.Context, string) (filesview.FileIndex, error)) {
	t.Helper()
	old := searchIndex
	searchIndex = fn
	t.Cleanup(func() { searchIndex = old })
}

func TestFilesSearchAbsentWhenOff(t *testing.T) {
	h := newHarness(t)
	if resp := h.get(searchURL("a"), h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "files_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestFilesSearchNeedsAuth(t *testing.T) {
	h := browseHarness(t)
	if resp := h.get(searchURL("a"), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}

func TestFilesSearchUnknownWorkspace(t *testing.T) {
	h := browseHarness(t)
	resp := h.get("/api/v3/workspaces/nope/files/search?q=a", h.token, nil)
	if resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_workspace" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestFilesSearchRefusesAMissingBlankOrHugeQuery(t *testing.T) {
	h := browseHarness(t)
	for _, path := range []string{
		"/api/v3/workspaces/w1/files/search",
		searchURL(""),
		searchURL("   "),
		searchURL("a\x00b"),
		searchURL(strings.Repeat("a", maxSearchQuery+1)),
	} {
		if resp := h.get(path, h.token, nil); resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "bad_request" {
			t.Fatalf("%.60s: status %d", path, resp.StatusCode)
		}
	}
}

func TestFilesSearchRanksNameHitsBeforeFolderHits(t *testing.T) {
	h, root := searchHarness(t)
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := decodeBody[FilesSearch](t, h.get(searchURL("app"), h.token, nil))
	// the file called app.go beats the file that only sits in an app folder
	if got.WorkspaceID != "w1" || len(got.Hits) != 2 || got.Hits[0] != "src/app.go" || got.Hits[1] != "app/readme.txt" || got.Total != 2 {
		t.Fatalf("search %+v", got)
	}
}

func TestFilesSearchAnswersEveryWordAcrossFolderAndName(t *testing.T) {
	h, _ := searchHarness(t)
	got := decodeBody[FilesSearch](t, h.get(searchURL("src util"), h.token, nil))
	if len(got.Hits) != 1 || got.Hits[0] != "src/util.go" {
		t.Fatalf("search %+v", got)
	}
}

func TestFilesSearchLeavesOutWhatWouldNotOpen(t *testing.T) {
	h, root := searchHarness(t)
	// Git keeps listing a tracked file after it was deleted from disk
	gone := filepath.Join(root, "src", "gone.go")
	if err := os.WriteFile(gone, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "src/gone.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"gone",         // deleted on disk, still in Git's list
		"escape",       // a link to a folder outside the Workspace
		"node_modules", // the tree's fixed heavy names
		"assets",       // a folder is not a file (its files are)
	} {
		got := decodeBody[FilesSearch](t, h.get(searchURL(query), h.token, nil))
		for _, hit := range got.Hits {
			if hit == "src/gone.go" || hit == "escape" || strings.HasPrefix(hit, "node_modules") || hit == "assets" {
				t.Fatalf("%q returned %q", query, hit)
			}
		}
		// every match was checked, so the count agrees with what came back
		if got.Total != len(got.Hits) {
			t.Fatalf("%q: total %d for %d hits", query, got.Total, len(got.Hits))
		}
	}
	if got := decodeBody[FilesSearch](t, h.get(searchURL("assets"), h.token, nil)); len(got.Hits) != 2 {
		t.Fatalf("the files inside assets are findable: %+v", got)
	}
}

func TestFilesSearchCapsHitsAndCountsTheRest(t *testing.T) {
	h, root := searchHarness(t)
	if err := os.MkdirAll(filepath.Join(root, "bulk"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxSearchHits+10; i++ {
		if err := os.WriteFile(filepath.Join(root, "bulk", fmt.Sprintf("n%02d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := decodeBody[FilesSearch](t, h.get(searchURL("bulk"), h.token, nil))
	if len(got.Hits) != maxSearchHits || got.Total != maxSearchHits+10 || got.Hits[0] != "bulk/n00.txt" || got.Hits[maxSearchHits-1] != "bulk/n49.txt" || got.Truncated {
		t.Fatalf("hits %d total %d first %q truncated %v", len(got.Hits), got.Total, got.Hits[0], got.Truncated)
	}
}

func TestFilesSearchFindsNothingWithAnEmptyList(t *testing.T) {
	h, _ := searchHarness(t)
	resp := h.get(searchURL("zzzz-nothing-matches"), h.token, nil)
	body := string(bodyOf(t, resp))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"hits":[]`) || !strings.Contains(body, `"total":0`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}

func TestFilesSearchSaysWhenTheIndexWasCut(t *testing.T) {
	h := browseHarness(t)
	swapSearchIndex(t, func(context.Context, string) (filesview.FileIndex, error) {
		return filesview.FileIndex{Paths: []string{"README.md"}, Truncated: true}, nil
	})
	got := decodeBody[FilesSearch](t, h.get(searchURL("readme"), h.token, nil))
	if !got.Truncated || len(got.Hits) != 1 || got.Hits[0] != "README.md" {
		t.Fatalf("search %+v", got)
	}
}

func TestFilesSearchIndexFailuresAreStableCodes(t *testing.T) {
	h := browseHarness(t)
	for _, tc := range []struct {
		err  error
		code string
	}{
		{filesview.ErrNoContentEngine, "search_unavailable"},
		{errors.New("git exploded"), "files_failed"},
	} {
		swapSearchIndex(t, func(context.Context, string) (filesview.FileIndex, error) { return filesview.FileIndex{}, tc.err })
		resp := h.get(searchURL("a"), h.token, nil)
		if resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != tc.code {
			t.Fatalf("%v: status %d", tc.err, resp.StatusCode)
		}
	}
}

func TestFilesSearchAdvertisedWithFiles(t *testing.T) {
	h := browseHarness(t)
	if !strings.Contains(string(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil))), `"filesSearch":true`) {
		t.Fatal("bootstrap does not advertise filesSearch beside files")
	}
	h.srv.opts.Files = false
	if strings.Contains(string(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil))), "filesSearch") {
		t.Fatal("filesSearch advertised with Files off")
	}
}

func TestFilesSearchGolden(t *testing.T) {
	h, _ := searchHarness(t)
	golden(t, "files-search.json", bodyOf(t, h.get(searchURL("go"), h.token, nil)))
	swapSearchIndex(t, func(context.Context, string) (filesview.FileIndex, error) {
		return filesview.FileIndex{}, filesview.ErrNoContentEngine
	})
	golden(t, "error-files-search-unavailable.json", bodyOf(t, h.get(searchURL("go"), h.token, nil)))
}
