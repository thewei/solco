package remotehost

import (
	"testing"
	"time"
)

// Two stores over one directory are the desktop app and the resident service:
// a Revoke made in one must reach the other's callbacks and its token check.
func TestDeviceStoreRefreshFollowsAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenDeviceStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, token, err := a.AddWithKey("iPhone", "nodekey-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenDeviceStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var revokedID, revokedKey string
	b.OnRevoke(func(id string) { revokedID = id })
	b.OnRevokeKey(func(key string) { revokedKey = key })
	if _, ok := b.Authenticate(token); !ok {
		t.Fatal("the second store must know the device")
	}
	time.Sleep(10 * time.Millisecond) // distinct mtime
	if err := a.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Authenticate(token); ok {
		t.Fatal("a token revoked by the other process must stop working")
	}
	if revokedID != dev.ID || revokedKey != "nodekey-1" {
		t.Fatalf("callbacks = %q %q", revokedID, revokedKey)
	}
	if b.HasTailcatKey("nodekey-1") {
		t.Fatal("the revoked key must no longer be admitted")
	}
}
