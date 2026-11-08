package nativeui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

/**
 * [INPUT]: design/icons.json + design/tokens.json, the generated design_gen.go/icons_gen.go, the native sources and the Chat page sources
 * [OUTPUT]: guards for the unified design language: generated files are current, one glyph per icon id, no inline icon path data outside the generated files, the stroke has one value
 * [POS]: 2026-11-07 Desktop and phone design language, U0/U4 — the registry is the only place a glyph or a palette value is written
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type iconRegistryFile struct {
	Stroke struct {
		Pointer float64 `json:"pointer"`
		Touch   float64 `json:"touch"`
	} `json:"stroke"`
	Icons []struct {
		ID     string   `json:"id"`
		Tabler string   `json:"tabler"`
		Go     []string `json:"go"`
		Chat   []string `json:"chat"`
	} `json:"icons"`
	Glyphs map[string]struct {
		Outline string `json:"outline"`
	} `json:"glyphs"`
}

func loadIconRegistry(t *testing.T) iconRegistryFile {
	t.Helper()
	raw, err := os.ReadFile("../../design/icons.json")
	if err != nil {
		t.Fatal(err)
	}
	var reg iconRegistryFile
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestGeneratedDesignFilesAreCurrent runs the generator's read-only check: a
// hand edit of a generated file, or a source edit without a regenerate, fails
// here. It needs node (the generator is a script); without it the gate's own
// drift lane carries the check.
func TestGeneratedDesignFilesAreCurrent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "design/gen.mjs", "--check")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated design files drifted: %v\n%s", err, out)
	}
}

// TestIconRegistryHasOneGlyphPerID pins the registry's own shape: an id is
// listed once, each Go var and Chat key belongs to exactly one id, and every
// id resolves to a glyph.
func TestIconRegistryHasOneGlyphPerID(t *testing.T) {
	reg := loadIconRegistry(t)
	ids, goVars, chatKeys := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, i := range reg.Icons {
		if ids[i.ID] {
			t.Errorf("icon id %s is listed twice", i.ID)
		}
		ids[i.ID] = true
		if _, ok := reg.Glyphs[i.Tabler]; !ok {
			t.Errorf("icon %s: no glyph for %q", i.ID, i.Tabler)
		}
		for _, v := range i.Go {
			if other, dup := goVars[v]; dup {
				t.Errorf("%s is claimed by %s and %s", v, other, i.ID)
			}
			goVars[v] = i.ID
		}
		for _, k := range i.Chat {
			if other, dup := chatKeys[k]; dup {
				t.Errorf("Chat key %s is claimed by %s and %s", k, other, i.ID)
			}
			chatKeys[k] = i.ID
		}
	}
}

// TestIconStrokeIsTheRegistrys keeps the native stroke weight equal to the
// pointer tier of the registry, so the two cannot drift apart.
func TestIconStrokeIsTheRegistrys(t *testing.T) {
	reg := loadIconRegistry(t)
	if got, want := iconStroke, strconv.FormatFloat(reg.Stroke.Pointer, 'f', -1, 64); got != want {
		t.Errorf("iconStroke = %s, registry pointer stroke = %s", got, want)
	}
}

// TestNoInlineIconPathsOutsideTheGeneratedFiles: a glyph is written once, in
// design/icons.json. A new native icon is a registry row, not an icon(`<path…`)
// call. Brand marks (proc_brands.go: technology logos; agent_visual.go: the
// provider glyphs) are the one named exception — they are logos, not product
// meanings.
func TestNoInlineIconPathsOutsideTheGeneratedFiles(t *testing.T) {
	call := regexp.MustCompile(`\bicon\(` + "`")
	allowed := map[string]bool{"icons_gen.go": true, "proc_brands.go": true, "agent_visual.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || allowed[name] {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if call.Match(src) {
			t.Errorf("%s draws an inline icon: add the glyph to design/icons.json and regenerate", name)
		}
	}

	// The Chat page: icons.tsx reads the generated table; the meter ring and the
	// Stop square are drawn shapes, not icons.
	drawn := map[string]bool{"context.tsx": true, "icons.tsx": true}
	shape := regexp.MustCompile(`<(path|polygon|polyline|line|ellipse)\b`)
	chat, err := filepath.Glob("../../frontend/src/*.tsx")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob("../../frontend/src/*/*.tsx")
	for _, name := range append(chat, more...) {
		if drawn[filepath.Base(name)] {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if shape.Match(src) {
			t.Errorf("%s carries inline svg shapes: use <Icon name=…/> (design/icons.json)", name)
		}
	}
}
