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
