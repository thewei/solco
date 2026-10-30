package usage

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

/**
 * [INPUT]: 本机正在运行的 Antigravity language server（ps 命令行里的 --csrf_token、lsof 给出的 127.0.0.1 监听端口）
 * [OUTPUT]: Antigravity Fetcher（POST 本机 GetUserStatus）、parseAntigravityPS/parseLsofPorts/parseAntigravity 纯函数
 * [POS]: 零凭据额度来源，仅在 Antigravity App 运行时可用（否则 ErrUnavailable，页面显示“未运行”）；只连 127.0.0.1，自签证书仅对回环地址放行；csrf token 只存在于一次读取的内存里
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type antigravity struct {
	// discover finds the running language server: its csrf token and ports.
	discover func(ctx context.Context) (csrf string, ports []int, err error)
	client   *http.Client
}

// newAntigravityLocal reads Antigravity's model quotas from its local
// language server (only while the app runs).
func newAntigravityLocal() *antigravity {
	return &antigravity{discover: antigravityDiscover, client: &http.Client{
		Timeout: 8 * time.Second,
		// Loopback only: the server's certificate is self-signed.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
	}}
}

func (*antigravity) ID() ID { return Antigravity }

const antigravityPath = "/exa.language_server_pb.LanguageServerService/GetUserStatus"

func (a *antigravity) Fetch(ctx context.Context) (Snapshot, error) {
	csrf, ports, err := a.discover(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	body := []byte(`{"metadata":{"ideName":"antigravity","extensionName":"antigravity","locale":"en"}}`)
	for _, port := range ports {
		for _, scheme := range []string{"https", "http"} {
			raw, ok := a.post(ctx, fmt.Sprintf("%s://127.0.0.1:%d%s", scheme, port, antigravityPath), csrf, body)
			if !ok {
				continue
			}
			snap, err := parseAntigravity(raw)
			if err != nil {
				continue
			}
			snap.At = time.Now()
			return snap, nil
		}
	}
	return Snapshot{}, fmt.Errorf("%w: Antigravity's local server did not answer", ErrUnavailable)
}

func (a *antigravity) post(ctx context.Context, url, csrf string, body []byte) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("X-Codeium-Csrf-Token", csrf)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return raw, err == nil
}

var (
	csrfFlag = regexp.MustCompile(`--csrf_token[ =]([^\s]+)`)
	lsofPort = regexp.MustCompile(`:(\d+) \(LISTEN\)`)
)

// parseAntigravityPS finds Antigravity's language server in `ps -axo
// pid=,command=` output: its pid and csrf token.
func parseAntigravityPS(ps string) (pid int, csrf string, ok bool) {
	for _, line := range strings.Split(ps, "\n") {
		if !strings.Contains(line, "language_server") || !strings.Contains(strings.ToLower(line), "antigravity") {
			continue
		}
		m := csrfFlag.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		return n, m[1], true
	}
	return 0, "", false
}

// parseLsofPorts lists the listening ports in `lsof -nP -iTCP -sTCP:LISTEN`
// output, ascending.
func parseLsofPorts(out string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, m := range lsofPort.FindAllStringSubmatch(out, -1) {
		if p, err := strconv.Atoi(m[1]); err == nil && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	sort.Ints(ports)
	return ports
}

func antigravityDiscover(ctx context.Context) (string, []int, error) {
	psOut, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return "", nil, fmt.Errorf("%w: couldn't look for Antigravity", ErrUnavailable)
	}
	pid, csrf, ok := parseAntigravityPS(string(psOut))
	if !ok {
		return "", nil, fmt.Errorf("%w: Antigravity is not running", ErrUnavailable)
	}
	lsofOut, _ := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-p", strconv.Itoa(pid)).Output()
	ports := parseLsofPorts(string(lsofOut))
	if len(ports) == 0 {
		return "", nil, fmt.Errorf("%w: Antigravity's local server has no open port", ErrUnavailable)
	}
	return csrf, ports, nil
}

type antigravityBody struct {
	UserStatus struct {
		UserTier *struct {
			Name string `json:"name"`
		} `json:"userTier"`
		PlanStatus *struct {
			PlanInfo struct {
				PlanName string `json:"planName"`
			} `json:"planInfo"`
		} `json:"planStatus"`
		CascadeModelConfigData struct {
			ClientModelConfigs []struct {
				Label     string `json:"label"`
				QuotaInfo *struct {
					RemainingFraction *float64 `json:"remainingFraction"`
					ResetTime         string   `json:"resetTime"`
				} `json:"quotaInfo"`
			} `json:"clientModelConfigs"`
		} `json:"cascadeModelConfigData"`
	} `json:"userStatus"`
}

// parseAntigravity groups models that share a quota (same remaining share and
// reset) into one window named after the first of them.
func parseAntigravity(raw []byte) (Snapshot, error) {
	var b antigravityBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return Snapshot{}, fmt.Errorf("%w: unexpected Antigravity response", ErrUnavailable)
	}
	type group struct {
		w     Window
		count int
	}
	var order []string
	groups := map[string]*group{}
	for _, m := range b.UserStatus.CascadeModelConfigData.ClientModelConfigs {
		q := m.QuotaInfo
		if q == nil || q.RemainingFraction == nil {
			continue
		}
		key := fmt.Sprintf("%.4f|%s", *q.RemainingFraction, q.ResetTime)
		if g := groups[key]; g != nil {
			g.count++
			continue
		}
		w := Window{Name: m.Label, UsedPct: clampPct((1 - *q.RemainingFraction) * 100)}
		if t, err := time.Parse(time.RFC3339, q.ResetTime); err == nil {
			w.Resets = t
		}
		groups[key] = &group{w: w, count: 1}
		order = append(order, key)
	}
	var snap Snapshot
	for _, key := range order {
		g := groups[key]
		if g.count > 1 {
			g.w.Name += fmt.Sprintf(" +%d", g.count-1)
		}
		snap.Windows = append(snap.Windows, g.w)
	}
	switch {
	case b.UserStatus.UserTier != nil && b.UserStatus.UserTier.Name != "":
		snap.Plan = b.UserStatus.UserTier.Name
	case b.UserStatus.PlanStatus != nil:
		snap.Plan = b.UserStatus.PlanStatus.PlanInfo.PlanName
	}
	if len(snap.Windows) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Antigravity reported no quotas", ErrUnavailable)
	}
	return snap, nil
}
