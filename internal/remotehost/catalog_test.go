package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/history"
)

func catalogHarness(t *testing.T) (*harness, *[]string) {
	t.Helper()
	h := newHarness(t)
	var seen []string
	h.srv.opts.Catalog = func(_ context.Context, provider history.AgentID, root string) agentcatalog.Catalog {
		seen = append(seen, string(provider)+"@"+root)
		return agentcatalog.Catalog{
			Commands:     []agentcatalog.Entry{{Name: "/compact", Insert: "/compact ", Detail: "Compact", Kind: agentcatalog.KindText}},
			Skills:       []agentcatalog.Entry{{Name: "/review", Insert: "/review ", Kind: agentcatalog.KindSkill}},
			Models:       []agentcatalog.Model{{ID: "anthropic/sonnet"}},
			ModelVerb:    "/model ",
			DefaultModel: "sonnet",
		}
	}
	return h, &seen
}

func TestCatalogServesTheAgentsOwnListForThePaneFolder(t *testing.T) {
	h, seen := catalogHarness(t)
	resp := h.get("/api/v3/panes/w1:p1/chat/catalog?instance=default", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out catalogBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Kind != "text" || len(out.Skills) != 1 || out.Skills[0].Kind != "skill" ||
		len(out.Models) != 1 || out.ModelVerb != "/model " || out.DefaultModel != "sonnet" {
		t.Fatalf("catalog %+v", out)
	}
	if len(*seen) != 1 || (*seen)[0] != "claude-code@/work/solco" {
		t.Fatalf("loader saw %v", *seen)
	}
}

func TestCatalogCarriesEffortsAndTheNotesThatGoWithThem(t *testing.T) {
	h := newHarness(t)
	h.srv.opts.Catalog = func(context.Context, history.AgentID, string) agentcatalog.Catalog {
		return agentcatalog.Catalog{
			Models: []agentcatalog.Model{{
				ID: "Gemini 3.8 Flash", Pick: "/model Gemini 3.8 Flash (High)",
				Efforts: []agentcatalog.Effort{{Label: "High", Send: "/model Gemini 3.8 Flash (High)"}},
			}},
			ModelVerb: "/model ", ModelNote: "saved as default",
			Efforts: []agentcatalog.Effort{{Label: "low", Send: "/effort low"}}, EffortNote: "saved per model",
		}
	}
	resp := h.get("/api/v3/panes/w1:p1/chat/catalog?instance=default", h.token, nil)
	var out catalogBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Models) != 1 || out.Models[0].Pick == "" || len(out.Models[0].Efforts) != 1 || out.ModelVerb != "/model " ||
		out.ModelNote == "" || len(out.Efforts) != 1 || out.Efforts[0].Send != "/effort low" || out.EffortNote == "" {
		t.Fatalf("the phone shows a switch with its note, so both must reach it: %+v", out)
	}
}

func TestCatalogRefusals(t *testing.T) {
	h, seen := catalogHarness(t)
	for _, c := range []struct {
		path, token string
		status      int
		code        string
	}{
		{"/api/v3/panes/w1:p1/chat/catalog?instance=default", "", 401, "unauthorized"},
		{"/api/v3/panes/nope/chat/catalog?instance=default", h.token, 404, "unknown_pane"},
		{"/api/v3/panes/w1:p1/chat/catalog?instance=ghost", h.token, 404, "unknown_instance"},
	} {
		resp := h.get(c.path, c.token, nil)
		if resp.StatusCode != c.status || errCode(t, resp) != c.code {
			t.Fatalf("%s: status %d", c.path, resp.StatusCode)
		}
	}
	if len(*seen) != 0 {
		t.Fatal("a refused request reached the catalog")
	}
}

func TestCatalogIsEmptyForAPaneWithoutAKnownAgent(t *testing.T) {
	h, seen := catalogHarness(t)
	h.back.mu.Lock()
	for i := range h.back.proj.Panes {
		if h.back.proj.Panes[i].ID == "w1:p1" {
			h.back.proj.Panes[i].Agent = "some-unknown-tool"
		}
	}
	h.back.mu.Unlock()
	resp := h.get("/api/v3/panes/w1:p1/chat/catalog?instance=default", h.token, nil)
	var out catalogBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d err %v", resp.StatusCode, err)
	}
	if len(out.Commands)+len(out.Skills)+len(out.Models) != 0 || len(*seen) != 0 {
		t.Fatalf("catalog %+v loader %v", out, *seen)
	}
}
