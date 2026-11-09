package feedback

/**
 * [INPUT]: 数据目录里的 broker.json 与 token
 * [OUTPUT]: 对外提供 Client（Create/Result/Threads/Thread/Answer/End/Recall/Stream）、NewClient、ErrOffline
 * [POS]: broker 的唯一客户端（2026-11-09 决策）：Agent 侧 shim、Solco 桌面桥、Host v3 的 Feedback 缝都用它；
 *        端点按需发现、连不上就重读一次 broker.json（换主后端口会变），仍连不上返回 ErrOffline，调用方把「离线」当一等状态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrOffline means no broker answers right now.
var ErrOffline = errors.New("feedback broker offline")

// Client talks to whichever process owns the broker.
type Client struct {
	dir  string
	http *http.Client

	mu    sync.Mutex
	base  string
	token string
}

// NewClient returns a Client that finds the broker through dir.
func NewClient(dir string) *Client {
	return &Client{dir: dir, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) endpoint(refresh bool) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.base != "" {
		return c.base, c.token, nil
	}
	b, err := os.ReadFile(filepath.Join(c.dir, endpointName))
	if err != nil {
		c.base = ""
		return "", "", ErrOffline
	}
	var ep endpointFile
	if json.Unmarshal(b, &ep) != nil || ep.Addr == "" {
		return "", "", ErrOffline
	}
	tb, err := os.ReadFile(filepath.Join(c.dir, tokenName))
	if err != nil {
		return "", "", ErrOffline
	}
	c.base, c.token = "http://"+ep.Addr, strings.TrimSpace(string(tb))
	return c.base, c.token, nil
}

func isDial(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func (c *Client) request(ctx context.Context, method, path string, in any, stream bool) (*http.Response, error) {
	var payload []byte
	if in != nil {
		payload, _ = json.Marshal(in)
	}
	for attempt := 0; attempt < 2; attempt++ {
		base, token, err := c.endpoint(attempt > 0)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		cl := c.http
		if stream {
			cl = &http.Client{}
		}
		resp, err := cl.Do(req)
		if err != nil {
			if isDial(err) && attempt == 0 {
				continue
			}
			if isDial(err) {
				return nil, ErrOffline
			}
			return nil, err
		}
		return resp, nil
	}
	return nil, ErrOffline
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	resp, err := c.request(ctx, method, path, in, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		switch resp.StatusCode {
		case 404:
			return fmt.Errorf("%w: %s", ErrNotFound, e.Error)
		case 409:
			return fmt.Errorf("%w: %s", ErrFinal, e.Error)
		case 400:
			return fmt.Errorf("%w: %s", ErrInvalid, e.Error)
		}
		return fmt.Errorf("feedback: broker answered %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// Health pings the broker.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v1/health", nil, nil)
}

// Create registers an Ask and returns its id and thread id.
func (c *Client) Create(ctx context.Context, in CreateInput) (askID, threadID string, err error) {
	var out struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	err = c.do(ctx, http.MethodPost, "/v1/asks", createBody{ID: in.ID, CallerInfo: in.CallerInfo, Client: in.Client,
		ClientVersion: in.ClientVersion, Description: in.Description, ProjectRoot: in.ProjectRoot, TaskID: in.TaskID,
		ThreadID: in.ThreadID, Questions: in.Questions, TimeoutMs: in.TimeoutMs}, &out)
	return out.ID, out.ThreadID, err
}

// Result reads an Ask as its Agent sees it (and counts as the Agent's heartbeat).
func (c *Client) Result(ctx context.Context, id string) (AskResult, error) {
	var r AskResult
	err := c.do(ctx, http.MethodGet, "/v1/asks/"+url.PathEscape(id), nil, &r)
	return r, err
}

// Threads lists thread rows.
func (c *Client) Threads(ctx context.Context, waitingOnly bool, project string) ([]ThreadSummary, error) {
	q := url.Values{}
	if waitingOnly {
		q.Set("state", "waiting")
	}
	if project != "" {
		q.Set("project", project)
	}
	var out struct {
		Threads []ThreadSummary `json:"threads"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/inbox/threads?"+q.Encode(), nil, &out)
	return out.Threads, err
}

// Thread reads one thread with its Asks.
func (c *Client) Thread(ctx context.Context, id string) (Thread, error) {
	var t Thread
	err := c.do(ctx, http.MethodGet, "/v1/inbox/threads/"+url.PathEscape(id), nil, &t)
	return t, err
}

// Answer submits the user's answer.
func (c *Client) Answer(ctx context.Context, askID string, answers map[string]Answer, atts []Attachment) error {
	return c.do(ctx, http.MethodPost, "/v1/inbox/asks/"+url.PathEscape(askID)+"/answer", answerBody{Answers: answers, Attachments: atts}, nil)
}

// End closes a pending Ask without an answer.
func (c *Client) End(ctx context.Context, askID string) error {
	return c.do(ctx, http.MethodPost, "/v1/inbox/asks/"+url.PathEscape(askID)+"/end", map[string]string{}, nil)
}

// Recall looks up earlier answers.
func (c *Client) Recall(ctx context.Context, project, query string, limit int) ([]RecallItem, error) {
	q := url.Values{"project": {project}, "q": {query}}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	var out struct {
		Items []RecallItem `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/recall?"+q.Encode(), nil, &out)
	return out.Items, err
}

// Stream delivers Events to fn until ctx ends or the connection drops; it returns the reason. Callers loop with a
// pause and refetch the list after each reconnect.
func (c *Client) Stream(ctx context.Context, fn func(Event)) error {
	resp, err := c.request(ctx, http.MethodGet, "/v1/inbox/stream", nil, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("feedback: stream answered %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "data: "); ok {
			var e Event
			if json.Unmarshal([]byte(rest), &e) == nil {
				fn(e)
			}
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return io.EOF
}
