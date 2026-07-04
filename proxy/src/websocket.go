package main

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

// isWebSocketUpgrade reports whether r is a WebSocket handshake request
// (Connection: Upgrade + Upgrade: websocket, per RFC 6455).
func isWebSocketUpgrade(r *http.Request) bool {
	return headerHasToken(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// headerHasToken reports whether the comma-separated header value contains token
// (case-insensitive), e.g. Connection: "keep-alive, Upgrade".
func headerHasToken(header, token string) bool {
	for _, p := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

// forwardWebSocket handles an intercepted WebSocket handshake on a configured
// host. It re-issues the handshake to the TLS upstream with credentials injected
// and the Upgrade/Connection headers preserved (forwardVia would strip them),
// and on 101 hijacks the client connection and splices both directions until
// either side closes.
func forwardWebSocket(w http.ResponseWriter, r *http.Request, authority string, rule *Rule) {
	upConn, err := dialUpstreamTLS(authority)
	if err != nil {
		log.Printf("ws upstream %s: %v", authority, err)
		http.Error(w, "sandbox-proxy: upstream dial failed", http.StatusBadGateway)
		return
	}
	defer upConn.Close()

	// Rebuild the handshake toward the upstream, preserving ALL client headers
	// (Upgrade, Connection, Sec-WebSocket-*), then inject the credential.
	outReq, err := http.NewRequest(r.Method, "https://"+authority+r.URL.RequestURI(), nil)
	if err != nil {
		http.Error(w, "sandbox-proxy: bad gateway", http.StatusBadGateway)
		return
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			outReq.Header.Add(k, v)
		}
	}
	outReq.Host = hostHeader("https", authority)
	if rule != nil {
		inject(outReq, rule)
	}
	if err := outReq.Write(upConn); err != nil {
		http.Error(w, "sandbox-proxy: upstream write failed", http.StatusBadGateway)
		return
	}

	upBuf := bufio.NewReader(upConn)
	resp, err := http.ReadResponse(upBuf, outReq)
	if err != nil {
		http.Error(w, "sandbox-proxy: upstream read failed", http.StatusBadGateway)
		return
	}

	// Upstream declined the upgrade — relay the normal response and stop.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		flushCopy(w, resp.Body)
		return
	}

	// 101: take over the client connection and splice raw frames.
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	if _, err := io.WriteString(clientConn, "HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		return
	}
	_ = resp.Header.Write(clientConn)
	if _, err := io.WriteString(clientConn, "\r\n"); err != nil {
		return
	}
	log.Printf("WS %s%s%s (upgraded)", hostOnly(authority), r.URL.Path, injLabel(rule))

	// Splice both directions; flush bytes already buffered on either reader
	// (frames that arrived alongside the handshake) before the raw copy.
	done := make(chan struct{}, 2)
	go func() {
		flushBuffered(clientBuf.Reader, upConn)
		io.Copy(upConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		flushBuffered(upBuf, clientConn)
		io.Copy(clientConn, upConn)
		done <- struct{}{}
	}()
	<-done
}

// flushBuffered writes any bytes already buffered in br out to dst.
func flushBuffered(br *bufio.Reader, dst io.Writer) {
	if n := br.Buffered(); n > 0 {
		b, _ := br.Peek(n)
		dst.Write(b)
		br.Discard(n)
	}
}

// dialUpstreamTLS dials authority (host:port) — through the env proxy if set —
// and completes a TLS handshake, verifying the real upstream certificate.
func dialUpstreamTLS(authority string) (net.Conn, error) {
	raw, err := dialUpstream(authority)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(raw, &tls.Config{ServerName: hostOnly(authority)})
	if err := tc.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}
