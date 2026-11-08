// [INPUT]: a paired device's opted-in Expo token and committed Assistant Activity
// [OUTPUT]: offline OS push with durable ticket, receipt and device-notice ACK
// [POS]: Remote Host transport. No second Agent, no user-content egress.
// [PROTOCOL]: Never send names, prompts, facts or evidence in push payloads/logs.
package remotehost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

const (
	pushFile          = "assistant-push.json"
	pushSendURL       = "https://exp.host/--/api/v2/push/send"
	pushReceiptsURL   = "https://exp.host/--/api/v2/push/getReceipts"
	pushInterval      = 30 * time.Second
	receiptDelay      = 15 * time.Minute
	receiptExpiry     = 24 * time.Hour
	retryDelay        = 2 * time.Minute
	uncertainRetryGap = 2 * time.Hour
	pushMaxDevices    = 12
	pushMaxAssistants = 24
)

var expoTokenRe = regexp.MustCompile("^(Expo|Exponent)PushToken\\[[A-Za-z0-9_-]{16,160}\\]$")

type pushPending struct {
	Seq       int64     `json:"seq"`
	Ticket    string    `json:"ticket"`
	SentAt    time.Time `json:"sentAt"`
	LastCheck time.Time `json:"lastCheck,omitempty"`
}
type pushDevice struct {
	Token      string                 `json:"token"`
	Enrollment string                 `json:"enrollment,omitempty"`
	Pending    map[string]pushPending `json:"pending,omitempty"`
	RetryAfter time.Time              `json:"retryAfter,omitempty"`
}
type pushDisk struct {
	Devices map[string]pushDevice `json:"devices"`
}
type pushRegistry struct {
	mu      sync.Mutex
	path    string
	devices map[string]pushDevice
}

func openPushRegistry(dir string) (*pushRegistry, error) {
	p := &pushRegistry{path: filepath.Join(dir, pushFile), devices: map[string]pushDevice{}}
	raw, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var d pushDisk
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("invalid push registry: %w", err)
	}
	for id, v := range d.Devices {
		if !strings.HasPrefix(id, "dev_") || !expoTokenRe.MatchString(v.Token) {
			return nil, errors.New("invalid push registry device")
		}
		if v.Pending == nil {
			v.Pending = map[string]pushPending{}
		}
		p.devices[id] = v
	}
	return p, nil
}
func clonePush(m map[string]pushDevice) map[string]pushDevice {
	n := make(map[string]pushDevice, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}
func (p *pushRegistry) write(n map[string]pushDevice) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(pushDisk{Devices: n})
	if err != nil {
		return err
	}
	return writeFileAtomic(p.path, b, 0600)
}

var errPushDisabled = errors.New("system push is disabled for this device")

func (p *pushRegistry) register(id, token string) error { return p.set(id, token, false) }

// A token-rotation callback cannot silently re-enable delivery after the
// owner tapped Disable. The enrolled row must still exist *under the lock*.
func (p *pushRegistry) refresh(id, token string) error { return p.set(id, token, true) }

