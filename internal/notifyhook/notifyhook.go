// Package notifyhook calls the user's own webhook channels when the phone could not be told
// (product decision 2026-11-09). It is transport only: no Agent, no runtime, no content beyond the
// event's own words.
//
// [INPUT]: the channel list in settings.json and each channel's URL/token in the credential file
// [OUTPUT]: Event, Target, Load, Sender.Send, SecretKey, ValidateURL, Words
// [POS]: used by remotehost (which knows whether a phone is connected) and nativeui (Settings → Notifications)
// [PROTOCOL]: the URL is a secret: never logged, never returned in an error; a channel is named by its Name in logs
package notifyhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/internal/secretstore"
	"github.com/thewei/solco/internal/settings"
)

// Event kinds. "question" matches what the Feishu Worker already expects.
const (
	KindQuestion   = "question"
	KindAgent      = "agent"
	KindAssistant  = "assistant"
	KindAutomation = "automation"
)

// Event is one thing the user should be told about. Every field is bounded text the Mac already shows in its
// own notification; there is no path, no answer and no image.
type Event struct {
	Kind    string `json:"event"`
	Title   string `json:"title,omitempty"`
	Text    string `json:"text,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
	// Summary is a bounded excerpt of the description an Agent attached to its question (the user chose to
	// send it, 2026-11-09).
	Summary string `json:"summary,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Item    string `json:"item,omitempty"`
	// Link is the solco:// deep link the phone opens.
	Link string `json:"link,omitempty"`
}

// Target is one enabled channel with its secrets.
type Target struct {
	Name  string
	URL   string
	Token string
}

// SecretKey is the credential-file key of one channel's {url, token}.
func SecretKey(id string) string { return "notifyhook.channel." + id }

type secret struct {
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
}

// EncodeSecret is the value stored under SecretKey.
func EncodeSecret(rawURL, token string) string {
	b, _ := json.Marshal(secret{URL: strings.TrimSpace(rawURL), Token: strings.TrimSpace(token)})
	return string(b)
}

// DecodeSecret reads a stored value back.
func DecodeSecret(value string) (rawURL, token string, ok bool) {
	var sec secret
	if json.Unmarshal([]byte(value), &sec) != nil || sec.URL == "" {
		return "", "", false
	}
	return sec.URL, sec.Token, true
}

// ErrBadURL is returned for a URL a channel may not use. It never carries the URL.
var ErrBadURL = errors.New("a webhook URL must be https (http only for localhost)")

// ValidateURL accepts https URLs, and http only toward the machine itself (a tunnel or a local test server).
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return ErrBadURL
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return ErrBadURL
}

// Load reads the enabled channels and the grace period for the data directory. It is read per event, so a change
// in Settings needs no restart of the resident Host. Channels without a usable secret are skipped.
func Load(userDataDir string) (targets []Target, grace time.Duration) {
	cur, err := settings.NewFileStore(userDataDir).Load(context.Background())
	if err != nil {
		return nil, time.Duration(settings.DefaultNotificationGrace) * time.Second
	}
	grace = time.Duration(cur.Notifications.Grace()) * time.Second
	store := secretstore.New(nil)
	store.Use(filepath.Join(userDataDir, "secrets.enc"))
	for _, ch := range cur.Notifications.Channels {
		if !ch.Enabled {
			continue
		}
		value, ok := store.Get(SecretKey(ch.ID))
		if !ok {
			continue
		}
		rawURL, token, ok := DecodeSecret(value)
		if !ok || ValidateURL(rawURL) != nil {
			continue
		}
		name := strings.TrimSpace(ch.Name)
		if name == "" {
			name = ch.ID
		}
		targets = append(targets, Target{Name: name, URL: rawURL, Token: token})
	}
	return targets, grace
}

// Sender posts events. The zero value is ready.
type Sender struct {
	Client *http.Client
}

const sendTimeout = 10 * time.Second

// Send posts ev as JSON to every target at once and waits for them. A failing channel is logged by name and
// status only and never fails the others.
func (s Sender) Send(ctx context.Context, targets []Target, ev Event) {
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.post(ctx, t, ev); err != nil {
				slog.Warn("notification channel failed", "channel", t.Name, "error", err)
			}
		}()
	}
	wg.Wait()
}

func (s Sender) post(ctx context.Context, t Target, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	cl := s.Client
	if cl == nil {
		cl = &http.Client{}
	}
	resp, err := cl.Do(req)
	if err != nil {
		// net/http's error embeds the URL; say only what failed.
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return errors.New("timeout")
		}
		return errors.New("unreachable")
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("refused: status %d", resp.StatusCode)
	}
	return nil
}

// Words bounds one line of text for an Event.
func Words(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// Excerpt bounds a multi-line text (a description) to max runes.
func Excerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
