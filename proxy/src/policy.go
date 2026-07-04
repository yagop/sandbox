package main

import (
	"log"
	"net/http"
	"os"
	"strings"
)

// match returns the rule for host (exact), or nil.
func match(host string) *Rule {
	for i := range config.Rules {
		if config.Rules[i].Host == host {
			return &config.Rules[i]
		}
	}
	return nil
}

// inject adds the rule's configured Authorization header, reading the secret
// value from the proxy's own environment so tokens never enter the workload.
func inject(req *http.Request, ru *Rule) {
	if ru.Inject == "" {
		return
	}
	s, ok := config.Secrets[ru.Inject]
	if !ok {
		log.Printf("WARN rule references unknown secret %q", ru.Inject)
		return
	}
	val := os.Getenv(s.EnvVar)
	if val == "" {
		log.Printf("WARN secret %q: env %s is empty; not injecting", ru.Inject, s.EnvVar)
		return
	}
	switch strings.ToLower(s.Type) {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+val)
	case "basic":
		user := s.Username
		if user == "" {
			user = "x-access-token" // GitHub convention for token-as-password
		}
		req.SetBasicAuth(user, val)
	default:
		log.Printf("WARN secret %q: unknown type %q", ru.Inject, s.Type)
	}
}

// decide returns the matched rule (nil if none) and whether the request is
// permitted. A configured host is allowed (and injected); any other host is
// permitted only when allow_all is set, with no rule (hence no injection).
func decide(host string) (*Rule, bool) {
	if ru := match(host); ru != nil {
		return ru, true
	}
	return nil, config.AllowAll
}

// hostHasRules reports whether host is configured (used to decide whether to
// TLS-intercept it vs. blind-tunnel).
func hostHasRules(host string) bool {
	return match(host) != nil
}
