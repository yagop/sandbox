package main

import (
	"encoding/json"
	"os"
)

// Rule is a single allow-list entry, matched by host (exact). If Inject names a
// secret, that secret's Authorization header is added to the upstream request.
type Rule struct {
	Host   string `json:"host"`   // exact host, e.g. "api.github.com"
	Inject string `json:"inject"` // name of a secret to inject; empty = allow, no injection
}

// Secret describes how to build an Authorization header. The actual value is
// read from the proxy's environment (EnvVar) so tokens live only on the host.
type Secret struct {
	Type     string `json:"type"`     // "bearer" or "basic"
	EnvVar   string `json:"env"`      // env var holding token (bearer) or password (basic)
	Username string `json:"username"` // for basic auth (git-over-https uses any non-empty user)
}

type Config struct {
	// AllowAll opens egress to every host. Hosts with rules are still
	// TLS-intercepted and gated (so injection stays scoped); every other host
	// is blind-tunneled through untouched — no interception, no injection.
	AllowAll bool              `json:"allow_all"`
	Rules    []Rule            `json:"rules"`
	Secrets  map[string]Secret `json:"secrets"`
}

// config is the process-wide loaded policy. It is written once at startup by
// loadConfig and only read afterwards.
var config Config

func loadConfig(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	config = c
	return nil
}
