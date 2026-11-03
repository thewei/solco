package services

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbePortHTTPShapes(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	serverError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer serverError.Close()
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer notFound.Close()

	cases := []struct {
		name string
		url  string
		want ProbeClass
		code int
	}{
		{"200 is alive http", ok.URL, ProbeHTTP, 200},
		{"500 is an app error", serverError.URL, ProbeHTTP, 500},
		{"404 still answers", notFound.URL, ProbeHTTP, 404},
	}
	for _, tc := range cases {
		port := portOfURL(t, tc.url)
		got := ProbePort(context.Background(), port)
		if got.Class != tc.want || got.Code != tc.code {
			t.Errorf("%s: got %v/%d, want %v/%d", tc.name, got.Class, got.Code, tc.want, tc.code)
		}
	}
}

func TestProbePortBareTCPAndDownAndHung(t *testing.T) {
	// A bare TCP service: accepts, answers its own protocol line — alive,
	// and definitely not an HTTP status.
	raw := make(chan struct{})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			close(raw)
			conn.Write([]byte("-ERR unknown command 'GET'\r\n"))
			conn.Close()
		}
	}()

	// A hung server: accepts but never answers — the read times out.
	hung, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hung.Close()
	go func() {
		for {
			conn, err := hung.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			<-raw
		}
	}()

	downPort := portOfURL(t, "http://127.0.0.1:1") // nothing listens on port 1

	cases := []struct {
		name string
		port uint16
		want ProbeClass
	}{
		{"bare tcp answers its own protocol", portOfListener(listener), ProbeAlive},
		{"nothing listens", downPort, ProbeDown},
		{"accepts but never answers", portOfListener(hung), ProbeHung},
	}
	for _, tc := range cases {
		got := ProbePort(context.Background(), tc.port)
		if got.Class != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got.Class, tc.want)
		}
	}
}

func TestParseHTTPStatus(t *testing.T) {
	cases := []struct {
		head string
		want int
		ok   bool
	}{
		{"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nbody", 200, true},
		{"HTTP/1.0 500 Internal Server Error\n", 500, true},
		{"HTTP/2 204\r\n", 204, true},
		{"-ERR unknown command\r\n", 0, false},
		{"", 0, false},
		{"HTTP/1.1 nonsense\r\n", 0, false},
	}
	for _, tc := range cases {
		code, ok := parseHTTPStatus(tc.head)
		if ok != tc.ok || code != tc.want {
			t.Errorf("parse %q = %d/%v, want %d/%v", tc.head, code, ok, tc.want, tc.ok)
		}
	}
}

// TestProbePortRespectsContext pins the deadline hand-off: a probe against
// a hung server gives up by its own read timeout, not forever.
func TestProbePortRespectsContext(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, _ := listener.Accept()
			if conn != nil {
				defer conn.Close()
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := ProbePort(ctx, portOfListener(listener))
	if got.Class != ProbeHung {
		t.Fatalf("class = %v, want hung", got.Class)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("probe took %v; the deadlines must cap it", elapsed)
	}
}

func portOfURL(t *testing.T, raw string) uint16 {
	t.Helper()
	reader := bufio.NewReader(strings.NewReader(raw))
	_ = reader
	if _, after, found := strings.Cut(raw, "://"); found {
		raw = after
	}
	host := raw
	if next, _, found := strings.Cut(raw, "/"); found {
		host = next
	}
	_, port, _ := strings.Cut(host, ":")
	var out uint16
	for _, r := range port {
		if r < '0' || r > '9' {
			break
		}
		out = out*10 + uint16(r-'0')
	}
	if out == 0 {
		t.Fatalf("no port in %q", raw)
	}
	return out
}

func portOfListener(listener net.Listener) uint16 {
	_, port, _ := strings.Cut(listener.Addr().String(), ":")
	var out uint16
	for _, r := range port {
		if r < '0' || r > '9' {
			break
		}
		out = out*10 + uint16(r-'0')
	}
	return out
}
