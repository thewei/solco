package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/thewei/solco/internal/hostd"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// probe asks the Host for its bootstrap with the paired token, accepting the self-signed certificate only if it
// is the pinned one (the phone's own rule). It returns the status and the fingerprint seen.
func probe(port int, token string) (int, string, map[string]any, error) {
	var seen string
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			sum := sha256.Sum256(raw[0])
			seen = hex.EncodeToString(sum[:])
			return nil
		},
	}}}
	req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(port)+"/api/v3/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, seen, body, nil
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

// SOLCO-R01 (real process): the resident service answers a paired phone while no desktop runs, stops listening
// while the desktop holds the port, serves again — with the same certificate — when the desktop leaves, and ends
// cleanly on SIGTERM.
func TestResidentProcessServesYieldsAndComesBack(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the service")
	}
	bin := filepath.Join(t.TempDir(), "solco-hostd")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	user := t.TempDir()
	port := freePort(t)
	s := settings.Default()
	s.Remote.Mode, s.Remote.Port = "loopback", port
	if err := settings.NewFileStore(user).Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	dir := hostd.RemoteDir(user)
	// The phone was paired by the desktop earlier: its token is in the shared device store.
	paired, err := remotehost.New(remotehost.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := paired.Devices().Add("Pixel")
	if err != nil {
		t.Fatal(err)
	}
	_, wantFP, err := remotehost.LoadOrCreateCert(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// The Host's bootstrap asks Herdr for its sessions and answers 502 without one, so the process
	// gets a Herdr that lists none: the test then holds on a machine (CI) with no Herdr installed.
	stub := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '{\"sessions\":[]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-data", user)
	cmd.Env = append(os.Environ(), "HERDR_BIN_PATH="+stub, "HERDR_CONFIG_PATH="+filepath.Join(user, "no-herdr.toml"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitFor(t, "resident service answers the paired phone", func() bool {
		code, fp, _, err := probe(port, token)
		return err == nil && code == 200 && fp == wantFP
	})
	if code, _, _, err := probe(port, "not-a-token"); err != nil || code != 401 {
		t.Fatalf("unknown token: %d %v", code, err)
	}
	_, _, boot, _ := probe(port, token)
	if caps, _ := boot["capabilities"].(map[string]any); caps["remoteDesktop"] == true || caps["automationsManage"] == true || caps["sessions"] == true || caps["gitWrite"] == true {
		t.Fatalf("the resident service claims window-bound capabilities: %v", caps)
	}

	lease, err := hostd.Claim(dir, 8*time.Second) // the desktop starts
	if err != nil {
		t.Fatalf("desktop claim: %v", err)
	}
	if _, _, _, err := probe(port, token); err == nil {
		t.Fatal("the resident service still listens while the desktop owns the port")
	}
	lease.Release() // the desktop quits
	waitFor(t, "resident service comes back with the same identity", func() bool {
		code, fp, _, err := probe(port, token)
		return err == nil && code == 200 && fp == wantFP
	})

	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("solco-hostd ignored SIGTERM")
	}
}
