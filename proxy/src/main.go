// sandbox-proxy: a zero-dependency (stdlib-only) forward proxy that injects
// credentials on the wire so a sandboxed workload never holds the real tokens.
//
// It MITM-terminates TLS for allow-listed hosts using a locally generated CA,
// applies a default-deny allow list (host + method + path), and injects
// Authorization headers (Bearer/Basic) pulled from the proxy's own environment.
//
// The code is split across files in this one package:
//
//	main.go    — startup
//	proxy.go   — HTTP/CONNECT handling and upstream round-trips
//	tunnel.go  — allow_all blind (non-intercepting) tunnel
//	policy.go  — allow-list matching and credential injection
//	config.go  — config types and loading
//	ca.go      — CA generation and on-the-fly leaf certs
//	helpers.go — small shared utilities
package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	listen := envOr("PROXY_LISTEN", ":3128")
	configPath := envOr("PROXY_CONFIG", "config.json")
	caDir := envOr("PROXY_CA_DIR", "ca")

	if err := loadConfig(configPath); err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := loadOrCreateCA(caDir); err != nil {
		log.Fatalf("ca: %v", err)
	}

	srv := &http.Server{
		Addr:    listen,
		Handler: http.HandlerFunc(handle),
		// The proxy handshakes TLS itself per-connection, so no TLSConfig here.
		ReadHeaderTimeout: 15 * time.Second, // slow-loris guard on the ingress
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("sandbox-proxy listening on %s (%d rules, CA at %s/ca.crt)", listen, len(config.Rules), caDir)
	log.Fatal(srv.ListenAndServe())
}
