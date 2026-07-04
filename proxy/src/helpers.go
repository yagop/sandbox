package main

import (
	"cmp"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
)

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

// writeSimple writes a minimal HTTP/1.1 response directly to a raw connection
// (used on the hijacked CONNECT socket, where there is no ResponseWriter).
func writeSimple(c net.Conn, code int, body string) {
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func injLabel(ru *Rule) string {
	if ru == nil || ru.Inject == "" {
		return ""
	}
	return " [+" + ru.Inject + "]"
}

// envOr returns the environment value for k, or def when unset/empty.
func envOr(k, def string) string {
	return cmp.Or(os.Getenv(k), def)
}
