package noise

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/home"
)

// rfc7748Priv and rfc7748Pub are Alice's public key from RFC 7748 section 6.1.
const (
	rfc7748Priv = "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a"
	rfc7748Pub  = "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a"
)

func TestEnsureIdentityCreatesOnce(t *testing.T) {
	h := t.TempDir()
	if _, err := LoadIdentity(h); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load missing identity: %v", err)
	}
	a, err := EnsureIdentity(h)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnsureIdentity(h)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Public(), b.Public()) {
		t.Fatal("second ensure made a new key")
	}
	c, err := LoadIdentity(h)
	if err != nil || !bytes.Equal(a.Public(), c.Public()) {
		t.Fatalf("load %v", err)
	}
	raw, err := os.ReadFile(home.IdentityFile(h))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(string(raw), "\n")
	if len(line) != 64 || strings.Contains(line, "\n") {
		t.Fatalf("identity file %q", raw)
	}
	if _, err := hex.DecodeString(line); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIdentityRefusesBadFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		want    string
	}{
		{"shared", rfc7748Priv, 0o644, "chmod 600"},
		{"not hex", strings.Repeat("zz", 32), 0o600, "not hexadecimal"},
		{"short", rfc7748Priv[:62], 0o600, "31 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := t.TempDir()
			if err := os.MkdirAll(h+"/keys", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(home.IdentityFile(h), []byte(tt.content+"\n"), tt.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(home.IdentityFile(h), tt.mode); err != nil {
				t.Fatal(err)
			}
			_, err := LoadIdentity(h)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestIdentityKeys(t *testing.T) {
	id, err := ParseIdentity(rfc7748Priv)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(id.Public()); got != rfc7748Pub {
		t.Fatalf("public %s", got)
	}
	pub, _ := hex.DecodeString(rfc7748Pub)
	hid, err := IDOf(pub)
	if err != nil {
		t.Fatal(err)
	}
	if hid != id.ID() || len(hid) != 26 || strings.ToLower(string(hid)) != string(hid) {
		t.Fatalf("id %q", hid)
	}
	if hid != "gagjzfqdxevewopnhfml7esace" {
		t.Fatalf("id %q", hid)
	}
	if _, err := IDOf(pub[:31]); err == nil {
		t.Fatal("accepted a 31-byte key")
	}
}
