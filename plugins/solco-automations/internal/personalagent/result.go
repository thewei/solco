// [INPUT]: a Worker's finished Herdr/OMP run and the read-only OMP transcript Solco pinned for it
// [OUTPUT]: WorkerResult — the deliverable, where it was read from, which tools/skills really ran, which URLs the tool results grounded
// [POS]: Gap W02/W03/W06/Q01: an Agent going idle is not a finished task; only a readable, attributable result is
// [PROTOCOL]: Update header on changes; read project CLAUDE.md
package personalagent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/thewei/solco/plugins/solco-automations/internal/hostpath"
)

// ErrNoResult means the Agent ended but no trustworthy final answer could be
// read for this run: the transcript is missing, ambiguous, unfinished or empty.
// It is never turned into a success.
var ErrNoResult = errors.New("no readable worker result")

// ResultUnknownPrefix opens the run error of a run whose Agent ended without a
// readable result. The run is `uncertain` (needs a person or a recollect), not done.
const ResultUnknownPrefix = "result_unknown: "

// Evidence kinds.
const (
	EvidenceOMPTranscript = "omp_transcript"
)

// SourceRef is one URL the Worker's answer names. Grounded is true only when
// the same URL appears in the output of a tool the Worker really ran; a link
// the model produced from memory is listed but not grounded.
type SourceRef struct {
	URL      string `json:"url"`
	Grounded bool   `json:"grounded"`
}

// WorkerResult is what Solco read back from a Worker, with its provenance.
// Summary is the Agent's own final message (data, never instructions); the
// remaining fields are facts Solco derived from the transcript itself.
type WorkerResult struct {
	Summary string `json:"summary"`
	// Evidence says where Summary was read from.
	Evidence       string `json:"evidence"`
	AgentSessionID string `json:"agentSessionId"`
	MessageID      string `json:"messageId"`
	// SHA256 is the hex digest of Summary exactly as stored.
	SHA256 string `json:"sha256"`
	// Tools counts the tool calls of the run, by name; ToolErrors those whose
	// result was an error.
	Tools      map[string]int `json:"tools"`
	ToolErrors int            `json:"toolErrors"`
	// Skills are skill names the run demonstrably used (a read of the skill's
	// own SKILL.md or a skill:// URL).
	Skills  []string    `json:"skills"`
	Sources []SourceRef `json:"sources"`
	// Truncated is true when Summary was cut to the stored bound.
	Truncated bool `json:"truncated,omitempty"`
}

// ResultReader reads a finished Worker's result. A Launcher that implements it
// makes the Dispatcher refuse to call a run done without one.
type ResultReader interface {
	// Collect returns ErrNoResult when nothing trustworthy can be read. Any
	// other error means the read itself failed (the run stays uncertain).
	Collect(ctx context.Context, sess Session, spec LaunchSpec) (WorkerResult, error)
}

// WorkerSessionRoot holds one OMP session directory per run. Pinning the
// directory is what makes the transcript attributable to exactly one run.
func WorkerSessionRoot() string { return filepath.Join(hostpath.StateDir(), "worker-sessions") }

// WorkerSessionDir is the pinned OMP session directory of one run.
func WorkerSessionDir(runID string) string { return filepath.Join(WorkerSessionRoot(), runID) }

// RunMarkerLine is the first line of every Worker prompt: it ties the OMP user
// turn to the run so a transcript can be checked against the run it belongs to.
func RunMarkerLine(runID string) string { return "Solco-Run-Id: " + runID + "\n" }

func hasRunMarker(text, runID string) bool {
	return runID != "" && strings.Contains(text, RunMarkerLine(runID))
}
