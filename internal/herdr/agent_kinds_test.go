package herdr

import (
	"reflect"
	"testing"
)

func TestParseAgentKinds(t *testing.T) {
	help := `Usage: herdr agent start <NAME> --kind <KIND> --pane <ID> [OPTIONS]

Options:
      --kind <KIND>
          Supported agent kind and canonical executable
          
          [possible values: pi, claude, codex, omp]

      --pane <ID>
`
	if got, want := parseAgentKinds(help), []string{"pi", "claude", "codex", "omp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := parseAgentKinds("no such flag"); got != nil {
		t.Fatalf("unparseable help must yield nil, got %v", got)
	}
}
