package main

import (
	"crypto/tls"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// hermeticUpstream replaces the shared upstream transport with one that dials
// directly (no env proxy) and skips upstream cert verification, so tests reach a
// local httptest server. Restored on cleanup.
func hermeticUpstream(t *testing.T) {
	t.Helper()
	old := upstream
	upstream = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { upstream = old })
}

func proxyClient(proxyURL string, mitm bool) *http.Client {
	u, _ := url.Parse(proxyURL)
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	if mitm {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // trust the MITM leaf
	}
	return &http.Client{Transport: tr}
}

func hostOf(rawURL, scheme string) string {
	return strings.TrimPrefix(rawURL, scheme+"://")
}

// --- plain HTTP forward-proxy path ---

func TestForwardHTTPInjects(t *testing.T) {
	hermeticUpstream(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Auth", r.Header.Get("Authorization"))
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	uhost := hostOf(up.URL, "http")

	config = Config{
		Secrets: map[string]Secret{"s": {Type: "bearer", EnvVar: "TOK"}},
		Rules:   []Rule{{Host: hostOnly(uhost), Inject: "s"}},
	}
	t.Setenv("TOK", "secret123")

	proxy := httptest.NewServer(http.HandlerFunc(handle))
	defer proxy.Close()

	resp, err := proxyClient(proxy.URL, false).Get("http://" + uhost + "/path")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Echo-Auth"); got != "Bearer secret123" {
		t.Errorf("upstream received auth %q, want injected token", got)
	}
}

func TestForwardHTTPDefaultDeny(t *testing.T) {
	hermeticUpstream(t)
	config = Config{AllowAll: false} // no rules

	proxy := httptest.NewServer(http.HandlerFunc(handle))
	defer proxy.Close()

	resp, err := proxyClient(proxy.URL, false).Get("http://denied.example/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}

func TestForwardHTTPAllowAllNoInject(t *testing.T) {
	hermeticUpstream(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Auth", r.Header.Get("Authorization"))
	}))
	defer up.Close()
	uhost := hostOf(up.URL, "http")

	config = Config{AllowAll: true} // no rules -> allowed, not injected

	proxy := httptest.NewServer(http.HandlerFunc(handle))
	defer proxy.Close()

	resp, err := proxyClient(proxy.URL, false).Get("http://" + uhost + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Echo-Auth"); got != "" {
		t.Errorf("allow_all non-rule host must not be injected, got %q", got)
	}
}

// --- CONNECT / MITM path ---

func TestConnectMITMInjectsAndRelays(t *testing.T) {
	if err := loadOrCreateCA(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	leafCert = map[string]*tls.Certificate{} // isolate leaf cache
	hermeticUpstream(t)

	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Auth", r.Header.Get("Authorization"))
		io.WriteString(w, "secured-ok")
	}))
	defer up.Close()
	uhost := hostOf(up.URL, "https")

	config = Config{
		Secrets: map[string]Secret{"s": {Type: "basic", EnvVar: "TOK", Username: "x-access-token"}},
		Rules:   []Rule{{Host: hostOnly(uhost), Inject: "s"}},
	}
	t.Setenv("TOK", "mitm-secret")

	proxy := httptest.NewServer(http.HandlerFunc(handle))
	defer proxy.Close()

	resp, err := proxyClient(proxy.URL, true).Get("https://" + uhost + "/x")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "secured-ok" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:mitm-secret"))
	if got := resp.Header.Get("X-Echo-Auth"); got != want {
		t.Errorf("upstream auth=%q want %q", got, want)
	}
}

func TestConnectMITMDefaultDeny(t *testing.T) {
	if err := loadOrCreateCA(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	hermeticUpstream(t)
	config = Config{AllowAll: false} // no rules -> CONNECT refused

	proxy := httptest.NewServer(http.HandlerFunc(handle))
	defer proxy.Close()

	_, err := proxyClient(proxy.URL, true).Get("https://blocked.example:443/")
	if err == nil {
		t.Fatal("expected CONNECT to be refused for an unconfigured host")
	}
}
