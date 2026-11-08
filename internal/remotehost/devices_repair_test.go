package remotehost

import "testing"

// A phone that pairs again carries the same tunnel key: the old card is replaced (one phone, one row), its token
// stops working, its sockets close, and the tunnel peer is not cut because the new card owns the key.
func TestPairingAgainWithTheSameKeyReplacesTheCard(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenDeviceStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var closed []string
	var keyCut []string
	a.OnRevoke(func(id string) { closed = append(closed, id) })
	a.OnRevokeKey(func(k string) { keyCut = append(keyCut, k) })
	first, firstToken, err := a.AddWithKey("V2309A", "nodekey-1")
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := a.AddWithKey("Other", "nodekey-2")
	second, secondToken, err := a.AddWithKey("V2309A", "nodekey-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(a.List()); got != 2 {
		t.Fatalf("devices = %d, want 2 (the replaced card must be gone)", got)
	}
	if _, ok := a.Authenticate(firstToken); ok {
		t.Fatal("the replaced card's token must stop working")
	}
	if _, ok := a.Authenticate(secondToken); !ok {
		t.Fatal("the new card must authenticate")
	}
	if len(closed) != 1 || closed[0] != first.ID {
		t.Fatalf("closed = %v, want [%s]", closed, first.ID)
	}
	if len(keyCut) != 0 {
		t.Fatalf("the tunnel peer must stay: %v", keyCut)
	}
	if !a.HasTailcatKey("nodekey-1") || !a.HasTailcatKey("nodekey-2") || second.ID == first.ID || other.ID == "" {
		t.Fatal("keys must stay bound")
	}
}

// A legacy pairing with no key never replaces anything.
func TestPairingWithoutKeyStacks(t *testing.T) {
	a, err := OpenDeviceStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = a.AddWithKey("Phone", "")
	_, _, _ = a.AddWithKey("Phone", "")
	if got := len(a.List()); got != 2 {
		t.Fatalf("devices = %d, want 2", got)
	}
}
