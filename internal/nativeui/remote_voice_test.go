package nativeui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/settings"
)

func voiceWith(vs settings.VoiceSettings) *remoteVoice {
	return &remoteVoice{current: func() settings.Settings { return settings.Settings{Voice: vs} }}
}

// A phone clip is transcribed with the desktop's own source: the endpoint, key
// and model of Settings → Voice, read fresh on every call.
func TestRemoteVoiceUsesTheDesktopEndpointSettings(t *testing.T) {
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"text":"  fix the failing test \n"}`))
	}))
	defer server.Close()

	v := voiceWith(settings.VoiceSettings{EndpointURL: server.URL, APIKey: "sk-desk", Model: "tiny"})
	if !v.Ready() {
		t.Fatal("an endpoint with a key is a ready source")
	}
	text, err := v.Transcribe(context.Background(), []byte("webm-bytes"), "audio/webm")
	if err != nil || text != "fix the failing test" {
		t.Fatalf("text %q, err %v", text, err)
	}
	if gotAuth != "Bearer sk-desk" || !strings.Contains(gotBody, "webm-bytes") || !strings.Contains(gotBody, "tiny") {
		t.Fatalf("auth %q, body %q", gotAuth, gotBody)
	}
}

// A recorder's type may carry parameters or arrive malformed; the upload name stays a plain extension
// and a bad type never panics the Host.
func TestRemoteVoiceNamesTheClipFromAnyMime(t *testing.T) {
	var names []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		names = append(names, regexp.MustCompile(`filename="([^"]*)"`).FindStringSubmatch(body)[1])
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer server.Close()
	v := voiceWith(settings.VoiceSettings{EndpointURL: server.URL, APIKey: "k"})
	for _, mime := range []string{"audio/webm;codecs=opus", "audio/mp4", "nonsense", ""} {
		if _, err := v.Transcribe(context.Background(), []byte("x"), mime); err != nil {
			t.Fatalf("%q: %v", mime, err)
		}
	}
	want := []string{"clip.webm", "clip.mp4", "clip.webm", "clip.webm"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names %v, want %v", names, want)
	}
}

func TestRemoteVoiceRefusesAnEmptyClipAndReportsAuthFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	v := voiceWith(settings.VoiceSettings{EndpointURL: server.URL, APIKey: "bad"})
	if _, err := v.Transcribe(context.Background(), nil, "audio/webm"); err == nil {
		t.Fatal("an empty clip must be refused")
	}
	if _, err := v.Transcribe(context.Background(), []byte("x"), "audio/webm"); err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteVoiceReadinessFollowsTheDesktopSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", t.TempDir()) // no Codex sign-in here
	cases := map[string]struct {
		vs   settings.VoiceSettings
		want bool
	}{
		"nothing configured":          {settings.VoiceSettings{}, false},
		"key":                         {settings.VoiceSettings{APIKey: "sk"}, true},
		"custom endpoint, no key":     {settings.VoiceSettings{EndpointURL: "http://127.0.0.1:9000/v1"}, true},
		"chatgpt without consent":     {settings.VoiceSettings{Source: "chatgpt"}, false},
		"chatgpt consented, no Codex": {settings.VoiceSettings{Source: "chatgpt", ChatGPTConsent: true}, false},
	}
	for name, tc := range cases {
		if got := voiceWith(tc.vs).Ready(); got != tc.want {
			t.Errorf("%s: Ready = %v, want %v", name, got, tc.want)
		}
	}
	t.Setenv("OPENAI_API_KEY", "sk-env")
	if !voiceWith(settings.VoiceSettings{}).Ready() {
		t.Error("OPENAI_API_KEY must make the default endpoint ready")
	}
}
