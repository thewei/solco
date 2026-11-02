package changelog

import (
	"reflect"
	"testing"
)

const sample = `# Changelog

Curated by hand before every release; see .agents/skills/solco-release.

## v0.11.0 — 2026-11-01

### New
- Chat: the follow-up queue is one ordered stack with drag reordering
- About page in Settings with the release notes

### Fixed
- Terminal: resizing no longer drops the last line

### Improved
- Sessions: rows fade in when a filter brings them

### Internal
- release: changelog stage gates the release script

## v0.10.0 — 2026-10-30

### New
- Older release, must be ignored
`

func TestCurrentParsesTheNewestReleaseOnly(t *testing.T) {
	// The parser sees only the first release; swap the snapshot for the sample.
	orig := snapshot
	snapshot = sample
	t.Cleanup(func() { snapshot = orig })

	rel, ok := Current()
	if !ok {
		t.Fatal("Current() = ok=false for a valid snapshot")
	}
	if rel.Version != "0.11.0" || rel.Date != "2026-11-01" {
		t.Fatalf("version/date = %q/%q", rel.Version, rel.Date)
	}
	want := []Group{
		{Name: "New", Items: []string{
			"Chat: the follow-up queue is one ordered stack with drag reordering",
			"About page in Settings with the release notes",
		}},
		{Name: "Fixed", Items: []string{"Terminal: resizing no longer drops the last line"}},
		{Name: "Improved", Items: []string{"Sessions: rows fade in when a filter brings them"}},
		{Name: "Internal", Items: []string{"release: changelog stage gates the release script"}},
	}
	if !reflect.DeepEqual(rel.Groups, want) {
		t.Fatalf("groups =\n%+v\nwant\n%+v", rel.Groups, want)
	}
}

func TestAllParsesAllReleases(t *testing.T) {
	orig := snapshot
	snapshot = sample
	t.Cleanup(func() { snapshot = orig })

	all := All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d releases, want 2", len(all))
	}
	if all[0].Version != "0.11.0" || all[1].Version != "0.10.0" {
		t.Fatalf("versions = %s, %s", all[0].Version, all[1].Version)
	}
}

func TestBetweenFiltersReleasesSpan(t *testing.T) {
	orig := snapshot
	snapshot = sample
	t.Cleanup(func() { snapshot = orig })

	// From 0.9.0 to 0.11.0: both 0.11.0 and 0.10.0
	span := Between("0.9.0", "0.11.0")
	if len(span) != 2 {
		t.Fatalf("Between(0.9.0, 0.11.0) = %d releases, want 2", len(span))
	}

	// From 0.10.0 to 0.11.0: only 0.11.0
	span1 := Between("0.10.0", "0.11.0")
	if len(span1) != 1 || span1[0].Version != "0.11.0" {
		t.Fatalf("Between(0.10.0, 0.11.0) = %+v, want [0.11.0]", span1)
	}

	// From 0.11.0 to 0.11.0: empty
	span0 := Between("0.11.0", "0.11.0")
	if len(span0) != 0 {
		t.Fatalf("Between(0.11.0, 0.11.0) = %d releases, want 0", len(span0))
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		v1, v2 string
		want   int
	}{
		{"0.12.0", "0.11.0", 1},
		{"0.11.0", "0.12.0", -1},
		{"0.12.0", "0.12.0", 0},
		{"v0.12.0", "0.12.0", 0},
		{"0.12.1", "0.12.0", 1},
		{"1.0.0", "0.12.0", 1},
		{"dev", "0.12.0", -1},
		{"0.12.0", "dev", 1},
	}
	for _, tc := range cases {
		got := CompareVersions(tc.v1, tc.v2)
		if got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.v1, tc.v2, got, tc.want)
		}
	}
}

func TestCurrentWithoutReleaseSectionIsNotOK(t *testing.T) {
	orig := snapshot
	snapshot = "# Changelog\n\nnothing here yet\n"
	t.Cleanup(func() { snapshot = orig })

	if _, ok := Current(); ok {
		t.Fatal("Current() = ok=true for a snapshot without a release section")
	}
}

func TestSnapshotIsCommittedAndParses(t *testing.T) {
	// The shipped build must carry a real section: the release flow writes
	// it, but a stray empty file would blank the About page.
	rel, ok := Current()
	if !ok {
		t.Fatal("committed snapshot has no release section")
	}
	if rel.Version == "" || len(rel.Groups) == 0 {
		t.Fatalf("snapshot release = %+v", rel)
	}
}
