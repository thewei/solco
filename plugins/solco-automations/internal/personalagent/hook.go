package personalagent

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Webhook ingress core (docs/assistant-webhook-sleep-wake-design.md W1, Gaps
// A06/A09). An outside service can ask ONE Assistant to look at something. The
// request is authenticated by a per-Assistant, revocable key (HMAC over the
// method, path, timestamp, delivery id and body hash), bound to that
// Assistant's own task or goal, deduped by delivery id, rate limited, and
// written as a durable receipt plus an ordinary `hook.check` event.
//
// It never creates a task, a routine or a prompt: the event goes through the
// Assistant's existing heartbeat, where approval, wake budget, proactivity
// level and Quiet Hours decide whether a model wakes. Whatever the sender wrote
// is data: only a short, sanitised reason is kept for the owner to read and it
// never enters an event line, a prompt or a log.

const (
	// HookMaxBody bounds one delivery.
	HookMaxBody = 16 << 10
	// HookTolerance is how far a delivery's timestamp may be from now.
	HookTolerance = 5 * time.Minute
	// HookRatePerHour bounds the accepted deliveries per key per hour.
	HookRatePerHour = 60
	// MaxHookKeys bounds the active keys of one Assistant.
	MaxHookKeys = 3
	// HookEvent is the only event a delivery may carry.
	HookEvent = "check.requested"
)

var hookSourceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
var hookDeliveryRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{4,128}$`)

// HookPath is the one path a key's Assistant is reached on.
func HookPath(assistantID string) string { return "/api/v3/hooks/assistants/" + assistantID + "/wake" }

// HookKey is a key without its secret.
type HookKey struct {
	ID          string   `json:"id"`
	AssistantID string   `json:"assistantId"`
	Sources     []string `json:"sources"`
	Status      string   `json:"status"`
	CreatedAt   int64    `json:"createdAt"`
	RevokedAt   int64    `json:"revokedAt,omitempty"`
	LastUsedAt  int64    `json:"lastUsedAt,omitempty"`
}

// HookSign is what a sender computes: hex HMAC-SHA256 over
// METHOD \n path \n timestamp \n delivery \n hex(sha256(body)).
func HookSign(secret, method, path, timestamp, delivery string, body []byte) string {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%s\n%s\n%s\n%s\n%s", strings.ToUpper(method), path, timestamp, delivery, hex.EncodeToString(sum[:]))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// CreateHookKey makes a key. The secret is returned here once and is never
// listed again.
func (s *Store) CreateHookKey(requestID, assistantID string, sources []string) (HookKey, string, bool, error) {
	if len(sources) == 0 || len(sources) > 8 {
		return HookKey{}, "", false, fmt.Errorf("%w: name 1–8 sources", ErrInvalid)
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, src := range sources {
		src = strings.ToLower(strings.TrimSpace(src))
		if !hookSourceRe.MatchString(src) {
			return HookKey{}, "", false, fmt.Errorf("%w: source %q (lower-case letters, digits, . _ -)", ErrInvalid, src)
		}
		if !seen[src] {
			seen[src] = true
			clean = append(clean, src)
		}
	}
	type result struct {
		K      HookKey
		Secret string
	}
	r, replayed, err := execute(s, requestID, "hook.key.create", map[string]any{"a": assistantID, "s": clean}, func(tx *sql.Tx) (result, error) {
		a, err := getAssistantTx(tx, assistantID)
		if err != nil {
			return result{}, err
		}
		if a.Status != StatusActive {
			return result{}, ErrAssistantDisabled
		}
		var live int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM hook_keys WHERE assistant_id=? AND status='active'`, assistantID).Scan(&live)
		if live >= MaxHookKeys {
			return result{}, fmt.Errorf("%w: at most %d active keys per Assistant; revoke one first", ErrInvalid, MaxHookKeys)
		}
		id, secret, now := "hk_"+randomHex(8), randomHex(32), s.nowMS()
		raw, _ := json.Marshal(clean)
		if _, err := tx.Exec(`INSERT INTO hook_keys(id,assistant_id,secret,sources,status,created_at) VALUES(?,?,?,?,?,?)`, id, assistantID, secret, string(raw), "active", now); err != nil {
			return result{}, err
		}
		return result{HookKey{ID: id, AssistantID: assistantID, Sources: clean, Status: "active", CreatedAt: now}, secret}, nil
	})
	return r.K, r.Secret, replayed, err
}

