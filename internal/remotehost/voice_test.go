package remotehost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
)

type fakeVoice struct {
	mu    sync.Mutex
	ready bool
	text  string
	err   error
	got   [][]byte
	mime  string
}

func (f *fakeVoice) Ready() bool { return f.ready }

func (f *fakeVoice) Transcribe(_ context.Context, audio []byte, mime string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, audio)
	f.mime = mime
	return f.text, f.err
}

func voiceBody(audio []byte, mime string) string {
	raw, _ := json.Marshal(voiceRequest{DataBase64: base64.StdEncoding.EncodeToString(audio), Mime: mime})
	return string(raw)
}

func voiceHarness(t *testing.T) (*harness, *fakeVoice) {
	t.Helper()
	h := newHarness(t)
	f := &fakeVoice{ready: true, text: "fix the failing test"}
	h.srv.opts.Voice = f
	return h, f
}

func TestVoiceTranscribesWithTheDesktopSource(t *testing.T) {
	h, f := voiceHarness(t)
	resp := h.launch("/api/v3/voice/transcribe", h.token, voiceBody([]byte("webm-bytes"), "audio/webm"))
	var out voiceReply
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || out.Text != "fix the failing test" {
		t.Fatalf("status %d text %q", resp.StatusCode, out.Text)
	}
	if len(f.got) != 1 || string(f.got[0]) != "webm-bytes" || f.mime != "audio/webm" {
		t.Fatalf("seam got %q (%s)", f.got, f.mime)
	}
}

func TestVoiceRefusals(t *testing.T) {
	h, f := voiceHarness(t)
	good := voiceBody([]byte("x"), "audio/webm")
	for _, c := range []struct {
		name, token, body string
		status            int
		code              string
	}{
		{"no token", "", good, 401, "unauthorized"},
		{"not json", h.token, "nope", 400, "bad_request"},
		{"not base64", h.token, `{"dataBase64":"@@@"}`, 400, "bad_request"},
		{"empty", h.token, `{"dataBase64":""}`, 400, "bad_request"},
	} {
		resp := h.launch("/api/v3/voice/transcribe", c.token, c.body)
		if resp.StatusCode != c.status || (c.code != "unauthorized" && errCode(t, resp) != c.code) {
			t.Errorf("%s: status %d, want %d/%s", c.name, resp.StatusCode, c.status, c.code)
		}
	}
	if len(f.got) != 0 {
		t.Fatalf("a refused request reached the source: %d calls", len(f.got))
	}
	big := make([]byte, maxVoiceAudio+1)
	if resp := h.launch("/api/v3/voice/transcribe", h.token, voiceBody(big, "audio/webm")); resp.StatusCode != http.StatusRequestEntityTooLarge || errCode(t, resp) != "audio_too_large" {
		t.Fatalf("oversize: status %d", resp.StatusCode)
	}
	f.err = errors.New("transcription auth failed — check the voice API key")
	if resp := h.launch("/api/v3/voice/transcribe", h.token, good); resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "transcribe_failed" {
		t.Fatalf("source failure: status %d", resp.StatusCode)
	}
}

func TestVoiceIsAdvertisedOnlyWhenTheDesktopSourceIsReady(t *testing.T) {
	h, f := voiceHarness(t)
	caps := func() map[string]bool {
		var body struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.Capabilities
	}
	if !caps()["voice"] {
		t.Fatal("a ready source must be advertised")
	}
	f.ready = false
	if caps()["voice"] {
		t.Fatal("an unconfigured source must not be advertised")
	}
	if resp := h.launch("/api/v3/voice/transcribe", h.token, voiceBody([]byte("x"), "")); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "voice_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	h.srv.opts.Voice = nil
	if caps()["voice"] {
		t.Fatal("no seam, no voice")
	}
}
