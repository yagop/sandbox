package main

import (
	"io"
	"net"
	"net/http"
)

// hopByHopHeaders must not be forwarded between client and upstream (RFC 7230
// §6.1). Notably Transfer-Encoding/Content-Length framing is owned by whichever
// side writes the message, so relaying them corrupts the re-framed response.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// copyHeaders copies src into dst, dropping hop-by-hop headers. Keys from
// net/http are already canonicalized.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if hopByHopHeaders[k] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// hostHeader returns authority with a default port (443/80) stripped, so the
// Host header matches what host-signed schemes (SigV4, GCS, …) expect; a
// non-default port is preserved for vhost routing.
func hostHeader(scheme, authority string) string {
	h, p, err := net.SplitHostPort(authority)
	if err != nil {
		return authority // no port present
	}
	if (scheme == "https" && p == "443") || (scheme == "http" && p == "80") {
		return h
	}
	return authority
}

// flushCopy streams src to w, flushing after each write so streaming responses
// (SSE, long transfers) reach the client promptly rather than buffering.
func flushCopy(w http.ResponseWriter, src io.Reader) {
	if f, ok := w.(http.Flusher); ok {
		io.Copy(&flushWriter{w: w, f: f}, src)
		return
	}
	io.Copy(w, src)
}

type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if n > 0 {
		fw.f.Flush()
	}
	return n, err
}

// oneShotListener yields a single already-accepted net.Conn to http.Server.Serve
// and then blocks Accept until Close, so Serve stays alive for the lifetime of
// that one connection (used to run an http.Server over a hijacked TLS conn).
type oneShotListener struct {
	conn   net.Conn
	yield  chan net.Conn
	closed chan struct{}
}

func newOneShotListener(c net.Conn) *oneShotListener {
	l := &oneShotListener{conn: c, yield: make(chan net.Conn, 1), closed: make(chan struct{})}
	l.yield <- c
	return l
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.yield:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *oneShotListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *oneShotListener) Addr() net.Addr { return l.conn.LocalAddr() }
