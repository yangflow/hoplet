package noise

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/yangflow/hoplet/internal/home"
)

// KeySize is the length of an X25519 public or private key.
const KeySize = 32

// Identity is a long-term X25519 key pair.
type Identity struct {
	priv *ecdh.PrivateKey
}

// HostID names a host or device by its public key: the first 16 bytes of
// the key's SHA-256, in lowercase unpadded base32 (26 characters).
type HostID string

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// IDOf returns the HostID of a public key.
func IDOf(pub []byte) (HostID, error) {
	if len(pub) != KeySize {
		return "", fmt.Errorf("noise: public key is %d bytes, want %d", len(pub), KeySize)
	}
	sum := sha256.Sum256(pub)
	return HostID(strings.ToLower(idEncoding.EncodeToString(sum[:16]))), nil
}

// NewIdentity generates a fresh identity.
func NewIdentity() (Identity, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("noise: generate identity: %w", err)
	}
	return Identity{priv: priv}, nil
}

// ParseIdentity reads a private key written as 64 hexadecimal characters.
func ParseIdentity(s string) (Identity, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return Identity{}, fmt.Errorf("noise: identity is not hexadecimal: %w", err)
	}
	if len(b) != KeySize {
		return Identity{}, fmt.Errorf("noise: identity is %d bytes, want %d", len(b), KeySize)
	}
	priv, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return Identity{}, fmt.Errorf("noise: identity: %w", err)
	}
	return Identity{priv: priv}, nil
}

// LoadIdentity reads home's identity key. A missing file is reported as
// os.ErrNotExist; a file other users can access is refused.
func LoadIdentity(homeDir string) (Identity, error) {
	s, err := home.ReadIdentity(homeDir)
	if err != nil {
		return Identity{}, fmt.Errorf("noise: load identity: %w", err)
	}
	return ParseIdentity(s)
}

// EnsureIdentity returns home's identity key, creating it once.
func EnsureIdentity(homeDir string) (Identity, error) {
	s, err := home.EnsureIdentity(homeDir, func() (string, error) {
		id, err := NewIdentity()
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(id.priv.Bytes()), nil
	})
	if err != nil {
		return Identity{}, fmt.Errorf("noise: ensure identity: %w", err)
	}
	return ParseIdentity(s)
}

// Valid reports whether the identity holds a key.
func (id Identity) Valid() bool { return id.priv != nil }

// Public returns the public key.
func (id Identity) Public() []byte { return id.priv.PublicKey().Bytes() }

// ID returns the HostID of the public key.
func (id Identity) ID() HostID {
	hid, _ := IDOf(id.Public()) // a public key is always KeySize bytes
	return hid
}
