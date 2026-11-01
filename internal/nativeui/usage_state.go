package nativeui

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/settings"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: internal/usage 的 Service（各家额度读取、缓存、节流）、history.DailyUsageReader（近 30 天本地 token）、mygo.Secrets（Keychain）与 settings.Usage 开关
 * [OUTPUT]: Shell.usage（usageState）、refreshUsage（页面打开/手动刷新时才读，无后台定时器）、setUsageEnabled/requestUsageProvider（逐家开启 + 读取他人登录前的同意）、saveUsageToken/removeUsageToken、usageSigninKey/forgetUsageSignin（借用登录在同意读取一次后的副本，撤回同意或改用手填令牌即删）、loadUsageActivity、usageSecrets 接口、cachedSecrets/appSecrets（进程内每个 Keychain 条目最多读一次，命中/未命中/被拒都记住，写删才失效）
 * [POS]: Usage 页与设置页共用的展示状态与动作；只做调度与偏好持久化：额度语义在 internal/usage，token 与副本只进 Keychain（key 为 usage.<id>.token / usage.<id>.signin），永不进 settings 文件或日志；副本让 Claude/Cursor 的读取不再逐次触碰对方应用的 Keychain 条目
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// usageSecrets is the credential store the Usage page keeps its tokens in.
// Production is the system Keychain; tests inject memory.
type usageSecrets interface {
	Get(key string) (string, bool)
	Set(key, value string) error
	Delete(key string) error
}

type keychainSecrets struct{}

func (keychainSecrets) Get(key string) (string, bool) {
	value, err := mygo.Secrets.Get(key)
	if err != nil {
		return "", false
	}
	return string(value), true
}

func (keychainSecrets) Set(key, value string) error { return mygo.Secrets.Set(key, []byte(value)) }
func (keychainSecrets) Delete(key string) error     { return mygo.Secrets.Delete(key) }

// cachedSecrets reads each Keychain item at most once per process: a hit, a
// miss and a refused read all stay answered until the item is written or
// deleted through it, so macOS is asked once instead of at every Quota open.
type cachedSecrets struct {
	inner usageSecrets
	mu    sync.Mutex
	seen  map[string]cachedSecret
}

type cachedSecret struct {
	value string
	ok    bool
}

func newCachedSecrets(inner usageSecrets) *cachedSecrets {
	return &cachedSecrets{inner: inner, seen: map[string]cachedSecret{}}
}

func (c *cachedSecrets) Get(key string) (string, bool) {
	c.mu.Lock()
	if e, hit := c.seen[key]; hit {
		c.mu.Unlock()
		return e.value, e.ok
	}
	c.mu.Unlock()
	value, ok := c.inner.Get(key)
	c.mu.Lock()
	if e, hit := c.seen[key]; hit { // written while this read was out
		c.mu.Unlock()
		return e.value, e.ok
	}
	c.seen[key] = cachedSecret{value, ok}
	c.mu.Unlock()
	return value, ok
}

func (c *cachedSecrets) Set(key, value string) error {
	if err := c.inner.Set(key, value); err != nil {
		return err
	}
	c.mu.Lock()
	c.seen[key] = cachedSecret{value, true}
	c.mu.Unlock()
	return nil
}

func (c *cachedSecrets) Delete(key string) error {
	if err := c.inner.Delete(key); err != nil {
		return err
	}
	c.mu.Lock()
	c.seen[key] = cachedSecret{}
	c.mu.Unlock()
	return nil
}

// appSecrets is the one Keychain-backed store of the app's own credentials
// (commit API key, Quota tokens and sign-in copies).
var appSecrets usageSecrets = newCachedSecrets(keychainSecrets{})

// usageTokenKey is the Keychain key of a provider's pasted token.
func usageTokenKey(id usage.ID) string { return "usage." + string(id) + ".token" }

// usageSigninKey is the Keychain key of a provider's cached copy of another
// app's sign-in, written once after the consented read.
func usageSigninKey(id usage.ID) string { return "usage." + string(id) + ".signin" }

// usageActivityDays is the window of the activity strip.
const usageActivityDays = 30

