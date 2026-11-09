package feedback

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco-gitkit/filesview"
)

// project makes a repo with a file, a folder and one project Skill, and points HOME at an empty folder so the
// user's own Skills never leak into the test.
func project(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := mkRepo(t)
	for rel, body := range map[string]string{
		"docs/api.md":                     "# api\n",
		"main.go":                         "package main\n",
		".agents/skills/think/SKILL.md":   "---\nname: think\ndescription: Plan before coding\n---\n",
		".agents/skills/cleanup/SKILL.md": "---\nname: cleanup\ndescription: Tidy a branch\n---\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	listFiles = func(context.Context, string) (filesview.FileIndex, error) {
		return filesview.FileIndex{Paths: []string{"docs/api.md", "main.go"}}, nil
	}
	listChanged = nil
	t.Cleanup(func() {
		indexMu.Lock()
		indexes = map[string]cachedIndex{}
		indexMu.Unlock()
	})
	return root
}

func TestPickListsTheTopLevelThenRanksFilesFoldersAndSkills(t *testing.T) {
	root := project(t)
	ctx := context.Background()
	top := Pick(ctx, root, "files", "")
	if len(top) != 2 || top[0] != (PickItem{Kind: RefFolder, Name: "docs"}) || top[1] != (PickItem{Kind: RefFile, Name: "main.go"}) {
		t.Fatalf("top level: %+v", top)
	}
	hit := Pick(ctx, root, "files", "api")
	if len(hit) == 0 || hit[len(hit)-1] != (PickItem{Kind: RefFile, Name: "docs/api.md"}) {
		t.Fatalf("search: %+v", hit)
	}
	sk := Pick(ctx, root, "skills", "")
	if len(sk) != 2 || sk[0].Name != "cleanup" || sk[1].Detail != "Plan before coding" {
		t.Fatalf("skills: %+v", sk)
	}
	if got := Pick(ctx, root, "skills", "tidy"); len(got) != 1 || got[0].Name != "cleanup" {
		t.Fatalf("a skill is found by its description too: %+v", got)
	}
	if Pick(ctx, "", "files", "") != nil || Pick(ctx, root, "secrets", "") != nil {
		t.Fatal("no project, or an unknown kind, lists nothing")
	}
}

func TestResolveContextKeepsToTheProjectAndTheInstalledSkills(t *testing.T) {
	root := project(t)
	got, err := ResolveContext(root, []ContextRef{
		{Kind: "file", Name: "docs/api.md"},
		{Kind: "file", Name: "docs"}, // the disk, not the page, decides file vs folder
		{Kind: "skill", Name: "think"},
		{Kind: "skill", Name: "think"}, // a repeat is dropped
	})
	if err != nil || len(got) != 3 {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if got[0].Path != filepath.Join(root, "docs", "api.md") || got[1].Kind != RefFolder || !strings.HasSuffix(got[2].Path, filepath.Join("think", "SKILL.md")) {
		t.Fatalf("paths: %+v", got)
	}
	for _, bad := range []ContextRef{
		{Kind: "file", Name: "../outside"},
		{Kind: "file", Name: "docs/../../outside"},
		{Kind: "file", Name: "/etc/passwd"},
		{Kind: "file", Name: "missing.txt"},
		{Kind: "file", Name: "."},
		{Kind: "skill", Name: "not-installed"},
		{Kind: "url", Name: "https://x"},
		{Kind: "file", Name: " "},
	} {
		if _, err := ResolveContext(root, []ContextRef{bad}); !errors.Is(err, ErrBadContext) {
			t.Errorf("%+v must be refused, got %v", bad, err)
		}
	}
	if _, err := ResolveContext("", []ContextRef{{Kind: "file", Name: "x"}}); !errors.Is(err, ErrBadContext) {
		t.Error("a thread without a project has no files to point at")
	}
	if got, err := ResolveContext("", []ContextRef{{Kind: "skill", Name: "none"}}); got != nil || err == nil {
		t.Error("an unknown skill is refused without a project too")
	}
}

func TestAnswerContextReachesTheAgentAsAbsolutePaths(t *testing.T) {
	root := project(t)
	dir := t.TempDir()
	token, _ := EnsureToken(dir)
	b := &Broker{Store: NewMemory(), Token: token}
	srv := httptest.NewServer(NewHandler(b))
	defer srv.Close()
	writeEndpoint(t, dir, srv.Listener.Addr().String())
	c := NewClient(dir)
	ctx := context.Background()

	askID, threadID, err := c.Create(ctx, CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: root, Questions: q1("Which?")})
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.Pick(ctx, threadID, "skills", "think")
	if err != nil || len(items) != 1 {
		t.Fatalf("pick through the broker: %+v %v", items, err)
	}
	if err := c.Answer(ctx, askID, map[string]Answer{"a": {Selected: []string{"x"}}}, nil, []ContextRef{{Kind: "file", Name: "../escape"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a reference outside the project must refuse the whole answer: %v", err)
	}
	if r, _ := c.Result(ctx, askID); r.Status != "pending" {
		t.Fatalf("a refused answer changes nothing: %+v", r)
	}
	refs := []ContextRef{{Kind: "file", Name: "main.go"}, {Kind: "skill", Name: "think"}}
	if err := c.Answer(ctx, askID, map[string]Answer{"a": {Selected: []string{"x"}}}, nil, refs); err != nil {
		t.Fatal(err)
	}
	r, _ := c.Result(ctx, askID)
	if r.Status != "answered" || len(r.Context) != 2 || r.Context[0].Path != filepath.Join(root, "main.go") || !strings.HasSuffix(r.Context[1].Path, "SKILL.md") {
		t.Fatalf("result: %+v", r)
	}
	th, _ := c.Thread(ctx, threadID)
	if len(th.Asks[0].Context) != 2 {
		t.Fatalf("the timeline keeps what was attached: %+v", th.Asks[0])
	}
}

func TestPickPutsChangedFilesFirstAndParsesGitStatus(t *testing.T) {
	root := project(t)
	listChanged = func(context.Context, string) []string { return []string{"main.go"} }
	ctx := context.Background()
	top := Pick(ctx, root, "files", "")
	if len(top) != 2 || top[0] != (PickItem{Kind: RefFile, Name: "main.go", Detail: ChangedDetail}) || top[1].Name != "docs" {
		t.Fatalf("changed first, once: %+v", top)
	}
	hit := Pick(ctx, root, "files", "go")
	if len(hit) == 0 || hit[0].Name != "main.go" || hit[0].Detail != ChangedDetail {
		t.Fatalf("a changed search hit leads: %+v", hit)
	}
	got := parseChanged([]byte(" M a.go\x00R  new.go\x00old.go\x00 D gone.go\x00?? n/x.md\x00"))
	if strings.Join(got, ",") != "a.go,new.go,n/x.md" {
		t.Fatalf("porcelain: %v", got)
	}
}

func TestFilterSuggestKeepsOnlyRealReferencesWithoutPaths(t *testing.T) {
	root := project(t)
	got := FilterSuggest(root, []ContextRef{
		{Kind: RefFile, Name: "main.go"}, {Kind: RefFile, Name: "../etc/passwd"}, {Kind: RefSkill, Name: "think"},
		{Kind: RefSkill, Name: "nope"}, {Kind: RefFile, Name: "main.go"}, {Kind: "weird", Name: "x"},
	})
	if len(got) != 2 || got[0] != (ContextRef{Kind: RefFile, Name: "main.go"}) || got[1] != (ContextRef{Kind: RefSkill, Name: "think"}) {
		t.Fatalf("suggest: %+v", got)
	}
}
