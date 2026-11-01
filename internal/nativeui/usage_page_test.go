package nativeui

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/settings"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 Usage 页/设置页/状态（usage_state.go、page_usage.go、usage_settings.go）、internal/usage.Service 的假 Fetcher、内存 usageSecrets、ui.Tester 无头渲染
 * [OUTPUT]: 对外提供 Usage 页回归：路由归属与标题栏、首次启用视图不读任何东西、卡片读数与 Used|Left、读取他人登录前必须同意、令牌只进 secrets 不进设置文件、借用登录副本的生命周期（撤回同意/手填令牌即删）、Settings 分组与 Sessions 侧栏入口
 * [POS]: 2026-10-30 Usage 决策的守卫测试；与 usage_*.go 同生共死；所有读取走假 Fetcher 与内存 secrets，绝不碰真实 Keychain/网络/子进程
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type memSecrets struct {
	mu   sync.Mutex
	data map[string]string
	gets atomic.Int32
}

func newMemSecrets() *memSecrets { return &memSecrets{data: map[string]string{}} }

func (m *memSecrets) Get(key string) (string, bool) {
	m.gets.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	return v, ok
}

func (m *memSecrets) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *memSecrets) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

type fakeUsageFetcher struct {
	id    usage.ID
	snap  usage.Snapshot
	err   error
	calls atomic.Int32
}

func (f *fakeUsageFetcher) ID() usage.ID { return f.id }

func (f *fakeUsageFetcher) Fetch(context.Context) (usage.Snapshot, error) {
	f.calls.Add(1)
	if f.err != nil {
		return usage.Snapshot{}, f.err
	}
	snap := f.snap
	snap.At = time.Now()
	return snap, nil
}

// usageTestShell is a shell whose Usage state reads fakes only.
func usageTestShell(t *testing.T, fakes ...*fakeUsageFetcher) (*Shell, *memSecrets) {
	t.Helper()
	s := NewShell()
	var fetchers []usage.Fetcher
	for _, f := range fakes {
		fetchers = append(fetchers, f)
	}
	secrets := newMemSecrets()
	s.usage = &usageState{
		svc:      usage.NewService(fetchers...),
		secrets:  secrets,
		tokens:   map[usage.ID]bool{},
		drafts:   map[usage.ID]*string{},
		firstRun: map[usage.ID]bool{usage.Codex: true},
		activity: nil,
	}
	return s, secrets
}

func codexSnapshot() usage.Snapshot {
	return usage.Snapshot{Plan: "Plus", Windows: []usage.Window{{
		Name: "5-hour", UsedPct: 62, Span: 5 * time.Hour, Resets: time.Now().Add(2*time.Hour + 14*time.Minute),
	}}}
}

func TestUsageRoutesBelongToSessions(t *testing.T) {
	if !isHistoryRoute(routeQuota) {
		t.Fatal("/quota must keep the Sessions view tab lit")
	}
	if persistableRoute(routeQuota) != routeQuota {
		t.Fatal("/quota should come back after a relaunch")
	}
	if persistableRoute(usageSettingsPath) != usageSettingsPath {
		t.Fatal("/settings/quota should come back after a relaunch")
	}
	s, _ := usageTestShell(t)
	s.router.Replace(routeQuota)
	spec, ok := s.routeHeader()
	if !ok || spec.title != "Quota" || spec.parent != "Sessions" || spec.actions == nil {
		t.Fatalf("header = %+v, want Sessions › Quota with an Actions menu", spec)
	}
	if got := settingsSectionFor(usageSettingsPath).label; got != "Quota" {
		t.Fatalf("settings section label = %q", got)
	}
	if got := s.currentRouteTitle(); got != "Quota" {
		t.Fatalf("window title = %q", got)
	}
}

