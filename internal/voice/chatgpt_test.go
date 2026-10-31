package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var fixedNow = time.Unix(1_800_000_000, 0)

func jwt(exp int64, plan string) string {
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return enc(map[string]any{"alg": "none"}) + "." + enc(map[string]any{
		"exp":                         exp,
		"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": plan},
	}) + ".sig"
}

func writeAuth(t *testing.T, dir, mode, token string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"auth_mode": mode,
		"tokens":    map[string]any{"access_token": token, "account_id": "acct-1", "refresh_token": "never-read"},
	})
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func client(t *testing.T, home, endpoint string) *ChatGPT {
	return &ChatGPT{Home: home, Endpoint: endpoint, Now: func() time.Time { return fixedNow }}
}

func TestStatusReadsStateAndPlanButNeverTheToken(t *testing.T) {
	home := t.TempDir()
	c := client(t, home, "")
	if got := c.Status(); got.State != SignedOut {
		t.Fatalf("no file: %+v", got)
	}
	writeAuth(t, home, "apikey", jwt(fixedNow.Add(time.Hour).Unix(), "plus"))
	if got := c.Status(); got.State != SignedOut {
		t.Fatalf("api-key login is not a ChatGPT sign-in: %+v", got)
	}
	writeAuth(t, home, "chatgpt", jwt(fixedNow.Add(time.Hour).Unix(), "plus"))
	if got := c.Status(); got.State != Ready || got.Plan != "plus" {
		t.Fatalf("ready: %+v", got)
	}
	writeAuth(t, home, "chatgpt", jwt(fixedNow.Add(30*time.Second).Unix(), "plus"))
	if got := c.Status(); got.State != Expired {
		t.Fatalf("inside the one-minute slack counts as expired: %+v", got)
	}
}

func TestTranscribeSendsTheDesktopAppRequest(t *testing.T) {
	home := t.TempDir()
	token := jwt(fixedNow.Add(time.Hour).Unix(), "plus")
	writeAuth(t, home, "chatgpt", token)
	var gotAuth, gotAccount, gotPath, gotName, gotAgent string
	var gotAudio []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccount, gotPath = r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-Id"), r.URL.Path
		gotAgent = r.Header.Get("User-Agent")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		gotName = header.Filename
		gotAudio, _ = io.ReadAll(file)
		io.WriteString(w, `{"text":" hello there ","asset_pointer":"x"}`)
	}))
	defer srv.Close()

	text, err := client(t, home, srv.URL).Transcribe(context.Background(), []byte("AUDIO"), "audio/webm;codecs=opus")
	if err != nil || text != "hello there" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	// The edge answers library default agents with a 403 challenge page.
	if !strings.HasPrefix(gotAgent, "Codex Desktop/") || !strings.Contains(gotAgent, "(Mac OS; ") {
		t.Fatalf("User-Agent = %q, want the desktop app's own shape", gotAgent)
	}
	if gotAuth != "Bearer "+token || gotAccount != "acct-1" || gotPath != "/transcribe" || gotName != "codex.webm" || !bytes.Equal(gotAudio, []byte("AUDIO")) {
		t.Fatalf("request shape: auth-ok=%v account=%q path=%q name=%q audio=%q", gotAuth == "Bearer "+token, gotAccount, gotPath, gotName, gotAudio)
	}
}

func TestExpiredTokenIsRenewedByCodexThenReread(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, "chatgpt", jwt(fixedNow.Add(-time.Hour).Unix(), "plus"))
	fresh := jwt(fixedNow.Add(time.Hour).Unix(), "plus")
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		io.WriteString(w, `{"text":"ok"}`)
	}))
	defer srv.Close()
	c := client(t, home, srv.URL)
	var renewals int32
	c.Refresh = func(context.Context) error { // Codex rewrites its own file
		atomic.AddInt32(&renewals, 1)
		writeAuth(t, home, "chatgpt", fresh)
		return nil
	}
	if text, err := c.Transcribe(context.Background(), []byte("a"), ""); err != nil || text != "ok" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if renewals != 1 || seen != "Bearer "+fresh {
		t.Fatalf("renewals=%d, used fresh token=%v", renewals, seen == "Bearer "+fresh)
	}
}

