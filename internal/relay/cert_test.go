package relay

import (
	"crypto/tls"
	"testing"
)

func TestTLSConfigChoosesTheCertificate(t *testing.T) {
	domains := []string{"relay.example.com", "backup.example.com"}
	var asked string
	conf := TLSConfig(func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		asked = h.ServerName
		return &tls.Certificate{}, nil
	}, domains)
	for sni, want := range map[string]string{
		"relay.example.com":   "relay.example.com",
		"backup.example.com":  "backup.example.com",
		"Backup.Example.com.": "backup.example.com",
		"proxied.example.net": "relay.example.com",
		"":                    "relay.example.com",
	} {
		if _, err := conf.GetCertificate(&tls.ClientHelloInfo{ServerName: sni}); err != nil {
			t.Fatal(err)
		}
		if asked != want {
			t.Errorf("SNI %q asked for %q, want %q", sni, asked, want)
		}
	}
}

func TestCertManagerAllowsOnlyTheDomains(t *testing.T) {
	m := CertManager(t.TempDir(), []string{"relay.example.com"})
	if err := m.HostPolicy(t.Context(), "relay.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.HostPolicy(t.Context(), "other.example.com"); err == nil {
		t.Fatal("the policy allowed another name")
	}
}
