package main

import (
	"net/http"
	"testing"
)

func TestCopyHeadersStripsHopByHop(t *testing.T) {
	src := http.Header{}
	src.Set("X-Keep", "1")
	src.Set("Connection", "keep-alive")
	src.Set("Transfer-Encoding", "chunked")
	src.Set("Proxy-Authorization", "secret")
	src.Set("Upgrade", "h2c")
	src.Set("Keep-Alive", "timeout=5")

	dst := http.Header{}
	copyHeaders(dst, src)

	if dst.Get("X-Keep") != "1" {
		t.Error("X-Keep should be copied")
	}
	for _, h := range []string{"Connection", "Transfer-Encoding", "Proxy-Authorization", "Upgrade", "Keep-Alive"} {
		if dst.Get(h) != "" {
			t.Errorf("%s should be stripped, got %q", h, dst.Get(h))
		}
	}
}

func TestHostHeader(t *testing.T) {
	cases := []struct{ scheme, authority, want string }{
		{"https", "h:443", "h"},
		{"http", "h:80", "h"},
		{"https", "h:8443", "h:8443"},
		{"http", "h:8080", "h:8080"},
		{"https", "h", "h"}, // no port present
	}
	for _, c := range cases {
		if got := hostHeader(c.scheme, c.authority); got != c.want {
			t.Errorf("hostHeader(%q,%q)=%q want %q", c.scheme, c.authority, got, c.want)
		}
	}
}

func TestHeaderHasToken(t *testing.T) {
	if !headerHasToken("keep-alive, Upgrade", "upgrade") {
		t.Error("comma list should contain upgrade (case-insensitive)")
	}
	if !headerHasToken("Upgrade", "upgrade") {
		t.Error("single token should match")
	}
	if headerHasToken("keep-alive", "upgrade") {
		t.Error("should not match absent token")
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	r, _ := http.NewRequest("GET", "https://x/", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	if !isWebSocketUpgrade(r) {
		t.Error("should detect websocket upgrade")
	}

	r.Header.Set("Upgrade", "h2c")
	if isWebSocketUpgrade(r) {
		t.Error("h2c upgrade is not a websocket")
	}

	plain, _ := http.NewRequest("GET", "https://x/", nil)
	if isWebSocketUpgrade(plain) {
		t.Error("plain GET is not a websocket upgrade")
	}
}