func TestFailuresMapToTypedErrorsWithoutLeakingTheToken(t *testing.T) {
	home := t.TempDir()
	token := jwt(fixedNow.Add(time.Hour).Unix(), "plus")
	writeAuth(t, home, "chatgpt", token)
	cases := []struct {
		name   string
		status int
		body   string
		want   error
		text   string
	}{
		{"rate limited", 429, `{}`, ErrLimited, ""},
		{"still unauthorized after a renewal", 401, `{}`, ErrExpired, ""},
		{"edge challenge is not an expiry", 403, `<html><body>Just a moment</body></html>`, ErrBlocked, ""},
		{"server error", 500, `echo ` + token, nil, "failed (500)"},
		{"too short", 400, `{"detail":{"code":"audio_too_short"}}`, nil, "too short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c := client(t, home, srv.URL)
			c.Refresh = func(context.Context) error { return nil }
			_, err := c.Transcribe(context.Background(), []byte("a"), "")
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.text)
			}
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "Bearer") {
				t.Fatalf("error leaks the credential: %v", err)
			}
		})
	}
}

func TestSignedOutAndUnrenewableAreDistinct(t *testing.T) {
	home := t.TempDir()
	c := client(t, home, "http://127.0.0.1:1")
	if _, err := c.Transcribe(context.Background(), []byte("a"), ""); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("no file: %v", err)
	}
	writeAuth(t, home, "chatgpt", jwt(fixedNow.Add(-time.Hour).Unix(), "plus"))
	c.Refresh = func(context.Context) error { return errors.New("codex missing") }
	if _, err := c.Transcribe(context.Background(), []byte("a"), ""); !errors.Is(err, ErrExpired) {
		t.Fatalf("renewal failure: %v", err)
	}
}

func TestRefreshExchangeSendsAccountReadWithRefreshToken(t *testing.T) {
	// A fake codex: answers initialize (id 1) and account/read (id 2).
	serverIn, clientIn := io.Pipe()
	serverOut, clientOut := io.Pipe()
	done := make(chan map[string]any, 1)
	go func() {
		dec := json.NewDecoder(serverIn)
		for {
			var msg map[string]any
			if dec.Decode(&msg) != nil {
				return
			}
			switch msg["method"] {
			case "initialize":
				io.WriteString(clientOut, `{"id":1,"result":{}}`+"\n")
			case "account/read":
				done <- msg
				io.WriteString(clientOut, `{"id":2,"result":{"account":null}}`+"\n")
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := refreshExchange(ctx, clientIn, serverOut); err != nil {
		t.Fatal(err)
	}
	params, _ := (<-done)["params"].(map[string]any)
	if params["refreshToken"] != true {
		t.Fatalf("params = %v", params)
	}
}

func TestDesktopUserAgentReadsInstalledVersion(t *testing.T) {
	old := desktopInfoPlist
	defer func() { desktopInfoPlist = old }()
	plist := filepath.Join(t.TempDir(), "Info.plist")
	body := "<dict>\n\t<key>CFBundleShortVersionString</key>\n\t<string>27.1.2</string>\n</dict>"
	if err := os.WriteFile(plist, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	desktopInfoPlist = plist
	if got := desktopUserAgent(); !strings.HasPrefix(got, "Codex Desktop/27.1.2 (Mac OS; ") {
		t.Fatalf("installed version not used: %q", got)
	}
	desktopInfoPlist = filepath.Join(t.TempDir(), "missing")
	if got := desktopUserAgent(); !strings.HasPrefix(got, "Codex Desktop/"+desktopFallbackVersion) {
		t.Fatalf("fallback version not used: %q", got)
	}
}
