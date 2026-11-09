package feedback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/settings"
)

func TestAWebhookChannelRetiresTheOldWorkerFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Feedback")
	_ = os.MkdirAll(dir, 0o700)
	if channelsConfigured(dir) {
		t.Fatal("a channel appeared out of nowhere")
	}
	cur := settings.Default()
	cur.Notifications.Channels = []settings.NotificationChannel{{ID: "a", Enabled: false}}
	if err := settings.NewFileStore(root).Save(context.Background(), cur); err != nil {
		t.Fatal(err)
	}
	if channelsConfigured(dir) {
		t.Fatal("a switched-off channel counted")
	}
	cur.Notifications.Channels[0].Enabled = true
	if err := settings.NewFileStore(root).Save(context.Background(), cur); err != nil {
		t.Fatal(err)
	}
	if !channelsConfigured(dir) {
		t.Fatal("an enabled channel did not replace the Worker file")
	}
}
