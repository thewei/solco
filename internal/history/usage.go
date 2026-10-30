package history

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

/**
 * [INPUT]: 一个 Provider 会话源文件路径（Pi/OMP、Claude Code、Codex 的 JSONL）与 AgentID
 * [OUTPUT]: ReadUsage——按需流式读取一次会话的 token 用量（输入/输出/缓存读/缓存写）与按模型的「路由」聚合（调用数、token、思考档位）；SessionUsage.Total
 * [POS]: Sessions 详情表的只读用量源；不进目录库、不落盘，每次展开现读（超大文件或不支持的 Provider 返回 ErrNoUsage，不是错误态）；只读，不写 Provider 文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ErrNoUsage means the file has no usage to report: an unsupported Provider
// or a source past maxUsageFileBytes.
var ErrNoUsage = errors.New("no usage report for this session")

const (
	// maxUsageFileBytes bounds the file one expand reads.
	maxUsageFileBytes = 256 << 20
	// maxUsageLineBytes bounds one JSONL line; a longer one (a tool result)
	// is skipped, since a usage report is never that large.
	maxUsageLineBytes = 8 << 20
)

// UsageRoute is what one model served in a session: its calls and tokens
// (input plus output), and the thinking level it ran at when one was set.
type UsageRoute struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model"`
	Level    string `json:"level,omitempty"`
	Calls    int    `json:"calls"`
	Tokens   int64  `json:"tokens"`
}

// SessionUsage is a session's token accounting.
type SessionUsage struct {
	Input      int64        `json:"input"`
	Output     int64        `json:"output"`
	CacheRead  int64        `json:"cache_read"`
	CacheWrite int64        `json:"cache_write"`
	Routes     []UsageRoute `json:"routes,omitempty"`
}

// Total is the session's token total as the list shows it: input plus output.
func (u SessionUsage) Total() int64 { return u.Input + u.Output }

// Cache is the cached tokens, read and written.
func (u SessionUsage) Cache() int64 { return u.CacheRead + u.CacheWrite }

// routeBook groups calls by model and level, in the order first met.
type routeBook struct {
	order []string
	byKey map[string]*UsageRoute
}

func (b *routeBook) add(provider, model, level string, tokens int64) {
	if model == "" {
		return
	}
	key := provider + "\x00" + model + "\x00" + level
	if b.byKey == nil {
		b.byKey = map[string]*UsageRoute{}
	}
	r := b.byKey[key]
	if r == nil {
		r = &UsageRoute{Provider: provider, Model: model, Level: level}
		b.byKey[key] = r
		b.order = append(b.order, key)
	}
	r.Calls++
	r.Tokens += tokens
}

func (b *routeBook) routes() []UsageRoute {
	out := make([]UsageRoute, 0, len(b.order))
	for _, key := range b.order {
		out = append(out, *b.byKey[key])
	}
	return out
}

// ReadUsage streams the session file once and totals its usage reports.
func ReadUsage(ctx context.Context, agent AgentID, path string) (SessionUsage, error) {
	var consume func(line []byte)
	var finish func() SessionUsage
	switch agent {
	case AgentPi, AgentOMP:
		c := &piUsage{}
		consume, finish = c.feed, c.finish
	case AgentClaudeCode:
		c := &claudeUsage{byID: map[string]*claudeUsageRow{}}
		consume, finish = c.feed, c.finish
	case AgentCodex:
		c := &codexUsage{}
		consume, finish = c.feed, c.finish
	default:
		return SessionUsage{}, ErrNoUsage
	}
	file, err := os.Open(path)
	if err != nil {
		return SessionUsage{}, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil {
		return SessionUsage{}, err
	} else if info.Size() > maxUsageFileBytes {
		return SessionUsage{}, ErrNoUsage
	}
	reader := bufio.NewReaderSize(file, 1<<20)
	for n := 0; ; n++ {
		if n%512 == 0 && ctx.Err() != nil {
			return SessionUsage{}, ctx.Err()
		}
		line, err := readUsageLine(reader)
		if len(line) > 0 {
			consume(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return SessionUsage{}, err
		}
	}
	return finish(), nil
}

// readUsageLine reads one line; a line over maxUsageLineBytes is dropped
// whole (nil), so the next call starts on the following line.
func readUsageLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	skipped := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !skipped {
			line = append(line, chunk...)
			if len(line) > maxUsageLineBytes {
				line, skipped = nil, true
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if skipped {
			return nil, err
		}
		return line, err
	}
}

func num(v *float64) int64 {
	if v == nil {
		return 0
	}
	return int64(*v)
}

// ---- Pi / Oh My Pi ----

type piUsage struct {
	level string
	sum   SessionUsage
	book  routeBook
}

type piUsageRow struct {
	Type          string `json:"type"`
	ThinkingLevel string `json:"thinkingLevel"`
	Message       *struct {
		Role     string `json:"role"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Usage    *struct {
			Input      *float64 `json:"input"`
			Output     *float64 `json:"output"`
			CacheRead  *float64 `json:"cacheRead"`
			CacheWrite *float64 `json:"cacheWrite"`
		} `json:"usage"`
	} `json:"message"`
}

func (p *piUsage) feed(line []byte) {
	text := string(line)
	if !strings.Contains(text, `"usage"`) && !strings.Contains(text, "thinking_level_change") {
		return
	}
	var row piUsageRow
	if json.Unmarshal(line, &row) != nil {
		return
	}
	if row.Type == "thinking_level_change" {
		p.level = row.ThinkingLevel
		return
	}
	m := row.Message
	if m == nil || m.Role != "assistant" || m.Usage == nil {
		return
	}
	in, out := num(m.Usage.Input), num(m.Usage.Output)
	p.sum.Input += in
	p.sum.Output += out
	p.sum.CacheRead += num(m.Usage.CacheRead)
	p.sum.CacheWrite += num(m.Usage.CacheWrite)
	p.book.add(m.Provider, m.Model, p.level, in+out)
}

func (p *piUsage) finish() SessionUsage {
	p.sum.Routes = p.book.routes()
	return p.sum
}

// ---- Claude Code ----

type claudeUsageRow struct {
	model                         string
	input, output, cacheR, cacheW int64
}

type claudeUsage struct {
	order []string
	byID  map[string]*claudeUsageRow
	anon  int
}

func (c *claudeUsage) feed(line []byte) {
	if !strings.Contains(string(line), `"usage"`) {
		return
	}
	var row struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				Input  *float64 `json:"input_tokens"`
				Output *float64 `json:"output_tokens"`
				CacheW *float64 `json:"cache_creation_input_tokens"`
				CacheR *float64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "assistant" || row.Message.Usage == nil {
		return
	}
	// One response is logged once per content block with the same id and
	// usage: the last report of an id is the response's.
	id := row.Message.ID
	if id == "" {
		c.anon++
		id = "\x00anon" + string(rune(c.anon))
	}
	r := c.byID[id]
	if r == nil {
		r = &claudeUsageRow{}
		c.byID[id] = r
		c.order = append(c.order, id)
	}
	if row.Message.Model != "" && row.Message.Model != "<synthetic>" {
		r.model = row.Message.Model
	}
	u := row.Message.Usage
	r.input, r.output, r.cacheR, r.cacheW = num(u.Input), num(u.Output), num(u.CacheR), num(u.CacheW)
}

func (c *claudeUsage) finish() SessionUsage {
	var sum SessionUsage
	var book routeBook
	for _, id := range c.order {
		r := c.byID[id]
		sum.Input += r.input
		sum.Output += r.output
		sum.CacheRead += r.cacheR
		sum.CacheWrite += r.cacheW
		book.add("", r.model, "", r.input+r.output)
	}
	sum.Routes = book.routes()
	return sum
}

// ---- Codex ----

type codexUsage struct {
	model, effort string
	total         *struct{ in, cached, out int64 }
	book          routeBook
}

func (c *codexUsage) feed(line []byte) {
	text := string(line)
	if !strings.Contains(text, "token_count") && !strings.Contains(text, "turn_context") {
		return
	}
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			Type   string `json:"type"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
			Info   *struct {
				Total *codexTokenUsage `json:"total_token_usage"`
				Last  *codexTokenUsage `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil {
		return
	}
	switch {
	case row.Type == "turn_context":
		if row.Payload.Model != "" {
			c.model = row.Payload.Model
		}
		c.effort = row.Payload.Effort
	case row.Type == "event_msg" && row.Payload.Type == "token_count" && row.Payload.Info != nil:
		info := row.Payload.Info
		if t := info.Total; t != nil {
			c.total = &struct{ in, cached, out int64 }{num(t.Input), num(t.Cached), num(t.Output)}
		}
		if l := info.Last; l != nil {
			if used := max(num(l.Input)-num(l.Cached), 0) + num(l.Output); used > 0 {
				c.book.add("", c.model, c.effort, used)
			}
		}
	}
}

type codexTokenUsage struct {
	Input  *float64 `json:"input_tokens"`
	Cached *float64 `json:"cached_input_tokens"`
	Output *float64 `json:"output_tokens"`
}

func (c *codexUsage) finish() SessionUsage {
	var sum SessionUsage
	if t := c.total; t != nil {
		sum.Input = max(t.in-t.cached, 0)
		sum.CacheRead = t.cached
		sum.Output = t.out
	}
	sum.Routes = c.book.routes()
	return sum
}
