package personalagent

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fetcherFor(t *testing.T, h http.HandlerFunc) *GitHubFetcher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &GitHubFetcher{BaseURL: srv.URL, Token: func() string { return "tok" }}
}

func TestGitHubFetcherReadsFacts(t *testing.T) {
	var gotAuth, gotPath string
	f := fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"state":"closed","merged":true,"title":"t","head":{"sha":"abc123"}}`))
	})
	st, err := f.FetchPR(t.Context(), "o/r", 12)
	if err != nil || st.State != "closed" || !st.Merged || st.HeadSHA != "abc123" {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	if gotPath != "/repos/o/r/pulls/12" || gotAuth != "Bearer tok" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
}

func TestGitHubFetcherMapsFailures(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		headers map[string]string
		check   func(error) bool
	}{
		{"401", 401, nil, func(e error) bool { return errors.Is(e, ErrPRUnauthorized) }},
		{"403 forbidden", 403, nil, func(e error) bool { return errors.Is(e, ErrPRUnauthorized) }},
		{"403 rate", 403, map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "90"}, func(e error) bool {
			var rl *RateLimitError
			return errors.As(e, &rl) && rl.RetryAfter == 90*time.Second
		}},
		{"404", 404, nil, func(e error) bool { return errors.Is(e, ErrPRNotFound) }},
		{"429", 429, map[string]string{"Retry-After": "30"}, func(e error) bool {
			var rl *RateLimitError
			return errors.As(e, &rl) && rl.RetryAfter == 30*time.Second
		}},
		{"500", 500, nil, func(e error) bool { return errors.Is(e, ErrPRUnavailable) }},
		{"502", 502, nil, func(e error) bool { return errors.Is(e, ErrPRUnavailable) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := fetcherFor(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
			})
			if _, err := f.FetchPR(t.Context(), "o/r", 1); err == nil || !c.check(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestGitHubFetcherOfflineIsAnError(t *testing.T) {
	f := &GitHubFetcher{BaseURL: "http://127.0.0.1:1", Token: func() string { return "" }}
	if _, err := f.FetchPR(t.Context(), "o/r", 1); err == nil {
		t.Fatal("offline read reported success")
	}
}

func TestGitHubFetcherRejectsBadRepo(t *testing.T) {
	f := &GitHubFetcher{BaseURL: "http://127.0.0.1:1"}
	if _, err := f.FetchPR(t.Context(), "../../etc", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
