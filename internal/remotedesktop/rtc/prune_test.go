package rtc

import (
	"net"
	"testing"
)

func TestPruneICEServersKeepsOneOfEachKind(t *testing.T) {
	in := []ICEServer{
		{URLs: []string{"stun:stun.cloudflare.com:3478", "stun:stun.cloudflare.com:443"}},
		{URLs: []string{"stun:stun.miwifi.com:3478"}},
		{URLs: []string{"stun:stun.example.org:3478"}},
		{URLs: []string{
			"turn:t.example:3478?transport=udp", "turn:t.example:3478?transport=tcp", "turns:t.example:5349?transport=tcp",
			"turn:t.example:443?transport=udp", "turn:t.example:80?transport=tcp",
		}, Username: "u", Credential: "c"},
	}
	var urls []string
	for _, s := range pruneICEServers(in) {
		urls = append(urls, s.URLs...)
		if len(s.URLs) > 0 && s.URLs[0][:4] == "turn" && (s.Username != "u" || s.Credential != "c") {
			t.Fatal("TURN credential lost")
		}
	}
	want := []string{"stun:stun.cloudflare.com:3478", "stun:stun.miwifi.com:3478", "turn:t.example:3478?transport=udp", "turn:t.example:3478?transport=tcp"}
	if len(urls) != len(want) {
		t.Fatalf("got %v, want %v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("got %v, want %v", urls, want)
		}
	}
}

func TestIPFilterTakesOneGlobalIPv6(t *testing.T) {
	f := ipFilter()
	for _, c := range []struct {
		ip   string
		want bool
	}{
		{"192.168.5.67", true}, {"100.88.0.1", true}, {"169.254.1.1", false}, {"fe80::1", false},
		{"fd7a:115c:a1e0::2358:d911", true}, {"fd12:3456::9", false},
		{"2408:8256:4c86:2de3::1", true}, {"2408:8256:4c86:2de3::2", false},
	} {
		if got := f(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("%s: got %v want %v", c.ip, got, c.want)
		}
	}
}
