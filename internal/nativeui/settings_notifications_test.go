package nativeui

/**
 * [INPUT]: Settings → Notifications page, memory secrets, MyGo Tester
 * [OUTPUT]: 渠道的添加/开关/移除、URL 与令牌只进凭据存储而不进 settings、非法 URL 被拒、宽限选择持久化
 * [POS]: 通知渠道设置页的验收，无网络、无钥匙串
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/notifyhook"
)

func TestNotificationsPageAddsATogglesAndRemovesAChannel(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.commitSecrets = commitMapSecrets{}
	s.router.Replace("/settings/notifications")
	tester := ui.NewTester(s.View, 1200, 900)
	for _, title := range []string{"Webhook channels", "No channels", "Add a channel", "Add channel", "Phone grace", "Send test"} {
		if !tester.HasText(title) {
			t.Fatalf("Notifications page missing %q: %q", title, tester.Texts())
		}
	}

	s.notifyUI.name, s.notifyUI.url = "Feishu", "http://example.com/hook"
	if err := tester.Click("Add channel"); err != nil {
		t.Fatal(err)
	}
	if len(s.settings.Notifications.Channels) != 0 {
		t.Fatal("a plain-http URL was accepted")
	}

	s.notifyUI.url, s.notifyUI.token = "https://worker.example.com/v1/notify?k=secret-part", "tok"
	if err := tester.Click("Add channel"); err != nil {
		t.Fatal(err)
	}
	chs := s.settings.Notifications.Channels
	if len(chs) != 1 || chs[0].Name != "Feishu" || !chs[0].Enabled {
		t.Fatalf("channels = %+v", chs)
	}
	raw, _ := json.Marshal(s.settings)
	if strings.Contains(string(raw), "secret-part") || strings.Contains(string(raw), "tok\"") {
		t.Fatal("the URL or token reached the settings")
	}
	stored, ok := s.commitStore().Get(notifyhook.SecretKey(chs[0].ID))
	if !ok {
		t.Fatal("the secret was not stored")
	}
	if u, token, ok := notifyhook.DecodeSecret(stored); !ok || !strings.Contains(u, "secret-part") || token != "tok" {
		t.Fatalf("stored = %q", stored)
	}
	if s.notifyUI.url != "" || s.notifyUI.token != "" {
		t.Fatal("the drafts were not cleared")
	}
	for _, text := range tester.Texts() {
		if strings.Contains(text, "secret-part") {
			t.Fatalf("the page shows the URL: %q", text)
		}
	}

	if err := tester.Click("Remove Feishu"); err != nil {
		t.Fatal(err)
	}
	if len(s.settings.Notifications.Channels) != 0 {
		t.Fatalf("channel not removed: %+v", s.settings.Notifications.Channels)
	}
	if _, ok := s.commitStore().Get(notifyhook.SecretKey(chs[0].ID)); ok {
		t.Fatal("the secret outlived its channel")
	}
}

func TestNotificationGraceLabelsRoundTrip(t *testing.T) {
	for _, ch := range notifyGraceChoices {
		if notifyGraceLabel(ch.seconds) != ch.label {
			t.Fatalf("%d -> %q", ch.seconds, notifyGraceLabel(ch.seconds))
		}
	}
	if notifyGraceLabel(37) != "30 seconds" {
		t.Fatal("an odd stored value must show the default")
	}
}