type usageState struct {
	svc     *usage.Service
	daily   *history.DailyUsageReader
	secrets usageSecrets
	// site mirrors Settings → Zhipu platform for the fetch goroutine, which
	// must not read the UI-lane settings.
	site atomic.Pointer[string]
	// agySignIn mirrors Usage.SignInAllowed[antigravity] for the same reason.
	agySignIn atomic.Bool

	// tokens says which providers have a token in the Keychain; it is read
	// off the UI lane (a Keychain read may prompt) the first time it is needed.
	tokens       map[usage.ID]bool
	tokensLoaded bool

	activity     map[usage.ID][]history.DayTokens
	activityBusy bool
	activityAt   time.Time

	// pageOpen is true while /quota is on screen: entering it reads once.
	pageOpen bool

	// drafts are the token fields; firstRun the pending switches of the
	// first-run view.
	drafts   map[usage.ID]*string
	firstRun map[usage.ID]bool

	// consent is the open "may Solco read this sign-in?" dialog.
	consentOpen bool
	consentFor  usage.ID
}

// newUsageState wires the real providers. Nothing runs until the page asks.
func newUsageState() *usageState {
	st := &usageState{
		secrets:  appSecrets,
		tokens:   map[usage.ID]bool{},
		drafts:   map[usage.ID]*string{},
		firstRun: map[usage.ID]bool{usage.Codex: true},
		activity: map[usage.ID][]history.DayTokens{},
	}
	token := func(id usage.ID) func() string {
		return func() string {
			value, _ := st.secrets.Get(usageTokenKey(id))
			return value
		}
	}
	site := func() string {
		if v := st.site.Load(); v != nil {
			return *v
		}
		return usage.ZhipuGlobal
	}
	st.svc = usage.NewService(
		usage.NewClaudeCached(st.secrets, usageSigninKey(usage.Claude)),
		usage.NewCodex(),
		usage.NewCursorCached(st.secrets, usageSigninKey(usage.Cursor), token(usage.Cursor)),
		usage.NewZhipu(token(usage.Zhipu), site),
		usage.NewCopilot(token(usage.Copilot)),
		usage.NewAntigravity(st.agySignIn.Load),
	)
	if home, err := os.UserHomeDir(); err == nil {
		st.daily = history.NewDailyUsageReader(history.DefaultRoots(home))
	}
	return st
}

