package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// codexFixture is a trimmed real account/rateLimits/read result (the
// account id is dropped: Solco never reads it).
const codexFixture = `{
 "ordinaryUsageAllowed": true,
 "rateLimits": {
  "limitId": "codex",
  "primary": {"usedPercent": 1, "windowDurationMins": 300, "resetsAt": 1793362966},
  "secondary": {"usedPercent": 19, "windowDurationMins": 10080, "resetsAt": 1793886543},
  "credits": {"hasCredits": false, "unlimited": false, "balance": "0"},
  "planType": "plus"
 },
 "rateLimitsByLimitId": {
  "codex": {"limitId": "codex"},
  "codex_spark": {"limitId": "codex_spark", "limitName": "Spark",
   "primary": {"usedPercent": 40, "windowDurationMins": 300, "resetsAt": 1793362966}}
 },
 "rateLimitResetCredits": {"availableCount": 1, "credits": [
  {"status": "available", "expiresAt": 4102444800}]}
}`

func TestParseCodex(t *testing.T) {
	snap, err := parseCodex(json.RawMessage(codexFixture))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Plan != "Plus" {
		t.Errorf("plan = %q", snap.Plan)
	}
	if len(snap.Windows) != 3 {
		t.Fatalf("windows = %+v", snap.Windows)
	}
	five, week, spark := snap.Windows[0], snap.Windows[1], snap.Windows[2]
	if five.Name != "5-hour" || five.UsedPct != 1 || five.Span != 5*time.Hour {
		t.Errorf("5-hour = %+v", five)
	}
	if week.Name != "Weekly" || week.UsedPct != 19 || week.Resets.Unix() != 1793886543 {
		t.Errorf("weekly = %+v", week)
	}
	if spark.Name != "Spark · 5-hour" || spark.UsedPct != 40 {
		t.Errorf("model limit = %+v", spark)
	}
	joined := strings.Join(snap.Notes, "|")
	if !strings.Contains(joined, "Credits $0") || !strings.Contains(joined, "1 rate-limit reset available") {
		t.Errorf("notes = %q", joined)
	}
}

func TestParseCodexRejectsEmpty(t *testing.T) {
	if _, err := parseCodex(json.RawMessage(`{"rateLimits":{"limitId":"codex"}}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if _, err := parseCodex(json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// fakeCodexServer answers the three-message dance on a pipe pair.
func fakeCodexServer(t *testing.T, authFails bool) (io.Writer, io.Reader) {
	t.Helper()
	requests, clientWrites := io.Pipe()
	clientReads, replies := io.Pipe()
	go func() {
		defer replies.Close()
		scanner := bufio.NewScanner(requests)
		for scanner.Scan() {
			var req struct {
				ID     *int   `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.ID == nil {
				continue
			}
			switch req.Method {
			case "initialize":
				_, _ = io.WriteString(replies, `{"id":1,"result":{"codexHome":"/x"}}`+"\n")
			case "account/rateLimits/read":
				if authFails {
					_, _ = io.WriteString(replies, `{"id":2,"error":{"message":"authentication required"}}`+"\n")
				} else {
					_, _ = io.WriteString(replies, `{"id":2,"result":`+compactJSON(t, codexFixture)+`}`+"\n")
				}
			}
		}
	}()
	return clientWrites, clientReads
}

func TestCodexExchange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, out := fakeCodexServer(t, false)
	raw, err := codexExchange(ctx, in, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseCodex(raw); err != nil {
		t.Fatal(err)
	}
}

func TestCodexExchangeSignedOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, out := fakeCodexServer(t, true)
	if _, err := codexExchange(ctx, in, out); !errors.Is(err, ErrSetup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
}

func TestCodexExchangeTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// A server that never answers.
	requests, clientWrites := io.Pipe()
	go io.Copy(io.Discard, requests)
	clientReads, replies := io.Pipe()
	defer replies.Close()
	if _, err := codexExchange(ctx, clientWrites, clientReads); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