func (p *pushRegistry) set(id, token string, requireExisting bool) error {
	if !expoTokenRe.MatchString(token) {
		return errors.New("invalid Expo token")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if requireExisting && p.devices[id].Token == "" {
		return errPushDisabled
	}
	if len(p.devices) >= pushMaxDevices && p.devices[id].Token == "" {
		return errors.New("push device limit")
	}
	n := clonePush(p.devices)
	if n[id].Token != token || n[id].Enrollment == "" {
		// Opt-out followed by a fresh register (even for the same Expo
		// token) is a new grant. Old in-flight sends may not write state
		// into this enrollment or drop its refreshed token.
		var generation [16]byte
		if _, err := rand.Read(generation[:]); err != nil {
			return err
		}
		n[id] = pushDevice{Token: token, Enrollment: hex.EncodeToString(generation[:]), Pending: map[string]pushPending{}}
	}
	if err := p.write(n); err != nil {
		return err
	}
	p.devices = n
	return nil
}
func (p *pushRegistry) remove(id string) error {
	return p.removeOwned(id, "")
}

// Provider errors from a previously opted-in Token must not revoke a
// newly re-enrolled phone with the same Token after an explicit opt-out.
func (p *pushRegistry) removeOwned(id, enrollment string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	current, ok := p.devices[id]
	if !ok || (enrollment != "" && current.Enrollment != enrollment) {
		return nil
	}
	n := clonePush(p.devices)
	delete(n, id)
	if err := p.write(n); err != nil {
		return err
	}
	p.devices = n
	return nil
}
func (p *pushRegistry) snapshot() map[string]pushDevice {
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePush(p.devices)
}
func (p *pushRegistry) current(id string) pushDevice {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.devices[id]
}
func (p *pushRegistry) update(id, token, enrollment string, fn func(*pushDevice)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.devices[id]
	if !ok || d.Token != token || d.Enrollment != enrollment {
		return nil
	}
	d.Pending = make(map[string]pushPending, len(d.Pending))
	for a, v := range p.devices[id].Pending {
		d.Pending[a] = v
	}
	fn(&d)
	n := clonePush(p.devices)
	n[id] = d
	if err := p.write(n); err != nil {
		return err
	}
	p.devices = n
	return nil
}

type expoStatus struct {
	Status  string `json:"status"`
	ID      string `json:"id"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}
type expoTickets struct {
	Data   json.RawMessage   `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}
type expoReceiptList struct {
	Data   map[string]expoStatus `json:"data"`
	Errors []json.RawMessage     `json:"errors"`
}
type pushBridge struct {
	registry                         *pushRegistry
	client                           *http.Client
	sendURL, receiptURL, accessToken string
}

func (p *pushBridge) post(ctx context.Context, endpoint string, payload, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("push service HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out)
}
func (p *pushBridge) send(ctx context.Context, token, assistantID, hostID string, attention bool) (expoStatus, error) {
	title, body := "Solco · Assistant activity", "New Assistant updates"
	if attention {
		title, body = "Solco · Assistant needs you", "New updates need review"
	}
	// All data sent to Expo is generic. ID is opaque and only used locally
	// after unlocking the device and reconnecting to the authenticated Mac.
	// Keep the route in the existing app's notification payload format.
	// No Mac token, goal, task or transcript content crosses Expo.
	route, _ := json.Marshal(map[string]any{
		"pathname": "/assistants/activity/[id]",
		"params":   map[string]string{"id": assistantID},
	})
	data := map[string]string{"route": string(route)}
	// The first 16 hex digits of the Mac's public TLS fingerprint are the
	// existing bootstrap hostId. An inactive Mac may notify the phone; on
	// tap the phone must switch to the exact paired Mac before opening its
	// Assistant. Never send the bearer/pairing token to Expo.
	if len(hostID) == 16 {
		data["hostId"] = hostID
	}
	message := map[string]any{
		"to": token, "title": title, "body": body, "priority": "high",
		"channelId": "agent-attention", "ttl": 3600,
		"collapseId": "solco-" + assistantID, "tag": "solco-" + assistantID,
		"data": data,
	}
	return p.sendPayload(ctx, message)
}

// A user-initiated test sends one privacy-safe probe, without reading or
// changing Activity state. A ticket only means Expo accepted the request.
func (p *pushBridge) test(ctx context.Context, token string) (expoStatus, error) {
	return p.sendPayload(ctx, map[string]any{
		"to": token, "title": "Solco · System push test",
		"body":     "Background notifications are being tested",
		"priority": "high", "channelId": "agent-attention", "ttl": 600,
	})
}

func (p *pushBridge) sendPayload(ctx context.Context, message map[string]any) (expoStatus, error) {
	var result expoTickets
	if err := p.post(ctx, p.sendURL, message, &result); err != nil {
		return expoStatus{}, err
	}
	if len(result.Errors) > 0 {
		return expoStatus{}, errors.New("push service refused batch")
	}
	var t expoStatus
	if err := json.Unmarshal(result.Data, &t); err != nil {
		return expoStatus{}, err
	}
	if t.Status != "ok" && t.Status != "error" {
		return expoStatus{}, errors.New("invalid push ticket")
	}
	if t.Status == "ok" && (t.ID == "" || len(t.ID) > 160) {
		return expoStatus{}, errors.New("missing push receipt ID")
	}
	return t, nil
}
func (p *pushBridge) receipt(ctx context.Context, id string) (expoStatus, bool, error) {
	var r expoReceiptList
	if err := p.post(ctx, p.receiptURL, map[string]any{"ids": []string{id}}, &r); err != nil {
		return expoStatus{}, false, err
	}
	if len(r.Errors) > 0 {
		return expoStatus{}, false, errors.New("receipt query failed")
	}
	v, ok := r.Data[id]
	if ok && v.Status != "ok" && v.Status != "error" {
		return expoStatus{}, false, errors.New("invalid receipt status")
	}
	return v, ok, nil
}
func (s *Server) pushTick(ctx context.Context) {
	if s.push == nil || s.opts.PersonalAgent == nil {
		return
	}
	devices := s.push.registry.snapshot()
	if len(devices) == 0 {
		return
	}
	c, ok := s.opts.PersonalAgent(ctx)
	if !ok || c == nil {
		return
	}
	overview, err := c.Overview(ctx)
	if err != nil {
		return
	}
	now := s.opts.Now()
	s.mu.Lock()
	hostID := s.fingerprint
	s.mu.Unlock()
	if len(hostID) > 16 {
		hostID = hostID[:16]
	}
	registered := map[string]bool{}
	for _, d := range s.devices.List() {
		registered[d.ID] = true
	}
	for deviceID, d := range devices {
		if ctx.Err() != nil {
			return
		}
		if !registered[deviceID] {
			_ = s.push.registry.remove(deviceID)
			continue
		}
		if s.DeviceOnline(deviceID) || now.Before(d.RetryAfter) {
			continue
		}
		for i, a := range overview.Assistants {
			if i >= pushMaxAssistants || ctx.Err() != nil {
				break
			}
			if pending, ok := d.Pending[a.ID]; ok {
				s.pushReceipt(ctx, deviceID, d.Token, d.Enrollment, a.ID, pending, c, now)
				continue
			}
			pro, e := c.Proactivity(ctx, a.ID)
			if e != nil || pro.QuietNow {
				continue
			}
			page, e := c.UnreadActivity(ctx, a.ID, paActivityNoticeConsumer(Device{ID: deviceID}), 50)
			if e != nil || len(page.Facts) == 0 || page.NextSeq <= page.AckSeq {
				continue
			}
			attention := false
			for _, fact := range page.Facts {
				switch fact.Kind {
				case "blocked", "held", "ready", "routine_failed":
					attention = true
				}
			}
			ticket, e := s.push.send(ctx, d.Token, a.ID, hostID, attention)
			if e != nil {
				_ = s.push.registry.update(deviceID, d.Token, d.Enrollment, func(x *pushDevice) { x.RetryAfter = now.Add(retryDelay) })
				break
			}
			if ticket.Status == "error" {
				if ticket.Details.Error == "DeviceNotRegistered" {
					_ = s.push.registry.removeOwned(deviceID, d.Enrollment)
					break
				}
				_ = s.push.registry.update(deviceID, d.Token, d.Enrollment, func(x *pushDevice) { x.RetryAfter = now.Add(retryDelay) })
				break
			}
			// Persist Expo acceptance. No notification cursor ACK until a valid
			// provider receipt. Crash in between may duplicate, never lose a fact.
			_ = s.push.registry.update(deviceID, d.Token, d.Enrollment, func(x *pushDevice) {
				x.Pending[a.ID] = pushPending{Seq: page.NextSeq, Ticket: ticket.ID, SentAt: now}
			})
		}
	}
}
func (s *Server) pushReceipt(ctx context.Context, deviceID, token, enrollment, assistantID string, pending pushPending, c *personalagent.Client, now time.Time) {
	if now.Sub(pending.SentAt) < receiptDelay {
		return
	}
	if !pending.LastCheck.IsZero() && now.Sub(pending.LastCheck) < receiptDelay {
		return
	}
	status, found, err := s.push.receipt(ctx, pending.Ticket)
	if err != nil || !found {
		_ = s.push.registry.update(deviceID, token, enrollment, func(x *pushDevice) {
			if now.Sub(pending.SentAt) >= receiptExpiry {
				// Expo no longer promises a receipt for this ticket.
				// Do not ACK Activity. Permit a bounded at-least-once
				// replay after a long cool-down instead of silencing this
				// Assistant's notifications forever.
				delete(x.Pending, assistantID)
				x.RetryAfter = now.Add(uncertainRetryGap)
			} else {
				pending.LastCheck = now
				x.Pending[assistantID] = pending
			}
		})
		return
	}
	if status.Status == "error" {
		if status.Details.Error == "DeviceNotRegistered" {
			_ = s.push.registry.removeOwned(deviceID, enrollment)
			return
		}
		// Explicit provider failure is safe to retry after backoff.
		_ = s.push.registry.update(deviceID, token, enrollment, func(x *pushDevice) {
			delete(x.Pending, assistantID)
			x.RetryAfter = now.Add(retryDelay)
		})
		return
	}
	// FCM/APNs accepted is NOT device-display proof. This only advances the
	// notification cursor, never the person's Activity "Mark seen" cursor.
	requestID := fmt.Sprintf("push-%s-%s-%d", deviceID, assistantID, pending.Seq)
	seq, err := c.AckActivity(ctx, requestID, assistantID, paActivityNoticeConsumer(Device{ID: deviceID}), pending.Seq)
	if err != nil || seq < pending.Seq {
		return
	}
	_ = s.push.registry.update(deviceID, token, enrollment, func(x *pushDevice) { delete(x.Pending, assistantID) })
}
func (s *Server) startPush() {
	if s.push == nil || s.opts.PersonalAgent == nil {
		return
	}
	s.mu.Lock()
	if s.pushCancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.pushCancel = cancel
	s.pushDone = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(pushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			turn, stop := context.WithTimeout(ctx, 20*time.Second)
			s.pushTick(turn)
			stop()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Server) stopPush() {
	s.mu.Lock()
	cancel, done := s.pushCancel, s.pushDone
	s.pushCancel = nil
	s.pushDone = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Explicit test push is ScopeManage and bound to the paired device token.
// It does NOT acknowledge Activity, even when Expo returns a ticket.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request, device Device) {
	if s.push == nil || s.opts.PersonalAgent == nil {
		writeError(w, 404, "system_push_unavailable", "")
		return
	}
	d := s.push.registry.current(device.ID)
	if d.Token == "" {
		writeError(w, 409, "push_disabled", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	ticket, err := s.push.test(ctx, d.Token)
	if err != nil || ticket.Status != "ok" {
		writeError(w, 502, "system_push_failed", "")
		return
	}
	writeJSON(w, 200, map[string]any{"schema": 1, "queued": true})
}

func (s *Server) handlePushRegistration(w http.ResponseWriter, r *http.Request, device Device) {
	if s.push == nil || s.opts.PersonalAgent == nil {
		writeError(w, 404, "system_push_unavailable", "")
		return
	}
	switch r.Method {
	case "GET":
		d := s.push.registry.current(device.ID)
		writeJSON(w, 200, map[string]any{"schema": 1, "enabled": d.Token != "", "provider": "expo"})
	case "POST":
		if strings.HasSuffix(r.URL.Path, "/disable") {
			// Never allow a requester to name a different paired device.
			// The transport-authenticated Device is the only row we mutate.
			if err := s.push.registry.remove(device.ID); err != nil {
				writeError(w, 503, "push_registration_unavailable", "")
				return
			}
			writeJSON(w, 200, map[string]any{"schema": 1, "enabled": false, "provider": "expo"})
			return
		}
		refresh := strings.HasSuffix(r.URL.Path, "/refresh")
		if !refresh && !strings.HasSuffix(r.URL.Path, "/register") {
			writeError(w, 404, "not_found", "")
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeError(w, 400, "bad_request", "")
			return
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			writeError(w, 400, "bad_request", "")
			return
		}
		if !expoTokenRe.MatchString(in.Token) {
			writeError(w, 400, "bad_request", "")
			return
		}
		var err error
		if refresh {
			err = s.push.registry.refresh(device.ID, in.Token)
		} else {
			err = s.push.registry.register(device.ID, in.Token)
		}
		if errors.Is(err, errPushDisabled) {
			writeError(w, 409, "push_disabled", "")
			return
		}
		if err != nil {
			writeError(w, 503, "push_registration_unavailable", "")
			return
		}
		writeJSON(w, 200, map[string]any{"schema": 1, "enabled": true, "provider": "expo"})
	default:
		writeError(w, 405, "method_not_allowed", "")
	}
}