// usageEnabled lists the providers the user switched on, in page order.
func (s *Shell) usageEnabled() []usage.ID {
	var ids []usage.ID
	for _, id := range usage.All {
		if s.settings.Usage.Enabled[string(id)] {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Shell) usageEnabledFor(id usage.ID) bool { return s.settings.Usage.Enabled[string(id)] }

// usageShowLeft reports the Used | Left reading.
func (s *Shell) usageShowLeft() bool { return s.settings.Usage.ShowLeft }

func (s *Shell) setUsageShowLeft(left bool) {
	s.applySettings(func(v *settings.Settings) error {
		v.Usage.ShowLeft = left
		return nil
	})
}

// refreshUsage reads the enabled providers. force is the manual Refresh; the
// page opening is the other trigger — there is no timer.
func (s *Shell) refreshUsage(force bool) { s.refreshUsageIDs(s.usageEnabled(), force) }

// refreshUsageIDs reads the given providers.
func (s *Shell) refreshUsageIDs(ids []usage.ID, force bool) {
	site := s.settings.Usage.ZhipuSite
	s.usage.site.Store(&site)
	s.usage.agySignIn.Store(s.settings.Usage.SignInAllowed[string(usage.Antigravity)])
	if len(ids) == 0 {
		return // nothing switched on: nothing is read, not even the Keychain
	}
	s.loadUsageTokens()
	s.loadUsageActivity(force)
	svc := s.usage.svc
	run := func() {
		svc.Refresh(context.Background(), ids, force, func() {
			s.applyOnWindow(func() { s.invalidateQuickPanel() })
		})
	}
	if s.win == nil {
		run() // headless: deterministic
		return
	}
	go run()
}

// usageActivityAgents maps the providers that keep local session logs.
var usageActivityAgents = map[usage.ID]history.AgentID{
	usage.Claude: history.AgentClaudeCode,
	usage.Codex:  history.AgentCodex,
}

// loadUsageActivity reads the last 30 days of local token activity off the
// UI lane. It never records anything: the strip is recomputed from the
// Provider session files (cached in memory per file).
func (s *Shell) loadUsageActivity(force bool) {
	st := s.usage
	if st.daily == nil || st.activityBusy {
		return
	}
	if !force && time.Since(st.activityAt) < 30*time.Second {
		return
	}
	st.activityBusy = true
	reader := st.daily
	work := func() {
		since := time.Now().AddDate(0, 0, -(usageActivityDays - 1))
		days, err := reader.Read(context.Background(), since, time.Local)
		s.applyOnWindow(func() {
			st.activityBusy = false
			st.activityAt = time.Now()
			if err != nil {
				slog.Warn("usage activity unavailable", "error", err)
				return
			}
			for id, agent := range usageActivityAgents {
				st.activity[id] = days[agent]
			}
		})
	}
	if s.win == nil {
		work()
		return
	}
	go work()
}

// loadUsageTokens learns which providers have a pasted token, once. A
// Keychain read can prompt, so it never runs on the UI lane.
func (s *Shell) loadUsageTokens() {
	st := s.usage
	if st.tokensLoaded {
		return
	}
	st.tokensLoaded = true
	secrets := st.secrets
	work := func() {
		found := map[usage.ID]bool{}
		for _, id := range []usage.ID{usage.Cursor, usage.Zhipu, usage.Copilot} {
			if _, ok := secrets.Get(usageTokenKey(id)); ok {
				found[id] = true
			}
		}
		s.applyOnWindow(func() {
			for id, ok := range found {
				st.tokens[id] = ok
			}
		})
	}
	if s.win == nil {
		work()
		return
	}
	go work()
}

// saveUsageToken stores a pasted token and reads with it.
func (s *Shell) saveUsageToken(id usage.ID, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	st := s.usage
	secrets := st.secrets
	slog.Info("usage token save requested", "provider", string(id))
	work := func() {
		err := secrets.Set(usageTokenKey(id), value)
		s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("usage token save failed", "provider", string(id), "error", err)
				s.pendingToast = "Couldn't save to the Keychain."
				return
			}
			st.tokens[id] = true
			delete(st.drafts, id)
			st.svc.Clear(id)
			s.forgetUsageSignin(id)
			s.pendingToast = "Saved in the Keychain."
			if s.usageEnabledFor(id) {
				s.refreshUsage(true)
			}
		})
	}
	if s.win == nil {
		work()
		return
	}
	go work()
}

// removeUsageToken deletes a pasted token.
func (s *Shell) removeUsageToken(id usage.ID) {
	st := s.usage
	secrets := st.secrets
	slog.Info("usage token remove requested", "provider", string(id))
	work := func() {
		err := secrets.Delete(usageTokenKey(id))
		s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("usage token remove failed", "provider", string(id), "error", err)
				s.pendingToast = "Couldn't remove the token."
				return
			}
			delete(st.tokens, id)
			st.svc.Clear(id)
			s.forgetUsageSignin(id)
			s.pendingToast = "Token removed."
		})
	}
	if s.win == nil {
		work()
		return
	}
	go work()
}

// forgetUsageSignin drops the cached copy of another app's sign-in: the
// consent was withdrawn, or the user pasted a token of their own.
func (s *Shell) forgetUsageSignin(id usage.ID) {
	switch id {
	case usage.Claude, usage.Cursor:
	default:
		return
	}
	st := s.usage
	secrets := st.secrets
	work := func() {
		if err := secrets.Delete(usageSigninKey(id)); err != nil {
			slog.Warn("usage sign-in copy remove failed", "provider", string(id), "error", err)
		}
	}
	if s.win == nil {
		work()
		return
	}
	go work()
}

