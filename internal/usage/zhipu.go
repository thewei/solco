package usage

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

/**
 * [INPUT]: 用户在 Settings 里保存的智谱 API Key 与站点（国际 api.z.ai / 国内 open.bigmodel.cn）
 * [OUTPUT]: Zhipu Fetcher（GET <站点>/api/monitor/usage/quota/limit）、parseZhipu 纯函数
 * [POS]: 唯一需要手填 Key 的来源；Key 只发往所选站点本身，不读环境变量，不落日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Zhipu sites.
const (
	ZhipuGlobal = "global"
	ZhipuChina  = "china"
)

var zhipuEndpoints = map[string]string{
	ZhipuGlobal: "https://api.z.ai/api/monitor/usage/quota/limit",
	ZhipuChina:  "https://open.bigmodel.cn/api/monitor/usage/quota/limit",
}

type zhipu struct {
	client *http.Client
	key    func() string
	site   func() string
	// endpoint overrides the site's URL (tests).
	endpoint string
}

// NewZhipu reads the GLM coding plan's quota with the saved API key; site
// returns ZhipuGlobal or ZhipuChina.
func NewZhipu(key, site func() string) Fetcher {
	return &zhipu{client: newHTTPClient(), key: key, site: site}
}

func (*zhipu) ID() ID { return Zhipu }

func (z *zhipu) Fetch(ctx context.Context) (Snapshot, error) {
	key := strings.TrimSpace(z.key())
	if key == "" {
		return Snapshot{}, fmt.Errorf("%w: add your Zhipu API key in Usage settings", ErrSetup)
	}
	endpoint := z.endpoint
	if endpoint == "" {
		endpoint = zhipuEndpoints[ZhipuGlobal]
		if z.site() == ZhipuChina {
			endpoint = zhipuEndpoints[ZhipuChina]
		}
	}
	var body zhipuBody
	if err := getJSON(ctx, z.client, endpoint, map[string]string{"Authorization": "Bearer " + key}, &body); err != nil {
		return Snapshot{}, err
	}
	if body.Code != 0 && body.Code != 200 {
		if body.Code == 401 || body.Code == 1001 || body.Code == 1002 {
			return Snapshot{}, ErrUnauthorized
		}
		return Snapshot{}, fmt.Errorf("%w: Zhipu answered code %d", ErrUnavailable, body.Code)
	}
	snap, err := parseZhipu(body)
	if err != nil {
		return Snapshot{}, err
	}
	snap.At = time.Now()
	return snap, nil
}

type zhipuLimit struct {
	Type          string  `json:"type"`
	Unit          int     `json:"unit"`
	Number        int     `json:"number"`
	Usage         float64 `json:"usage"`
	CurrentValue  float64 `json:"currentValue"`
	Percentage    float64 `json:"percentage"`
	NextResetTime int64   `json:"nextResetTime"` // ms
}

type zhipuBody struct {
	Code int `json:"code"`
	Data struct {
		Level  string       `json:"level"`
		Limits []zhipuLimit `json:"limits"`
	} `json:"data"`
}

// zhipuSpan is a limit's window: unit names the step (day, hour, minute,
// month, week) and number how many of them.
func zhipuSpan(unit, number int) time.Duration {
	if number <= 0 {
		number = 1
	}
	var step time.Duration
	switch unit {
	case 1:
		step = 24 * time.Hour
	case 3:
		step = time.Hour
	case 4:
		step = time.Minute
	case 5:
		step = 30 * 24 * time.Hour
	case 6:
		step = 7 * 24 * time.Hour
	default:
		return 0
	}
	return time.Duration(number) * step
}

// parseZhipu maps the quota limits onto windows: token limits by their span
// (5-hour, weekly), the MCP/tool-call limit as its own monthly one.
func parseZhipu(b zhipuBody) (Snapshot, error) {
	snap := Snapshot{Plan: planTitle(b.Data.Level)}
	for _, l := range b.Data.Limits {
		span := zhipuSpan(l.Unit, l.Number)
		pct := l.Percentage
		if pct == 0 && l.Usage > 0 {
			pct = l.CurrentValue / l.Usage * 100
		}
		w := Window{UsedPct: clampPct(pct), Span: span}
		if l.NextResetTime > 0 {
			w.Resets = time.UnixMilli(l.NextResetTime)
		}
		switch l.Type {
		case "TOKENS_LIMIT":
			w.Name = spanName(span)
		case "TIME_LIMIT":
			w.Name = "MCP · " + strings.ToLower(spanName(span))
			if l.Usage > 0 {
				w.Amount = fmt.Sprintf("%d of %d", int(l.CurrentValue), int(l.Usage))
				w.Used, w.Max, w.Unit = l.CurrentValue, l.Usage, "calls"
			}
		default:
			continue
		}
		snap.Windows = append(snap.Windows, w)
	}
	if len(snap.Windows) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Zhipu reported no limits", ErrUnavailable)
	}
	return snap, nil
}
