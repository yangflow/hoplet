package relay

import (
	"crypto/tls"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// CertManager returns the certificate manager for domains: Let's Encrypt
// certificates through the TLS-ALPN-01 challenge, for those names only,
// cached in dir/certs.
func CertManager(dir string, domains []string) *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domains...),
		Cache:      autocert.DirCache(filepath.Join(dir, "certs")),
	}
}

// TLSConfig serves the certificates get returns for domains. A name that
// is not among domains, an empty one included, gets the first domain's
// certificate: a proxy in front of a backup name reaches the relay that
// way.
func TLSConfig(get func(*tls.ClientHelloInfo) (*tls.Certificate, error), domains []string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1", acme.ALPNProto},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			h := *hello
			h.ServerName = strings.ToLower(strings.TrimSuffix(h.ServerName, "."))
			if !slices.Contains(domains, h.ServerName) {
				h.ServerName = domains[0]
			}
			return get(&h)
		},
	}
}