// openUsageApp starts the app a provider reads from (Antigravity), then reads
// once it has had time to open its local server. The user pressed the button.
func (s *Shell) openUsageApp(id usage.ID) {
	if id != usage.Antigravity || s.win == nil {
		return
	}
	go func() {
		if err := exec.Command("/usr/bin/open", "-a", "Antigravity").Run(); err != nil {
			slog.Warn("usage open app failed", "provider", string(id), "error", err)
			return
		}
		time.Sleep(8 * time.Second)
		s.applyOnWindow(func() { s.refreshUsageIDs([]usage.ID{id}, true) })
	}()
}

// usageNeedsConsent reports a provider whose sign-in belongs to another app:
// Solco reads it only after the user said so (product decision 2026-10-30).
func usageNeedsConsent(id usage.ID) bool {
	switch id {
	case usage.Claude, usage.Cursor, usage.Copilot, usage.Antigravity:
		return true
	}
	return false
}

// requestUsageProvider is a switch flipped for a provider: turning one on
// that reads another app's sign-in asks first (unless the user pasted a token
// of their own), everything else applies at once.
func (s *Shell) requestUsageProvider(id usage.ID, on bool) {
	if on && usageNeedsConsent(id) && !s.usage.tokens[id] {
		s.usage.consentOpen, s.usage.consentFor = true, id
		return
	}
	s.setUsageProvider(id, on)
}

// setUsageProvider applies a switch: the pending first-run choice before the
// first run was committed, the saved preference after.
func (s *Shell) setUsageProvider(id usage.ID, on bool) {
	if !on {
		s.setUsageSignIn(id, false)
	}
	if !s.settings.Usage.Configured {
		s.usage.firstRun[id] = on
		return
	}
	s.setUsageEnabled(id, on)
}

// setUsageEnabled saves one provider's opt-in. Turning one off forgets its
// reading.
func (s *Shell) setUsageEnabled(id usage.ID, on bool) {
	s.applySettings(func(v *settings.Settings) error {
		if v.Usage.Enabled == nil {
			v.Usage.Enabled = map[string]bool{}
		}
		v.Usage.Enabled[string(id)] = on
		v.Usage.Configured = true
		return nil
	})
	if on {
		s.refreshUsage(true)
		return
	}
	s.usage.svc.Clear(id)
}

// setUsageSignIn records (or withdraws) the user's yes for reading another
// app's saved sign-in.
func (s *Shell) setUsageSignIn(id usage.ID, allowed bool) {
	if !allowed {
		s.forgetUsageSignin(id)
	}
	if s.settings.Usage.SignInAllowed[string(id)] == allowed {
		return
	}
	s.applySettings(func(v *settings.Settings) error {
		if allowed {
			if v.Usage.SignInAllowed == nil {
				v.Usage.SignInAllowed = map[string]bool{}
			}
			v.Usage.SignInAllowed[string(id)] = true
		} else {
			delete(v.Usage.SignInAllowed, string(id))
		}
		return nil
	})
}

// commitUsageFirstRun is the first-run "Show Quota": the chosen switches
// become the preference.
func (s *Shell) commitUsageFirstRun() {
	chosen := map[string]bool{}
	for id, on := range s.usage.firstRun {
		if on {
			chosen[string(id)] = true
		}
	}
	s.applySettings(func(v *settings.Settings) error {
		v.Usage.Configured = true
		v.Usage.Enabled = chosen
		return nil
	})
	s.refreshUsage(true)
}

// setUsageZhipuSite saves the Zhipu platform and re-reads.
func (s *Shell) setUsageZhipuSite(site string) {
	s.applySettings(func(v *settings.Settings) error {
		v.Usage.ZhipuSite = site
		return nil
	})
	s.usage.svc.Clear(usage.Zhipu)
	if s.usageEnabledFor(usage.Zhipu) {
		s.refreshUsage(true)
	}
}

// usageConsentAllowed is the dialog's Allow.
func (s *Shell) usageConsentAllowed() {
	id := s.usage.consentFor
	s.usage.consentOpen = false
	s.setUsageSignIn(id, true)
	s.setUsageProvider(id, true)
}