// ListHookKeys lists an Assistant's keys, never their secrets.
func (s *Store) ListHookKeys(assistantID string) ([]HookKey, error) {
	rows, err := s.db.Query(`SELECT id,assistant_id,sources,status,created_at,revoked_at,last_used_at FROM hook_keys WHERE assistant_id=? ORDER BY created_at DESC, id`, assistantID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []HookKey{}
	for rows.Next() {
		var k HookKey
		var raw string
		if err := rows.Scan(&k.ID, &k.AssistantID, &raw, &k.Status, &k.CreatedAt, &k.RevokedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &k.Sources)
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeHookKey stops a key at once; deliveries already received stay.
func (s *Store) RevokeHookKey(requestID, keyID string) (HookKey, bool, error) {
	return execute(s, requestID, "hook.key.revoke", keyID, func(tx *sql.Tx) (HookKey, error) {
		res, err := tx.Exec(`UPDATE hook_keys SET status='revoked', revoked_at=CASE WHEN status='active' THEN ? ELSE revoked_at END WHERE id=?`, s.nowMS(), keyID)
		if err != nil {
			return HookKey{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return HookKey{}, ErrNotFound
		}
		var k HookKey
		var raw string
		err = tx.QueryRow(`SELECT id,assistant_id,sources,status,created_at,revoked_at,last_used_at FROM hook_keys WHERE id=?`, keyID).Scan(&k.ID, &k.AssistantID, &raw, &k.Status, &k.CreatedAt, &k.RevokedAt, &k.LastUsedAt)
		_ = json.Unmarshal([]byte(raw), &k.Sources)
		return k, err
	})
}

// HookDelivery is one request as the Host saw it.
type HookDelivery struct {
	KeyID     string `json:"keyId"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Timestamp string `json:"timestamp"`
	Delivery  string `json:"delivery"`
	Signature string `json:"signature"`
	Body      string `json:"body"`
}

// HookReceipt is the durable answer to a delivery. Accepted means recorded —
// never that anyone has looked at it.
type HookReceipt struct {
	EventID     string `json:"eventId"`
	AssistantID string `json:"assistantId"`
	Receipt     string `json:"receipt"` // accepted
	Routing     string `json:"routing"` // heartbeat | held | recorded
	Replayed    bool   `json:"replayed"`
}

type hookBody struct {
	Source   string `json:"source"`
	Event    string `json:"event"`
	Resource *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"resource"`
	Reason string `json:"reason"`
}

var reasonScrub = regexp.MustCompile(`[^\p{L}\p{N} .,:;!?'"()/_@#%+=\-]`)

func sanitizeReason(s string) string {
	s = reasonScrub.ReplaceAllString(s, " ")
	return clip(strings.Join(strings.Fields(s), " "), 200)
}

// IngestHook authenticates, scopes, dedupes and records one delivery. Every
// authentication failure is the same ErrHookRejected so a caller learns nothing
// about which key ids exist.
func (s *Store) IngestHook(d HookDelivery) (HookReceipt, error) {
	if len(d.Body) > HookMaxBody {
		return HookReceipt{}, fmt.Errorf("%w: body is larger than %d bytes", ErrInvalid, HookMaxBody)
	}
	var secret, assistantID, rawSources, status string
	err := s.db.QueryRow(`SELECT secret,assistant_id,sources,status FROM hook_keys WHERE id=?`, d.KeyID).Scan(&secret, &assistantID, &rawSources, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "active") {
		return HookReceipt{}, ErrHookRejected
	}
	if err != nil {
		return HookReceipt{}, wrapBusy(err)
	}
	ts, perr := strconv.ParseInt(d.Timestamp, 10, 64)
	now := s.now()
	if perr != nil || now.Sub(time.Unix(ts, 0)).Abs() > HookTolerance {
		return HookReceipt{}, ErrHookRejected
	}
	want := HookSign(secret, d.Method, d.Path, d.Timestamp, d.Delivery, []byte(d.Body))
	if !hmac.Equal([]byte(want), []byte(d.Signature)) || d.Path != HookPath(assistantID) || !strings.EqualFold(d.Method, "POST") {
		return HookReceipt{}, ErrHookRejected
	}
	// Authenticated from here on.
	if !hookDeliveryRe.MatchString(d.Delivery) || !utf8.ValidString(d.Body) {
		return HookReceipt{}, fmt.Errorf("%w: a delivery needs an id of 4–128 safe characters and a UTF-8 body", ErrInvalid)
	}
	var b hookBody
	dec := json.NewDecoder(strings.NewReader(d.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return HookReceipt{}, fmt.Errorf("%w: the body is not the documented JSON object", ErrInvalid)
	}
	var sources []string
	_ = json.Unmarshal([]byte(rawSources), &sources)
	b.Source = strings.ToLower(strings.TrimSpace(b.Source))
	okSource := false
	for _, src := range sources {
		okSource = okSource || src == b.Source
	}
	if !okSource {
		return HookReceipt{}, fmt.Errorf("%w: this key does not accept source %q", ErrInvalid, clip(b.Source, 32))
	}
	if b.Event != HookEvent {
		return HookReceipt{}, fmt.Errorf("%w: event must be %q", ErrInvalid, HookEvent)
	}
	resKind, resID := "", ""
	if b.Resource != nil {
		resKind, resID = b.Resource.Type, b.Resource.ID
		if resKind != "task" && resKind != "goal" {
			return HookReceipt{}, fmt.Errorf("%w: resource.type is task or goal", ErrInvalid)
		}
	}
	sum := sha256.Sum256([]byte(d.Body))
	payloadHash := hex.EncodeToString(sum[:])
	reason := sanitizeReason(b.Reason)

	rec, _, err := execute(s, "", "hook.ingest", nil, func(tx *sql.Tx) (HookReceipt, error) {
		a, err := getAssistantTx(tx, assistantID)
		if err != nil {
			return HookReceipt{}, err
		}
		if a.Status != StatusActive {
			return HookReceipt{}, ErrAssistantDisabled
		}
		var prevID, prevHash, routing string
		err = tx.QueryRow(`SELECT id,payload_hash,routing FROM hook_events WHERE key_id=? AND delivery_id=?`, d.KeyID, d.Delivery).Scan(&prevID, &prevHash, &routing)
		if err == nil {
			if prevHash != payloadHash {
				return HookReceipt{}, ErrRequestConflict
			}
			return HookReceipt{EventID: prevID, AssistantID: assistantID, Receipt: "accepted", Routing: routing, Replayed: true}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return HookReceipt{}, err
		}
		nowMS := s.nowMS()
		var recent int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM hook_events WHERE key_id=? AND received_at>?`, d.KeyID, nowMS-time.Hour.Milliseconds()).Scan(&recent)
		if recent >= HookRatePerHour {
			return HookReceipt{}, ErrRateLimited
		}
		line := "Webhook " + b.Source + ": check requested"
		taskID := ""
		switch resKind {
		case "task":
			t, err := getTaskTx(tx, resID)
			if err != nil || t.AssistantID != assistantID {
				return HookReceipt{}, fmt.Errorf("%w: that task is not this Assistant's", ErrInvalid)
			}
			taskID, line = t.ID, line+" for task "+clip(t.Title, 80)
		case "goal":
			g, err := getGoalTx(tx, resID)
			if err != nil || g.AssistantID != assistantID {
				return HookReceipt{}, fmt.Errorf("%w: that goal is not this Assistant's", ErrInvalid)
			}
			line += " for goal " + clip(g.Title, 80)
		}
		routing = "recorded"
		if h, err := getHeartbeatTx(tx, assistantID); err == nil && h.Revision > 0 {
			switch h.Events["hook.check"] {
			case "wake":
				routing = "heartbeat"
			case "hold":
				routing = "held"
			}
		}
		id := newID("h")
		if _, err := tx.Exec(`INSERT INTO hook_events(id,assistant_id,key_id,source,delivery_id,payload_hash,resource_kind,resource_id,reason,routing,received_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, assistantID, d.KeyID, b.Source, d.Delivery, payloadHash, resKind, resID, reason, routing, nowMS); err != nil {
			return HookReceipt{}, err
		}
		if _, err := emitTx(tx, Event{AssistantID: assistantID, Source: "hook", Kind: "hook.check", Resource: id, Revision: "1", TaskID: taskID, Detail: line}, nowMS); err != nil {
			return HookReceipt{}, err
		}
		if _, err := tx.Exec(`UPDATE hook_keys SET last_used_at=? WHERE id=?`, nowMS, d.KeyID); err != nil {
			return HookReceipt{}, err
		}
		return HookReceipt{EventID: id, AssistantID: assistantID, Receipt: "accepted", Routing: routing}, nil
	})
	return rec, err
}
