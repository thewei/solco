package feedback

/**
 * [INPUT]: Store、访问令牌、Notifier
 * [OUTPUT]: 对外提供 Broker、NewHandler（loopback HTTP：/v1/asks* 给 Agent 侧 shim，/v1/inbox/* 给 Solco 与 Host，/v1/recall，/v1/health）
 * [POS]: 反馈 broker 的 HTTP 面（2026-11-09 决策）：每个请求都要 Bearer 令牌、Host 必须是 loopback、带 Origin 头一律拒绝（浏览器页面读不到，
 *        旧 Bun daemon 的 `Access-Control-Allow-Origin: *` 加无鉴权已废）；读 /v1/asks/{id} 同时算 Agent 心跳；SSE 订阅者数量决定 broker 要不要自己弹本地通知
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Broker serves a Store over HTTP.
type Broker struct {
	Store    *Store
	Token    string
	Notifier Notifier
	Version  string

	streams atomic.Int32
}

// UIAttached reports whether a Solco window is subscribed to the event stream.
func (b *Broker) UIAttached() bool { return b.streams.Load() > 0 }

// NewHandler returns the broker's http.Handler.
func NewHandler(b *Broker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": b.Version})
	})
	mux.HandleFunc("POST /v1/asks", b.handleCreate)
	mux.HandleFunc("GET /v1/asks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		b.Store.Heartbeat(id)
		writeJSON(w, 200, b.Store.Result(id))
	})
	mux.HandleFunc("GET /v1/recall", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		writeJSON(w, 200, map[string]any{"items": b.Store.Recall(q.Get("project"), q.Get("q"), limit)})
	})
	mux.HandleFunc("GET /v1/inbox/threads", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		writeJSON(w, 200, map[string]any{"threads": nonNil(b.Store.Threads(Filter{Waiting: q.Get("state") == "waiting", Project: q.Get("project"), Limit: limit}))})
	})
	mux.HandleFunc("GET /v1/inbox/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, ok := b.Store.Thread(r.PathValue("id"))
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "thread not found"})
			return
		}
		writeJSON(w, 200, t)
	})
	mux.HandleFunc("POST /v1/inbox/asks/{id}/answer", b.handleAnswer)
	mux.HandleFunc("GET /v1/inbox/pick", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		items := Pick(r.Context(), b.Store.ProjectRootOf(q.Get("thread")), q.Get("kind"), q.Get("q"))
		if items == nil {
			items = []PickItem{}
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /v1/inbox/asks/{id}/end", func(w http.ResponseWriter, r *http.Request) {
		replyErr(w, b.Store.End(r.PathValue("id"), ""))
	})
	mux.HandleFunc("POST /v1/inbox/asks/{id}/abandon", func(w http.ResponseWriter, r *http.Request) {
		replyErr(w, b.Store.Abandon(r.PathValue("id")))
	})
	mux.HandleFunc("POST /v1/inbox/threads/delete", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || len(in.IDs) == 0 {
			writeJSON(w, 400, map[string]string{"error": "ids required"})
			return
		}
		writeJSON(w, 200, map[string]int{"deleted": b.Store.DeleteThreads(in.IDs)})
	})
	mux.HandleFunc("POST /v1/inbox/threads/purge", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			OlderThanDays int  `json:"olderThanDays"`
			DryRun        bool `json:"dryRun"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.OlderThanDays < 1 || in.OlderThanDays > 3650 {
			writeJSON(w, 400, map[string]string{"error": "olderThanDays must be 1..3650"})
			return
		}
		n := b.Store.Purge(time.Duration(in.OlderThanDays)*24*time.Hour, in.DryRun)
		writeJSON(w, 200, map[string]int{"matched": n})
	})
	mux.HandleFunc("GET /v1/inbox/search", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ids": b.Store.Search(r.URL.Query().Get("q"))})
	})
	mux.HandleFunc("GET /v1/inbox/retention", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]int{"days": b.Store.RetentionDays()})
	})
	mux.HandleFunc("PUT /v1/inbox/retention", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Days int `json:"days"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil {
			writeJSON(w, 400, map[string]string{"error": "days required"})
			return
		}
		replyErr(w, b.Store.SetRetentionDays(in.Days))
	})
	mux.HandleFunc("GET /v1/inbox/stream", b.handleStream)
	return b.guard(mux)
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func replyErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]bool{"ok": true})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, 404, map[string]string{"error": "ask not found"})
	case errors.Is(err, ErrFinal):
		writeJSON(w, 409, map[string]string{"error": "ask is already final"})
	case errors.Is(err, ErrInvalid):
		writeJSON(w, 400, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, 500, map[string]string{"error": "internal error"})
	}
}

// guard enforces the loopback-only, token-only, no-browser rules.
func (b *Broker) guard(next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(b.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			writeJSON(w, 403, map[string]string{"error": "browser origins are not allowed"})
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			writeJSON(w, 403, map[string]string{"error": "loopback only"})
			return
		}
		got := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		if b.Token == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type createBody struct {
	ID            string       `json:"id"`
	CallerInfo    string       `json:"callerInfo"`
	Client        string       `json:"client"`
	ClientVersion string       `json:"clientVersion"`
	Description   string       `json:"description"`
	Task          string       `json:"task,omitempty"`
	ProjectRoot   string       `json:"projectRoot"`
	TaskID        string       `json:"taskId"`
	ThreadID      string       `json:"threadId"`
	Questions     []Question   `json:"questions"`
	TimeoutMs     int64        `json:"timeoutMs"`
	Suggest       []ContextRef `json:"suggest,omitempty"`
}

func (b *Broker) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body createBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	ask, thread, err := b.Store.Create(CreateInput{ID: body.ID, CallerInfo: body.CallerInfo, Client: body.Client,
		ClientVersion: body.ClientVersion, Description: body.Description, Task: body.Task, ProjectRoot: body.ProjectRoot, TaskID: body.TaskID,
		ThreadID: body.ThreadID, Questions: body.Questions, TimeoutMs: body.TimeoutMs,
		Suggest: FilterSuggest(body.ProjectRoot, body.Suggest)})
	if err != nil {
		replyErr(w, err)
		return
	}
	if b.Notifier != nil {
		n := Notice{Thread: thread, Ask: ask, UIAttached: b.UIAttached()}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			b.Notifier.Notify(ctx, n)
		}()
	}
	writeJSON(w, 200, map[string]any{"id": ask.ID, "threadId": ask.ThreadID})
}

type answerBody struct {
	Answers     map[string]Answer `json:"answers"`
	Attachments []Attachment      `json:"attachments"`
	Context     []ContextRef      `json:"context"`
}

func (b *Broker) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var body answerBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	id := r.PathValue("id")
	var refs []ContextRef
	if len(body.Context) > 0 {
		var err error
		if refs, err = ResolveContext(b.Store.ProjectRootOfAsk(id), body.Context); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad_context"})
			return
		}
	}
	replyErr(w, b.Store.Resolve(id, body.Answers, body.Attachments, refs))
}

func (b *Broker) handleStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, ": ready\n\n")
	fl.Flush()
	b.streams.Add(1)
	defer b.streams.Add(-1)
	ch, cancel := b.Store.Subscribe()
	defer cancel()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
			fl.Flush()
		case <-tick.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
