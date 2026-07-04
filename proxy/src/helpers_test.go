package main

import "testing"

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"api.github.com:443": "api.github.com",
		"api.github.com":     "api.github.com",
		"[::1]:443":          "::1",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q)=%q want %q", in, got, want)
		}
	}
}

func TestInjLabel(t *testing.T) {
	if injLabel(nil) != "" {
		t.Error("nil rule should give empty label")
	}
	if injLabel(&Rule{Inject: ""}) != "" {
		t.Error("empty inject should give empty label")
	}
	if got := injLabel(&Rule{Inject: "gh"}); got != " [+gh]" {
		t.Errorf("label = %q", got)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("SANDBOX_TEST_ENVOR", "val")
	if got := envOr("SANDBOX_TEST_ENVOR", "def"); got != "val" {
		t.Errorf("set env: got %q", got)
	}
	if got := envOr("SANDBOX_TEST_ENVOR_UNSET", "def"); got != "def" {
		t.Errorf("unset env should return default: got %q", got)
	}
}
