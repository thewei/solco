package personalagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHubFetcher reads pull requests from GitHub's REST API. A public
// repository needs no credential; a private one uses GH_TOKEN/GITHUB_TOKEN or
// the user's own `gh` login. The token is read when needed, kept in memory
// only, and never logged or stored.
type GitHubFetcher struct {
	BaseURL string // default https://api.github.com
	Client  *http.Client
	// Token overrides the credential lookup (tests).
	Token func() string

	once  sync.Once
	token string
}

func (g *GitHubFetcher) base() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/")
	}
	return "https://api.github.com"
}

func (g *GitHubFetcher) credential() string {
	if g.Token != nil {
		return g.Token()
	}
	g.once.Do(func() {
		for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				g.token = v
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
			g.token = strings.TrimSpace(string(out))
		}
	})
	return g.token
}

// FetchPR implements PRFetcher.
func (g *GitHubFetcher) FetchPR(ctx context.Context, repo string, number int) (PRState, error) {
	if !repoRe.MatchString(repo) || number <= 0 {
		return PRState{}, fmt.Errorf("%w: bad repo or number", ErrInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/pulls/%d", g.base(), repo, number), nil)
	if err != nil {
		return PRState{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "solco-assistants")
	if tok := g.credential(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return PRState{}, fmt.Errorf("%w: %v", ErrPRUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		var body struct {
			State  string `json:"state"`
			Merged bool   `json:"merged"`
			Title  string `json:"title"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return PRState{}, fmt.Errorf("%w: unreadable answer", ErrPRUnavailable)
		}
		return PRState{State: body.State, Merged: body.Merged, HeadSHA: body.Head.SHA, Title: body.Title}, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return PRState{}, ErrPRUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" {
			return PRState{}, &RateLimitError{RetryAfter: retryAfter(resp)}
		}
		return PRState{}, ErrPRUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return PRState{}, ErrPRNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return PRState{}, &RateLimitError{RetryAfter: retryAfter(resp)}
	}
	return PRState{}, fmt.Errorf("%w: status %d", ErrPRUnavailable, resp.StatusCode)
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			if d := time.Until(time.Unix(n, 0)); d > 0 {
				return d
			}
		}
	}
	return time.Minute
}
