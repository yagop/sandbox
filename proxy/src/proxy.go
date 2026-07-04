package main

import (
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

// handle dispatches plain HTTP proxying vs. CONNECT (HTTPS) tunneling.
func handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		handleConnect(w, r)
		return
	}
	// Plain HTTP forward proxy: r.URL is absolute.
	if !r.URL.IsAbs() {
		http.Error(w, "sandbox-proxy: only proxy requests accepted", http.StatusBadRequest)
		return
	}
	forwardHTTP(w, r)
}

// handleConnect intercepts an HTTPS tunnel. Allow-listed hosts are TLS-terminated
// with an on-the-fly cert and served through a real http.Server — which owns
// request parsing, response framing (so HTTP/2 upstreams relay cleanly), and
// per-connection timeouts. Other hosts are blind-tunneled when allow_all is set.
func handleConnect(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	mitm := hostHasRules(host) // only intercept hosts we might inject into / gate
	if !mitm && !config.AllowAll {
		log.Printf("DENY connect %s (host not allowed)", host)
		http.Error(w, "sandbox-proxy: host not allowed", http.StatusForbidden)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}

	if !mitm {
		// allow_all passthrough: splice the encrypted stream without decrypting.
		// The proxy never sees plaintext and never injects here.
		defer clientConn.Close()
		blindTunnel(clientConn, r.Host, host)
		return
	}

	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		clientConn.Close()
		return
	}

	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"}, // we serve HTTP/1.1 only; never negotiate h2
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName // mint for the SNI the client actually validates
			if name == "" {
				name = host
			}
			return certFor(name)
		},
	}
	tlsConn := tls.Server(clientConn, tlsConf)

	// Bound the handshake so a client that stalls after CONNECT can't pin a
	// goroutine forever, then clear it for the (possibly long-lived) session.
	tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		tlsConn.Close()
		return
	}
	tlsConn.SetDeadline(time.Time{})

	// From here the connection is owned by the http.Server (which closes it on
	// StateClosed) or, on a WebSocket upgrade, by the hijacking handler. We must
	// NOT close it here — that would race a handler still splicing frames.

	// Serve HTTP/1.1 off the terminated TLS connection via a one-shot listener.
	// ConnState closes the listener when the connection ends so Serve returns.
	ln := newOneShotListener(tlsConn)
	srv := &http.Server{
		Handler:           interceptHandler(r.Host),
		ReadHeaderTimeout: 10 * time.Second,   // slow-loris guard
		ReadTimeout:       60 * time.Second,   // whole-request read bound
		WriteTimeout:      30 * time.Minute,   // allow long streaming (git clone, SSE)
		IdleTimeout:       2 * time.Minute,    // cap idle keep-alives
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				ln.Close()
			}
		},
	}
	srv.Serve(ln)
}

// interceptHandler serves one decrypted request. The upstream authority is
// pinned to the CONNECT target (authority) so a rewritten inner Host header
// cannot redirect the tunnel to a different host.
func interceptHandler(authority string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(authority)
		rule, ok := decide(host)
		if !ok {
			log.Printf("DENY %s %s%s", r.Method, host, r.URL.Path)
			http.Error(w, "sandbox-proxy: request not allowed by policy", http.StatusForbidden)
			return
		}
		if isWebSocketUpgrade(r) {
			log.Printf("ALLOW %s %s%s%s (ws)", r.Method, host, r.URL.Path, injLabel(rule))
			forwardWebSocket(w, r, authority, rule)
			return
		}
		log.Printf("ALLOW %s %s%s%s", r.Method, host, r.URL.Path, injLabel(rule))
		forwardVia(w, r, authority, true, rule)
	}
}

// forwardHTTP handles plain (non-TLS) absolute-form forward-proxy requests. Per
// RFC 7230 §5.4 the request-target (r.URL), not the Host header, is authoritative.
func forwardHTTP(w http.ResponseWriter, r *http.Request) {
	authority := r.URL.Host
	host := hostOnly(authority)
	rule, ok := decide(host)
	if !ok {
		log.Printf("DENY %s %s%s", r.Method, host, r.URL.Path)
		http.Error(w, "sandbox-proxy: request not allowed by policy", http.StatusForbidden)
		return
	}
	log.Printf("ALLOW %s %s%s%s", r.Method, host, r.URL.Path, injLabel(rule))
	forwardVia(w, r, authority, false, rule)
}

// forwardVia rewrites r toward scheme://authority, injects the rule's credential,
// and relays the upstream response back through w. Because w is a stdlib
// ResponseWriter, the response is re-framed as HTTP/1.1 regardless of the
// upstream protocol (HTTP/2 included) — no manual resp.Write.
func forwardVia(w http.ResponseWriter, r *http.Request, authority string, useTLS bool, rule *Rule) {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	outURL := &url.URL{Scheme: scheme, Host: authority, Path: path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		http.Error(w, "sandbox-proxy: bad gateway", http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)
	outReq.Host = hostHeader(scheme, authority)
	outReq.ContentLength = r.ContentLength
	if rule != nil {
		inject(outReq, rule)
	}

	resp, err := upstream.RoundTrip(outReq)
	if err != nil {
		log.Printf("upstream %s: %v", authority, err)
		http.Error(w, "sandbox-proxy: upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flushCopy(w, resp.Body)
}

// upstream is the shared transport for outbound requests.
var upstream = &http.Transport{
	// Chain through an upstream proxy if the host sets HTTP(S)_PROXY (corporate
	// egress, nested sandboxes); dials directly otherwise.
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	// HTTP/2 upstreams are fine now: we relay through a ResponseWriter which
	// re-frames as HTTP/1.1, so no need to force http/1.1 here.
}
