package notifyhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/secretstore"
	"github.com/thewei/solco/internal/settings"
)

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://worker.example.com/v1/notify", "http://localhost:8787/x", "http://127.0.0.1:9/x"} {
		if err := ValidateURL(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://example.com/x", "ftp://example.com", "https://user:pw@example.com", "example.com", "http://192.168.1.5/x"} {
		if err := ValidateURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLoadReadsOnlyEnabledChannelsWithUsableSecrets(t *testing.T) {
	dir := t.TempDir()
	cur := settings.Default()
	cur.Notifications = settings.NotificationSettings{GraceSeconds: 3, Channels: []settings.NotificationChannel{
		{ID: "a", Name: "Feishu", Enabled: true},
		{ID: "b", Name: "Off", Enabled: false},
		{ID: "c", Name: "No secret", Enabled: true},
		{ID: "d", Name: "Plain http", Enabled: true},
	}}
	if err := settings.NewFileStore(dir).Save(context.Background(), cur); err != nil {
		t.Fatal(err)
	}
	st := secretstore.New(nil)
	st.Use(filepath.Join(dir, "secrets.enc"))
	_ = st.Set(SecretKey("a"), EncodeSecret("https://w.example.com/v1/notify", "tok"))
	_ = st.Set(SecretKey("b"), EncodeSecret("https://off.example.com", ""))
	_ = st.Set(SecretKey("d"), EncodeSecret("http://example.com/x", ""))

	targets, grace := Load(dir)
	if len(targets) != 1 || targets[0].Name != "Feishu" || targets[0].Token != "tok" {
		t.Fatalf("targets = %+v", targets)
	}
	if grace.Seconds() != float64(settings.MinNotificationGrace) {
		t.Errorf("grace = %v, want the floor %ds", grace, settings.MinNotificationGrace)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "settings.json")); strings.Contains(string(raw), "w.example.com") {
		t.Error("the URL leaked into settings.json")
	}
}

func TestSendPostsJSONWithTheBearerAndNeverLeaksTheURL(t *testing.T) {
	var got Event
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	Sender{}.Send(context.Background(), []Target{{Name: "t", URL: srv.URL + "/hook", Token: "s3"}},
		Event{Kind: KindQuestion, Title: "solco · Claude Code", Text: "Ship it?", Thread: "t_1", Item: "a_1", Link: "solco://feedback?thread=t_1&ask=a_1"})
	if auth != "Bearer s3" || got.Kind != "question" || got.Item != "a_1" || got.Link == "" {
		t.Fatalf("auth %q, body %+v", auth, got)
	}
	err := Sender{}.post(context.Background(), Target{Name: "dead", URL: "http://127.0.0.1:1/secret-path-xyz"}, Event{})
	if err == nil || strings.Contains(err.Error(), "secret-path-xyz") {
		t.Errorf("error = %v", err)
	}
}

func TestWordsAndExcerptAreBounded(t *testing.T) {
	if got := Words("first line\nsecond", 80); got != "first line" {
		t.Errorf("Words = %q", got)
	}
	if got := Words(strings.Repeat("字", 100), 10); len([]rune(got)) != 10 {
		t.Errorf("Words runes = %d", len([]rune(got)))
	}
	if got := Excerpt(strings.Repeat("x", 600), 500); len([]rune(got)) != 500 {
		t.Errorf("Excerpt runes = %d", len([]rune(got)))
	}
}
