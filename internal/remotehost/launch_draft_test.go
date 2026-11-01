package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/history"
)

func draftHarness(t *testing.T) (*harness, *[]string) {
	t.Helper()
	h, _ := launcherHarness(t)
	var seen []string
	h.srv.opts.Catalog = func(_ context.Context, provider history.AgentID, root string) agentcatalog.Catalog {
		seen = append(seen, string(provider)+"@"+root)
		return agentcatalog.Catalog{Skills: []agentcatalog.Entry{{Name: "/review", Insert: "/review ", Kind: agentcatalog.KindSkill}}, ModelVerb: "/model "}
	}
	return h, &seen
}

func TestDraftCatalogResolvesAWorkspaceOrAPathWithoutAPane(t *testing.T) {
	h, seen := draftHarness(t)
	for _, query := range []string{"kind=claude-code&workspaceId=w1", "kind=claude-code&path=" + url.QueryEscape("/work/other")} {
		resp := h.get("/api/v3/launch/catalog?instance=default&"+query, h.token, nil)
		var out catalogBody
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Skills) != 1 || out.ModelVerb != "/model " {
			t.Fatalf("%s: status %d body %+v", query, resp.StatusCode, out)
		}
	}
	if want := []string{"claude-code@/work/solco", "claude-code@/work/other"}; !reflect.DeepEqual(*seen, want) {
		t.Fatalf("loader saw %v, want %v", *seen, want)
	}
}

func TestDraftCatalogRefusals(t *testing.T) {
	h, seen := draftHarness(t)
	cases := map[string]int{
		"kind=terminal&workspaceId=w1":              http.StatusBadRequest,
		"kind=rm&workspaceId=w1":                    http.StatusBadRequest,
		"kind=claude-code":                          http.StatusBadRequest,
		"kind=claude-code&workspaceId=w1&path=/tmp": http.StatusBadRequest,
		"kind=claude-code&path=work":                http.StatusBadRequest,
		"kind=claude-code&path=%2Fwork%2F..%2Fetc":  http.StatusBadRequest,
		"kind=claude-code&workspaceId=nope":         http.StatusNotFound,
	}
	for query, want := range cases {
		if resp := h.get("/api/v3/launch/catalog?instance=default&"+query, h.token, nil); resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d", query, resp.StatusCode, want)
		}
	}
	if resp := h.get("/api/v3/launch/catalog?instance=default&kind=claude-code&workspaceId=w1", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status %d", resp.StatusCode)
	}
	if len(*seen) != 0 {
		t.Fatalf("a refused request loaded a catalog: %v", *seen)
	}
	plain := newHarness(t)
	if resp := plain.get("/api/v3/launch/catalog?instance=default&kind=claude-code&workspaceId=w1", plain.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("without a launcher: status %d", resp.StatusCode)
	}
}

func TestDraftFilesListsTheNamedFolder(t *testing.T) {
	h, _ := draftHarness(t)
	root := t.TempDir()
	for _, p := range []string{"src/app.go", "README.md"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	resp := h.get("/api/v3/launch/files?instance=default&token=&path="+url.QueryEscape(root), h.token, nil)
	var out filesBody
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || !reflect.DeepEqual(out.Files, []string{"README.md", "src/"}) {
		t.Fatalf("status %d files %v", resp.StatusCode, out.Files)
	}
	if resp := h.get("/api/v3/launch/files?instance=default&token=", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no target: status %d", resp.StatusCode)
	}
}

func TestDraftAttachStoresWithoutAPane(t *testing.T) {
	h, saved := attachHarness(t)
	req, _ := http.NewRequest("POST", h.http.URL+"/api/v3/launch/attachments?instance=default", strings.NewReader(attachBody([]byte("note"), "text/plain", "n.txt")))
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out attachReply
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || out.Path == "" || len(*saved) != 1 {
		t.Fatalf("status %d path %q saved %d", resp.StatusCode, out.Path, len(*saved))
	}
}
