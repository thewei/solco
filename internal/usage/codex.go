package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

/**
 * [INPUT]: 本机 `codex app-server`（stdio JSON-RPC）的 account/rateLimits/read
 * [OUTPUT]: Codex Fetcher（无需任何凭据：登录态由 codex 自己持有）、parseCodex 纯函数
 * [POS]: 零凭据额度来源；每次读取起一个短命子进程并在读完后杀掉，不保留常驻进程，不读 auth 文件，不转发 accountId
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type codex struct {
	// run starts the exchange; tests replace it with a fake server.
	run func(ctx context.Context) (json.RawMessage, error)
}

// NewCodex reads Codex's limits from its own local app-server.
func NewCodex() Fetcher { return &codex{run: codexAppServer} }

func (*codex) ID() ID { return Codex }

func (c *codex) Fetch(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	raw, err := c.run(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return parseCodex(raw)
}

// codexAppServer spawns `codex app-server`, asks for the rate limits and
// kills it.
func codexAppServer(ctx context.Context) (json.RawMessage, error) {
	binary, err := findExecutable("codex")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Env = toolEnv(binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: codex did not start", ErrUnavailable)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	return codexExchange(ctx, stdin, stdout)
}

// codexExchange speaks the three JSON-RPC messages over a pipe pair and
// returns the rateLimits result.
func codexExchange(ctx context.Context, in io.Writer, out io.Reader) (json.RawMessage, error) {
	send := func(v any) error {
		line, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = in.Write(append(line, '\n'))
		return err
	}
	type reply struct {
		ID     *int            `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	replies := make(chan reply, 4)
	go func() {
		defer close(replies)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 0, 64<<10), maxBody)
		for scanner.Scan() {
			var r reply
			if json.Unmarshal(scanner.Bytes(), &r) == nil && r.ID != nil {
				replies <- r
			}
		}
	}()
	wait := func(id int) (json.RawMessage, error) {
		for {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: codex did not answer", ErrUnavailable)
			case r, ok := <-replies:
				if !ok {
					return nil, fmt.Errorf("%w: codex closed", ErrUnavailable)
				}
				if *r.ID != id {
					continue
				}
				if r.Error != nil {
					if strings.Contains(strings.ToLower(r.Error.Message), "auth") {
						return nil, fmt.Errorf("%w: sign in to Codex", ErrSetup)
					}
					return nil, fmt.Errorf("%w: codex refused the request", ErrUnavailable)
				}
				return r.Result, nil
			}
		}
	}
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "solco", "version": "1"}}}); err != nil {
		return nil, fmt.Errorf("%w: codex closed", ErrUnavailable)
	}
	if _, err := wait(1); err != nil {
		return nil, err
	}
	_ = send(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "account/rateLimits/read"}); err != nil {
		return nil, fmt.Errorf("%w: codex closed", ErrUnavailable)
	}
	return wait(2)
}

type codexWindow struct {
	UsedPercent float64 `json:"usedPercent"`
	DurationMin float64 `json:"windowDurationMins"`
	ResetsAt    int64   `json:"resetsAt"`
}

type codexLimit struct {
	LimitID   string       `json:"limitId"`
	LimitName string       `json:"limitName"`
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	Credits   *struct {
		HasCredits bool   `json:"hasCredits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
	PlanType string `json:"planType"`
}

func (w *codexWindow) window(name string) Window {
	span := time.Duration(w.DurationMin * float64(time.Minute))
	out := Window{Name: name, UsedPct: clampPct(w.UsedPercent), Span: span}
	if name == "" {
		out.Name = spanName(span)
	}
	if w.ResetsAt > 0 {
		out.Resets = time.Unix(w.ResetsAt, 0)
	}
	return out
}

// parseCodex turns the account/rateLimits/read result into a Snapshot.
func parseCodex(raw json.RawMessage) (Snapshot, error) {
	var res struct {
		RateLimits            *codexLimit           `json:"rateLimits"`
		ByID                  map[string]codexLimit `json:"rateLimitsByLimitId"`
		RateLimitResetCredits *struct {
			AvailableCount int `json:"availableCount"`
			Credits        []struct {
				Status    string `json:"status"`
				ExpiresAt int64  `json:"expiresAt"`
			} `json:"credits"`
		} `json:"rateLimitResetCredits"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.RateLimits == nil {
		return Snapshot{}, fmt.Errorf("%w: unexpected Codex response", ErrUnavailable)
	}
	main := res.RateLimits
	snap := Snapshot{Plan: planTitle(main.PlanType), At: time.Now()}
	add := func(l *codexLimit, prefix string) {
		for _, w := range []*codexWindow{l.Primary, l.Secondary} {
			if w == nil {
				continue
			}
			win := w.window("")
			if prefix != "" {
				win.Name = prefix + " · " + win.Name
				win.Minor = true
			}
			snap.Windows = append(snap.Windows, win)
		}
	}
	add(main, "")
	// Model-specific limits ride alongside the main one under their own id.
	ids := make([]string, 0, len(res.ByID))
	for id := range res.ByID {
		if id != main.LimitID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		l := res.ByID[id]
		name := l.LimitName
		if name == "" {
			name = id
		}
		add(&l, name)
	}
	var notes []string
	if c := main.Credits; c != nil {
		switch {
		case c.Unlimited:
			notes = append(notes, "Unlimited credits")
		case c.HasCredits || c.Balance != "":
			notes = append(notes, "Credits $"+fallback(c.Balance, "0"))
		}
	}
	if r := res.RateLimitResetCredits; r != nil && r.AvailableCount > 0 {
		note := fmt.Sprintf("%d rate-limit reset available", r.AvailableCount)
		var soonest int64
		for _, c := range r.Credits {
			if c.Status == "available" && c.ExpiresAt > 0 && (soonest == 0 || c.ExpiresAt < soonest) {
				soonest = c.ExpiresAt
			}
		}
		if soonest > 0 {
			note += " (expires " + shortWhen(time.Until(time.Unix(soonest, 0))) + ")"
		}
		notes = append(notes, note)
	}
	snap.Notes = notes
	if len(snap.Windows) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Codex reported no limits", ErrUnavailable)
	}
	return snap, nil
}

func fallback(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// planTitle capitalizes a vendor plan id ("plus" → "Plus").
func planTitle(plan string) string {
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return ""
	}
	plan = strings.ReplaceAll(plan, "_", " ")
	return strings.ToUpper(plan[:1]) + plan[1:]
}

// shortWhen is "in 27 d" / "in 5 h" for a duration ahead.
func shortWhen(d time.Duration) string {
	switch {
	case d <= 0:
		return "now"
	case d >= 48*time.Hour:
		return fmt.Sprintf("in %d d", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("in %d h", int(d.Hours()))
	}
	return fmt.Sprintf("in %d min", max(int(d.Minutes()), 1))
}
