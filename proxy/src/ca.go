package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	caCert   *x509.Certificate
	caKey    *rsa.PrivateKey
	leafMu   sync.Mutex
	leafCert = map[string]*tls.Certificate{}
)

// loadOrCreateCA loads the CA from dir, or generates and persists a new one.
func loadOrCreateCA(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	crtPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	if crtPEM, err := os.ReadFile(crtPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return err
		}
		blk, _ := pem.Decode(crtPEM)
		caCert, err = x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return err
		}
		blk, _ = pem.Decode(keyPEM)
		caKey, err = x509.ParsePKCS1PrivateKey(blk.Bytes)
		return err
	}

	// Generate a fresh CA.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sandbox-proxy CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	caCert, err = x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	caKey = key
	if err := writePEM(crtPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return writePEM(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key), 0o600)
}

// certFor mints (and caches) a leaf cert for host, signed by our CA.
func certFor(host string) (*tls.Certificate, error) {
	leafMu.Lock()
	defer leafMu.Unlock()
	if c, ok := leafCert[host]; ok {
		return c, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{
		Certificate: [][]byte{der, caCert.Raw},
		PrivateKey:  key,
	}
	leafCert[host] = cert
	return cert, nil
}