func TestUsageFirstRunReadsNothing(t *testing.T) {
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	s, secrets := usageTestShell(t, codex)
	tester := ui.NewTester(s.usagePage, 1000, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"Turn on the agents you use", "Show Quota", "Claude Code", "Zhipu GLM", "Antigravity"} {
		if !tester.HasText(want) {
			t.Errorf("first run lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if codex.calls.Load() != 0 || secrets.gets.Load() != 0 {
		t.Fatalf("first run read something: fetches=%d keychain=%d", codex.calls.Load(), secrets.gets.Load())
	}
}

func TestUsageShowUsageCommitsTheChoiceAndReads(t *testing.T) {
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	s, _ := usageTestShell(t, codex)
	s.commitUsageFirstRun()
	if !s.settings.Usage.Configured || !s.settings.Usage.Enabled["codex"] {
		t.Fatalf("usage settings = %+v", s.settings.Usage)
	}
	if s.settings.Usage.Enabled["claude"] || s.settings.Usage.Enabled["antigravity"] {
		t.Fatal("an agent the user did not switch on must stay off")
	}
	if codex.calls.Load() != 1 {
		t.Fatalf("codex fetches = %d, want 1", codex.calls.Load())
	}
}

func TestUsageCardShowsReadingAndLeft(t *testing.T) {
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	s, _ := usageTestShell(t, codex)
	s.setUsageEnabled(usage.Codex, true)
	tester := ui.NewTester(s.usagePage, 1000, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"Codex", "Plus", "5-hour", "62% used", "TIGHTEST"} {
		if !tester.HasText(want) {
			t.Errorf("page lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if !containsPrefix(tester.Texts(), "Resets in ") {
		t.Errorf("no reset line; texts=%q", tester.Texts())
	}
	if !containsPrefix(tester.Texts(), "Live") {
		t.Errorf("no Live badge; texts=%q", tester.Texts())
	}
	s.setUsageShowLeft(true)
	settleFrames(tester, 2)
	if !tester.HasText("38% left") {
		t.Errorf("Left reading missing; texts=%q", tester.Texts())
	}
}

func TestUsageFailureKeepsLastValuesAndOffersAFix(t *testing.T) {
	cursor := &fakeUsageFetcher{id: usage.Cursor, snap: codexSnapshot()}
	s, _ := usageTestShell(t, cursor)
	var offset atomic.Int64
	s.usage.svc = usage.NewServiceClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }, cursor)
	s.usage.tokens[usage.Cursor] = true
	s.setUsageEnabled(usage.Cursor, true)
	cursor.err = usage.ErrUnauthorized
	offset.Store(int64(time.Minute)) // past the floor
	s.usage.svc.Refresh(context.Background(), []usage.ID{usage.Cursor}, true, nil)
	tester := ui.NewTester(s.usagePage, 1000, 800)
	settleFrames(tester, 3)
	if !tester.HasText("62% used") {
		t.Errorf("the last values vanished; texts=%q", tester.Texts())
	}
	if !tester.HasText("Update Token…") {
		t.Errorf("no way to fix an expired token; texts=%q", tester.Texts())
	}
}

func TestUsageAntigravityWithoutPermissionOffersToAllowTheSignIn(t *testing.T) {
	ag := &fakeUsageFetcher{id: usage.Antigravity, err: usage.ErrSetup}
	s, _ := usageTestShell(t, ag)
	s.setUsageEnabled(usage.Antigravity, true)
	tester := ui.NewTester(s.usagePage, 1000, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"Allow Sign-In…", "Open Antigravity"} {
		if !tester.HasText(want) {
			t.Errorf("page lacks %q; texts=%q", want, tester.Texts())
		}
	}
}

func TestUsageAntigravityConsentRecordsAndSwitchOffWithdrawsIt(t *testing.T) {
	s, _ := usageTestShell(t)
	s.applySettings(func(v *settings.Settings) error { v.Usage.Configured = true; return nil })
	s.requestUsageProvider(usage.Antigravity, true)
	if !s.usage.consentOpen || s.usageEnabledFor(usage.Antigravity) {
		t.Fatal("turning Antigravity on must ask before using its saved sign-in")
	}
	if _, msg := usageConsentText(usage.Antigravity); !strings.Contains(msg, "at your own risk") {
		t.Fatalf("consent must state the risk: %q", msg)
	}
	s.usageConsentAllowed()
	if !s.usageEnabledFor(usage.Antigravity) || !s.settings.Usage.SignInAllowed["antigravity"] {
		t.Fatalf("Allow must switch it on and record the yes: %+v", s.settings.Usage)
	}
	if !s.usage.agySignIn.Load() {
		t.Fatal("the fetch goroutine's mirror must follow")
	}
	s.setUsageEnabled(usage.Antigravity, false)
	s.setUsageProvider(usage.Antigravity, false)
	if s.settings.Usage.SignInAllowed["antigravity"] {
		t.Fatal("switching Antigravity off must withdraw the permission")
	}
}

func TestUsageCardStylesFollowTheWindows(t *testing.T) {
	cur := &fakeUsageFetcher{id: usage.Cursor, snap: usage.Snapshot{Plan: "Pro · 500 requests", Windows: []usage.Window{
		{Name: "Requests · monthly", UsedPct: 25, Used: 124, Max: 500, Unit: "requests", Amount: "124 of 500", Resets: time.Now().Add(72 * time.Hour)},
		{Name: "On-demand", UsedPct: 10, Amount: "$2 of $20", Minor: true},
	}, Notes: []string{"1 rate-limit reset available"}}}
	agy := &fakeUsageFetcher{id: usage.Antigravity, snap: usage.Snapshot{Plan: "Google AI Pro", Windows: []usage.Window{
		{Name: "5-hour", Group: "Claude and GPT models", UsedPct: 10, Span: 5 * time.Hour, Resets: time.Now().Add(2 * time.Hour)},
		{Name: "Weekly", Group: "Claude and GPT models", UsedPct: 25, Span: 7 * 24 * time.Hour, Resets: time.Now().Add(48 * time.Hour)},
	}}}
	s, _ := usageTestShell(t, cur, agy)
	s.applySettings(func(v *settings.Settings) error {
		v.Usage.Configured = true
		v.Usage.Enabled = map[string]bool{"cursor": true, "antigravity": true}
		return nil
	})
	s.refreshUsage(true)
	tester := ui.NewTester(s.usagePage, 1000, 900)
	settleFrames(tester, 3)
	for _, want := range []string{"124", "used of 500 requests", "On-demand", "1 rate-limit reset available", "Claude and GPT models", "5-hour", "Weekly"} {
		if !tester.HasText(want) {
			t.Errorf("page lacks %q; texts=%q", want, tester.Texts())
		}
	}
	s.setUsageShowLeft(true)
	tester = ui.NewTester(s.usagePage, 1000, 900)
	settleFrames(tester, 3)
	if !tester.HasText("376") || !tester.HasText("left of 500 requests") {
		t.Errorf("Left mode must lead with what remains; texts=%q", tester.Texts())
	}
}

func TestUsageReadingAnotherAppsSignInNeedsConsent(t *testing.T) {
	s, _ := usageTestShell(t)
	s.applySettings(func(v *settings.Settings) error { v.Usage.Configured = true; return nil })
	s.requestUsageProvider(usage.Claude, true)
	if !s.usage.consentOpen || s.usage.consentFor != usage.Claude {
		t.Fatal("turning Claude on must ask first")
	}
	if s.usageEnabledFor(usage.Claude) {
		t.Fatal("Claude switched on before the user allowed it")
	}
	s.usageConsentAllowed()
	if s.usage.consentOpen || !s.usageEnabledFor(usage.Claude) {
		t.Fatalf("Allow must switch it on; consent=%v enabled=%v", s.usage.consentOpen, s.usageEnabledFor(usage.Claude))
	}

	// Codex reads no sign-in; Cursor with a pasted token reads none either.
	s.requestUsageProvider(usage.Codex, true)
	s.usage.tokens[usage.Cursor] = true
	s.requestUsageProvider(usage.Cursor, true)
	if s.usage.consentOpen {
		t.Fatal("no consent is due when no other app's sign-in is read")
	}
}

func TestUsageTokensGoToSecretsNeverSettings(t *testing.T) {
	zhipu := &fakeUsageFetcher{id: usage.Zhipu, snap: codexSnapshot()}
	s, secrets := usageTestShell(t, zhipu)
	s.setUsageEnabled(usage.Zhipu, true)
	s.saveUsageToken(usage.Zhipu, "  sk-very-secret  ")
	if got := secrets.data[usageTokenKey(usage.Zhipu)]; got != "sk-very-secret" {
		t.Fatalf("stored token = %q", got)
	}
	if !s.usage.tokens[usage.Zhipu] {
		t.Fatal("the page does not know a token is saved")
	}
	blob, err := json.Marshal(s.settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "sk-very-secret") {
		t.Fatal("token leaked into the settings file")
	}
	s.removeUsageToken(usage.Zhipu)
	if _, ok := secrets.data[usageTokenKey(usage.Zhipu)]; ok || s.usage.tokens[usage.Zhipu] {
		t.Fatal("Remove must delete the stored token")
	}
}

// TestUsageSigninCopyLifecycle pins the borrowed sign-in copy: withdrawing
// the consent, or pasting or removing a token of one's own, drops the copy,
// so the other app's Keychain item is never read behind the consent's back.
func TestUsageSigninCopyLifecycle(t *testing.T) {
	s, secrets := usageTestShell(t)
	s.applySettings(func(v *settings.Settings) error { v.Usage.Configured = true; return nil })
	if err := secrets.Set(usageSigninKey(usage.Claude), "borrowed-claude-blob"); err != nil {
		t.Fatal(err)
	}
	s.requestUsageProvider(usage.Claude, false)
	if _, ok := secrets.data[usageSigninKey(usage.Claude)]; ok {
		t.Fatal("withdrawing the consent must drop the cached copy")
	}

	if err := secrets.Set(usageSigninKey(usage.Cursor), "borrowed-cursor-token"); err != nil {
		t.Fatal(err)
	}
	s.saveUsageToken(usage.Cursor, "  pasted-cursor-token  ")
	if _, ok := secrets.data[usageTokenKey(usage.Cursor)]; !ok {
		t.Fatal("the pasted token must be stored")
	}
	if _, ok := secrets.data[usageSigninKey(usage.Cursor)]; ok {
		t.Fatal("pasting a token of one's own must drop the cached copy")
	}
	s.removeUsageToken(usage.Cursor)
	if _, ok := secrets.data[usageTokenKey(usage.Cursor)]; ok {
		t.Fatal("Remove must delete the pasted token")
	}
	if _, ok := secrets.data[usageSigninKey(usage.Cursor)]; ok {
		t.Fatal("removing the pasted token must leave the copy dropped")
	}
}

func TestUsageSettingsListEveryProvider(t *testing.T) {
	s, _ := usageTestShell(t)
	tester := ui.NewTester(func(c *ui.Context) { settingsPage(c, func() { s.settingsUsage(c) }) }, 900, 1800)
	settleFrames(tester, 3)
	for _, want := range []string{"Claude Code", "Codex", "Cursor", "Zhipu GLM", "GitHub Copilot", "Antigravity", "API key", "Platform"} {
		if !tester.HasText(want) {
			t.Errorf("settings lack %q; texts=%q", want, tester.Texts())
		}
	}
}

func TestSessionsSidebarHasAUsageEntry(t *testing.T) {
	s := newMultiAgentHistoryShell(t)
	tester := ui.NewTester(s.historyNav, 260, 600)
	tester.Frame()
	if !tester.HasText("Quota") {
		t.Fatalf("Sessions sidebar lacks the Quota entry; texts=%q", tester.Texts())
	}
}

func TestUsageFormatting(t *testing.T) {
	for in, want := range map[time.Duration]string{
		30 * time.Second:             "<1m",
		45 * time.Minute:             "45m",
		2*time.Hour + 14*time.Minute: "2h 14m",
		3 * time.Hour:                "3h",
		76 * time.Hour:               "3d 4h",
	} {
		if got := usageIn(in); got != want {
			t.Errorf("usageIn(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{999: "999", 1500: "1.5K", 340_000: "340K", 1_240_000: "1.2M"} {
		if got := usageTokens(in); got != want {
			t.Errorf("usageTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func containsPrefix(texts []string, prefix string) bool {
	for _, text := range texts {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// The strip's TIGHTEST line and "Needs a look" read one verdict per window.
func TestUsageWindowAlertSharesTheVerdict(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	win := func(used float64, elapsed time.Duration) usage.Window {
		return usage.Window{UsedPct: used, Span: week, Resets: now.Add(week - elapsed)}
	}
	if tone, text := usageWindowAlert(win(40, week/2), now); tone != ToneNeutral || text != "" {
		t.Fatalf("on pace = %v %q, want neutral and silent", tone, text)
	}
	tone, text := usageWindowAlert(win(87, 5*24*time.Hour), now)
	if tone == ToneNeutral || !strings.HasPrefix(text, "runs out in ") || !strings.HasSuffix(text, " at this pace") {
		t.Fatalf("running out = %v %q", tone, text)
	}
	if tone, text := usageWindowAlert(win(100, week/2), now); tone != ToneError || text != "limit reached" {
		t.Fatalf("exceeded = %v %q", tone, text)
	}
}
