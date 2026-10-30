package history

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: Provider 来源根（Claude Code、Codex 的 JSONL）与一个起始日期
 * [OUTPUT]: DailyUsageReader.Read——近 N 天按本地日历日聚合的 token 数（输入+输出，与会话列表同口径）；单文件按 (mtime,size) 在内存里缓存
 * [POS]: Usage 页“最近 30 天”柱图的只读数据源；不落盘、不记录历史（用户 2026-10-30：不记录历史数据），进程退出即清空；只读 Provider 文件，超大文件/超长行沿用 ReadUsage 的上限
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// DayTokens is one local calendar day's tokens.
type DayTokens struct {
	Day    time.Time // local midnight
	Tokens int64
}

// DailyUsageReader totals tokens per day across the Claude Code and Codex
// session files touched since a date. A file is parsed once per
// (mtime, size); the cache lives in memory only.
type DailyUsageReader struct {
	roots []SourceRoot

	mu    sync.Mutex
	cache map[string]dailyEntry
}

type dailyEntry struct {
	mtimeMS, size int64
	days          map[string]int64 // "2006-01-02" → tokens
}

// NewDailyUsageReader reads the given roots; only Claude Code and Codex roots
// are used.
func NewDailyUsageReader(roots []SourceRoot) *DailyUsageReader {
	return &DailyUsageReader{roots: roots, cache: map[string]dailyEntry{}}
}

const dayLayout = "2006-01-02"

// Read returns, per Agent, the days from since (inclusive, local midnight)
// through today, oldest first, zero days included.
func (r *DailyUsageReader) Read(ctx context.Context, since time.Time, loc *time.Location) (map[AgentID][]DayTokens, error) {
	if loc == nil {
		loc = time.Local
	}
	first := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
	type job struct {
		agent AgentID
		ref   SessionFileRef
	}
	var jobs []job
	for _, root := range r.roots {
		if root.Agent != AgentClaudeCode && root.Agent != AgentCodex {
			continue
		}
		refs, err := listJSONLRefs(root.Directory, root.Agent, root.NativeID)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			// A file untouched since the window began holds nothing in it.
			if ref.MtimeMS >= first.UnixMilli() {
				jobs = append(jobs, job{root.Agent, ref})
			}
		}
	}

	totals := map[AgentID]map[string]int64{AgentClaudeCode: {}, AgentCodex: {}}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		queue    = make(chan job)
	)
	for range min(maxScanWorkers, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				days, err := r.fileDays(ctx, j.agent, j.ref, loc)
				mu.Lock()
				switch {
				case errors.Is(err, ErrNoUsage):
				case err != nil:
					if firstErr == nil && ctx.Err() == nil {
						firstErr = err
					}
				default:
					for day, n := range days {
						totals[j.agent][day] += n
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		queue <- j
	}
	close(queue)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}

	out := map[AgentID][]DayTokens{}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	for agent, byDay := range totals {
		var series []DayTokens
		for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
			series = append(series, DayTokens{Day: d, Tokens: byDay[d.Format(dayLayout)]})
		}
		out[agent] = series
	}
	return out, nil
}

// fileDays is one file's per-day totals, from the cache when the file is
// unchanged.
func (r *DailyUsageReader) fileDays(ctx context.Context, agent AgentID, ref SessionFileRef, loc *time.Location) (map[string]int64, error) {
	r.mu.Lock()
	hit, ok := r.cache[ref.FilePath]
	r.mu.Unlock()
	if ok && hit.mtimeMS == ref.MtimeMS && hit.size == ref.SizeBytes {
		return hit.days, nil
	}
	days, err := readFileDays(ctx, agent, ref.FilePath, loc)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[ref.FilePath] = dailyEntry{mtimeMS: ref.MtimeMS, size: ref.SizeBytes, days: days}
	r.mu.Unlock()
	return days, nil
}

// readFileDays streams one session file and buckets its token reports by the
// local day of their timestamp.
func readFileDays(ctx context.Context, agent AgentID, path string, loc *time.Location) (map[string]int64, error) {
	var consume func(line []byte)
	var finish func() map[string]int64
	switch agent {
	case AgentClaudeCode:
		c := &claudeDaily{loc: loc, byID: map[string]claudeDailyRow{}}
		consume, finish = c.feed, c.finish
	case AgentCodex:
		c := &codexDaily{loc: loc, days: map[string]int64{}}
		consume, finish = c.feed, c.finish
	default:
		return nil, ErrNoUsage
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil {
		return nil, err
	} else if info.Size() > maxUsageFileBytes {
		return nil, ErrNoUsage
	}
	reader := bufio.NewReaderSize(file, 1<<20)
	for n := 0; ; n++ {
		if n%512 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line, err := readUsageLine(reader)
		if len(line) > 0 {
			consume(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	return finish(), nil
}

// dayOf is the local calendar day of an RFC 3339 timestamp, "" if unreadable.
func dayOf(stamp string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return ""
	}
	return t.In(loc).Format(dayLayout)
}

// ---- Claude Code ----

type claudeDailyRow struct {
	day    string
	tokens int64
}

type claudeDaily struct {
	loc  *time.Location
	byID map[string]claudeDailyRow
	anon int
}

func (c *claudeDaily) feed(line []byte) {
	if !strings.Contains(string(line), `"usage"`) {
		return
	}
	var row struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			ID    string `json:"id"`
			Usage *struct {
				Input  *float64 `json:"input_tokens"`
				Output *float64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "assistant" || row.Message.Usage == nil {
		return
	}
	day := dayOf(row.Timestamp, c.loc)
	if day == "" {
		return
	}
	// One response is logged once per content block with the same id and
	// usage; the last report of an id is the response's.
	id := row.Message.ID
	if id == "" {
		c.anon++
		id = "\x00anon" + string(rune(c.anon))
	}
	c.byID[id] = claudeDailyRow{day: day, tokens: num(row.Message.Usage.Input) + num(row.Message.Usage.Output)}
}

func (c *claudeDaily) finish() map[string]int64 {
	days := map[string]int64{}
	for _, r := range c.byID {
		days[r.day] += r.tokens
	}
	return days
}

// ---- Codex ----

type codexDaily struct {
	loc   *time.Location
	days  map[string]int64
	total int64 // the running session total last seen (input − cached + output)
}

func (c *codexDaily) feed(line []byte) {
	if !strings.Contains(string(line), "token_count") {
		return
	}
	var row struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
			Info *struct {
				Total *codexTokenUsage `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "event_msg" || row.Payload.Type != "token_count" ||
		row.Payload.Info == nil || row.Payload.Info.Total == nil {
		return
	}
	t := row.Payload.Info.Total
	total := max(num(t.Input)-num(t.Cached), 0) + num(t.Output)
	// Events repeat the same running total; only growth is new spend.
	if delta := total - c.total; delta > 0 {
		if day := dayOf(row.Timestamp, c.loc); day != "" {
			c.days[day] += delta
		}
	}
	if total > c.total {
		c.total = total
	}
}

func (c *codexDaily) finish() map[string]int64 { return c.days }

// SumDays totals a series.
func SumDays(days []DayTokens) int64 {
	var sum int64
	for _, d := range days {
		sum += d.Tokens
	}
	return sum
}

// Busiest returns the largest day of a series (0 for an empty one).
func Busiest(days []DayTokens) int64 {
	var best int64
	for _, d := range days {
		best = max(best, d.Tokens)
	}
	return best
}
