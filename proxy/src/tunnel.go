package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

// blindTunnel splices an encrypted CONNECT stream straight through without
// terminating TLS, so the proxy neither reads plaintext nor injects anything.
// hostport is the CONNECT authority (host:port); host is for logging only.
func blindTunnel(client net.Conn, hostport, host string) {
	up, err := dialUpstream(hostport)
	if err != nil {
		log.Printf("TUNNEL-FAIL %s: %v", host, err)
		writeSimple(client, http.StatusBadGateway, "sandbox-proxy: upstream dial failed\n")
		return
	}
	defer up.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	log.Printf("TUNNEL %s (no interception, no injection)", host)

	// Copy in both directions; return as soon as either side closes. The two
	// defers (here and in handleConnect) tear both connections down.
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, client); done <- struct{}{} }()
	go func() { io.Copy(client, up); done <- struct{}{} }()
	<-done
}

// dialUpstream opens a raw TCP connection to hostport, going through an
// upstream HTTP proxy (via CONNECT) when HTTP(S)_PROXY is set, else directly.
func dialUpstream(hostport string) (net.Conn, error) {
	px := upstreamProxyFor(hostport)
	if px == nil {
		return net.DialTimeout("tcp", hostport, 10*time.Second)
	}
	c, err := net.DialTimeout("tcp", px.Host, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", hostport, hostport); err != nil {
		c.Close()
		return nil, err
	}
	// Read the proxy's CONNECT reply. For a 200 there is no body and — since TLS
	// clients speak first — no tunnel bytes buffered ahead, so returning c is safe.
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		c.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf("upstream proxy CONNECT %s: %s", hostport, resp.Status)
	}
	if br.Buffered() > 0 {
		c.Close()
		return nil, fmt.Errorf("upstream proxy sent %d unexpected bytes after CONNECT", br.Buffered())
	}
	return c, nil
}

// upstreamProxyFor resolves the configured proxy (respecting NO_PROXY) for a
// CONNECT to hostport, or nil for a direct dial.
func upstreamProxyFor(hostport string) *url.URL {
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: hostport}}
	u, err := http.ProxyFromEnvironment(req)
	if err != nil {
		return nil
	}
	return u
}
