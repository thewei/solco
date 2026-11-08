package usage

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

/**
 * [INPUT]: GitHub 登录——手填 PAT 覆盖值，否则 `gh auth token`（用户自己的 gh 登录）
 * [OUTPUT]: Copilot Fetcher（GET api.github.com/copilot_internal/user）、parseCopilot 纯函数
 * [POS]: 只读借用 gh 的 token 或用户手填 PAT，仅发往 api.github.com；只读 quota_snapshots，不触碰 Copilot 其他接口
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	copilotHost = "api.github.com"
	copilotURL  = "https://" + copilotHost + "/copilot_internal/user"
)

type copilot struct {
	client   *http.Client
	endpoint string
	manual   func() string
	// gh returns `gh auth token`; ErrSetup when gh is missing or signed out.
	gh func(ctx context.Context) (string, error)
}

// NewCopilot reads GitHub Copilot's quota; manual returns the token saved in
// Settings, if any, which wins over gh's.
func NewCopilot(manual func() string) Fetcher {
	return &copilot{client: newHTTPClient(), endpoint: copilotURL, manual: manual, gh: ghToken}
}

func (*copilot) ID() ID { return Copilot }

func (c *copilot) Fetch(ctx context.Context) (Snapshot, error) {
	token := ""
	if c.manual != nil {
		token = strings.TrimSpace(c.manual())
	}
	if token == "" {
		var err error
		if token, err = c.gh(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	var body copilotBody
	err := getJSON(ctx, c.client, c.endpoint, map[string]string{
		"Authorization":         "token " + token,
		"Editor-Version":        "vscode/1.99.0",
		"Editor-Plugin-Version": "copilot-chat/0.26.7",
		"User-Agent":            "GitHubCopilotChat/0.26.7",
		"X-Github-Api-Version":  "2025-04-01",
	}, &body)
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := parseCopilot(body)
	if err != nil {
		return Snapshot{}, err
	}
	snap.At = time.Now()
	return snap, nil
}

// ghToken asks the user's own gh CLI for its token.
func ghToken(ctx context.Context) (string, error) {
	binary, err := findExecutable("gh")
	if err != nil {
		return "", fmt.Errorf("%w: GitHub CLI (gh) not found", ErrSetup)
	}
	cmd := exec.CommandContext(ctx, binary, "auth", "token")
	cmd.Env = toolEnv(binary)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: sign in with gh auth login", ErrSetup)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("%w: sign in with gh auth login", ErrSetup)
	}
	return token, nil
}

type copilotQuota struct {
	Entitlement      float64 `json:"entitlement"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percent_remaining"`
	Unlimited        bool    `json:"unlimited"`
}

type copilotBody struct {
	Plan           string `json:"copilot_plan"`
	SKU            string `json:"access_type_sku"`
	QuotaResetDate string `json:"quota_reset_date"`
	QuotaSnapshots struct {
		Premium     *copilotQuota `json:"premium_interactions"`
		Chat        *copilotQuota `json:"chat"`
		Completions *copilotQuota `json:"completions"`
	} `json:"quota_snapshots"`
}

// parseCopilot maps the monthly quota snapshots onto windows: premium
// requests always, chat and completions only when they are capped.
func parseCopilot(b copilotBody) (Snapshot, error) {
	snap := Snapshot{Plan: planTitle(strings.TrimPrefix(b.Plan, "individual_"))}
	if strings.Contains(b.SKU, "free") {
		snap.Plan = "Free"
	}
	var resets time.Time
	if t, err := time.Parse("2006-01-02", b.QuotaResetDate); err == nil {
		resets = t
	} else if t, err := time.Parse(time.RFC3339, b.QuotaResetDate); err == nil {
		resets = t
	}
	var span time.Duration
	if !resets.IsZero() {
		span = resets.Sub(resets.AddDate(0, -1, 0))
	}
	add := func(q *copilotQuota, name string, always bool) {
		if q == nil || q.Unlimited || q.Entitlement <= 0 {
			if q != nil && q.Unlimited && always {
				snap.Notes = append(snap.Notes, name+": unlimited")
			}
			return
		}
		used := q.Entitlement - q.Remaining
		pct := 100 - q.PercentRemaining
		if q.PercentRemaining == 0 && q.Remaining > 0 {
			pct = used / q.Entitlement * 100
		}
		snap.Windows = append(snap.Windows, Window{
			Name: name + " · monthly", UsedPct: clampPct(pct), Span: span, Resets: resets,
			Amount: fmt.Sprintf("%d of %d", int(used+0.5), int(q.Entitlement)),
			Used:   float64(int(used + 0.5)), Max: q.Entitlement, Unit: "requests", Minor: !always,
		})
	}
	add(b.QuotaSnapshots.Premium, "Premium requests", true)
	add(b.QuotaSnapshots.Chat, "Chat", false)
	add(b.QuotaSnapshots.Completions, "Completions", false)
	if len(snap.Windows) == 0 && len(snap.Notes) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Copilot reported no quota", ErrUnavailable)
	}
	return snap, nil
}
