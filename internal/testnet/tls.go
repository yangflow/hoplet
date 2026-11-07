package testnet

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TLS returns a server configuration with a test certificate for
// 127.0.0.1, ::1 and example.com, and the roots that trust it. Nothing
// leaves the machine.
func TLS(t testing.TB) (*tls.Config, *x509.CertPool) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	conf := ts.TLS.Clone()
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	ts.Close()
	return conf, roots
}
