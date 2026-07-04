package main

import (
	"net/http"
	"testing"
)

func TestMatchHostOnly(t *testing.T) {
	config = Config{Rules: []Rule{{Host: "api.github.com", Inject: "gh"}}}
	if r := match("api.github.com"); r == nil || r.Inject != "gh" {
		t.Fatalf("expected match for api.github.com, got %v", r)
	}
	if r := match("evil.com"); r != nil {
		t.Fatalf("expected no match for evil.com, got %v", r)
	}
}

func TestHostMatchesWildcard(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"github.com", "github.com", true},
		{"github.com", "api.github.com", false},
		{"*.githubusercontent.com", "objects.githubusercontent.com", true},
		{"*.githubusercontent.com", "raw.githubusercontent.com", true},
		{"*.githubusercontent.com", "a.b.githubusercontent.com", true},
		{"*.githubusercontent.com", "githubusercontent.com", true},   // bare domain
		{"*.githubusercontent.com", "evilgithubusercontent.com", false}, // not a subdomain
		{"*.githubusercontent.com", "github.com", false},
	}
	for _, c := range cases {
		if got := hostMatches(c.pattern, c.host); got != c.want {
			t.Errorf("hostMatches(%q,%q)=%v want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestMatchWildcardRule(t *testing.T) {
	config = Config{Rules: []Rule{{Host: "*.githubusercontent.com", Inject: ""}}}
	if match("objects.githubusercontent.com") == nil {
		t.Error("wildcard rule should match subdomain")
	}
	if match("example.com") != nil {
		t.Error("wildcard rule should not match unrelated host")
	}
}

func TestDecide(t *testing.T) {
	config = Config{AllowAll: false, Rules: []Rule{{Host: "h", Inject: "s"}}}

	if ru, ok := decide("h"); !ok || ru == nil {
		t.Fatalf("configured host should be allowed with rule (ru=%v ok=%v)", ru, ok)
	}
	if ru, ok := decide("other"); ok || ru != nil {
		t.Fatalf("unconfigured host should be denied when allow_all=false")
	}

	config.AllowAll = true
	if ru, ok := decide("other"); !ok || ru != nil {
		t.Fatalf("unconfigured host should be allowed with no rule when allow_all=true")
	}
	// configured host is still gated+injected even under allow_all
	if ru, ok := decide("h"); !ok || ru == nil {
		t.Fatalf("configured host should keep its rule under allow_all")
	}
}

func TestHostHasRules(t *testing.T) {
	config = Config{Rules: []Rule{{Host: "h"}}}
	if !hostHasRules("h") {
		t.Error("h should have rules")
	}
	if hostHasRules("x") {
		t.Error("x should not have rules")
	}
}

func TestInjectBearer(t *testing.T) {
	config = Config{Secrets: map[string]Secret{"s": {Type: "bearer", EnvVar: "TOK"}}}
	t.Setenv("TOK", "abc")
	req, _ := http.NewRequest("GET", "https://x/", nil)
	inject(req, &Rule{Inject: "s"})
	if got := req.Header.Get("Authorization"); got != "Bearer abc" {
		t.Fatalf("bearer: got %q", got)
	}
}

func TestInjectBasicDefaultUser(t *testing.T) {
	config = Config{Secrets: map[string]Secret{"s": {Type: "basic", EnvVar: "TOK"}}}
	t.Setenv("TOK", "pw")
	req, _ := http.NewRequest("GET", "https://x/", nil)
	inject(req, &Rule{Inject: "s"})
	u, p, ok := req.BasicAuth()
	if !ok || u != "x-access-token" || p != "pw" {
		t.Fatalf("basic: u=%q p=%q ok=%v", u, p, ok)
	}
}

func TestInjectOverwritesPlaceholder(t *testing.T) {
	config = Config{Secrets: map[string]Secret{"s": {Type: "bearer", EnvVar: "TOK"}}}
	t.Setenv("TOK", "real")
	req, _ := http.NewRequest("GET", "https://x/", nil)
	req.Header.Set("Authorization", "Bearer placeholder")
	inject(req, &Rule{Inject: "s"})
	if got := req.Header.Get("Authorization"); got != "Bearer real" {
		t.Fatalf("expected placeholder overwritten, got %q", got)
	}
}

func TestInjectEmptyEnvLeavesHeaderUnset(t *testing.T) {
	config = Config{Secrets: map[string]Secret{"s": {Type: "bearer", EnvVar: "SANDBOX_PROXY_TEST_UNSET"}}}
	req, _ := http.NewRequest("GET", "https://x/", nil)
	inject(req, &Rule{Inject: "s"})
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("empty env should not inject, got %q", got)
	}
}

func TestInjectNoInjectField(t *testing.T) {
	config = Config{}
	req, _ := http.NewRequest("GET", "https://x/", nil)
	inject(req, &Rule{Inject: ""})
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("no inject field should be a no-op, got %q", got)
	}
}
