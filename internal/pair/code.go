// Package pair encodes pairing codes: what a host shows so another device
// can pair with it once.
package pair

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yangflow/hoplet/internal/noise"
)

// Prefix starts every pairing code.
const Prefix = "hoplet1:"

// ErrCode is returned for a pairing code that cannot be read.
var ErrCode = errors.New("pair: bad pairing code")

// Code is a host's invitation to pair once.
type Code struct {
	ID      noise.HostID // host ID of Key
	Key     []byte       // the host's public key
	Name    string       // the host's display name
	Routes  []string     // where the host is reached, in order
	PSK     []byte       // the one-time pairing secret
	Expires time.Time    // the host refuses the secret after this
}

// wire is a code's JSON form.
type wire struct {
	ID     string   `json:"id"`
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Routes []string `json:"routes"`
	PSK    string   `json:"psk"`
	Exp    int64    `json:"exp"`
}

var b64 = base64.RawURLEncoding

// String encodes the code.
func (c Code) String() string {
	b, err := json.Marshal(wire{
		ID: string(c.ID), Key: b64.EncodeToString(c.Key), Name: c.Name, Routes: c.Routes,
		PSK: b64.EncodeToString(c.PSK), Exp: c.Expires.Unix(),
	})
	if err != nil {
		// A struct of strings and an int always marshals.
		return Prefix
	}
	return Prefix + b64.EncodeToString(b)
}

// Parse decodes a code and checks its parts. It does not check expiry:
// the caller compares Expires with its clock.
func Parse(s string) (Code, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), Prefix)
	if !ok {
		return Code{}, fmt.Errorf("%w: it does not start with %q", ErrCode, Prefix)
	}
	raw, err := b64.DecodeString(rest)
	if err != nil {
		return Code{}, fmt.Errorf("%w: %w", ErrCode, err)
	}
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Code{}, fmt.Errorf("%w: %w", ErrCode, err)
	}
	key, err := b64.DecodeString(w.Key)
	if err != nil {
		return Code{}, fmt.Errorf("%w: key: %w", ErrCode, err)
	}
	id, err := noise.IDOf(key)
	if err != nil {
		return Code{}, fmt.Errorf("%w: key: %w", ErrCode, err)
	}
	if string(id) != w.ID {
		return Code{}, fmt.Errorf("%w: the ID does not match the key", ErrCode)
	}
	psk, err := b64.DecodeString(w.PSK)
	if err != nil {
		return Code{}, fmt.Errorf("%w: secret: %w", ErrCode, err)
	}
	if len(psk) != noise.PSKSize {
		return Code{}, fmt.Errorf("%w: the secret is %d bytes, want %d", ErrCode, len(psk), noise.PSKSize)
	}
	if w.Name == "" || len(w.Name) > noise.MaxNameSize {
		return Code{}, fmt.Errorf("%w: the name must be 1 to %d bytes", ErrCode, noise.MaxNameSize)
	}
	if len(w.Routes) == 0 || w.Exp == 0 {
		return Code{}, fmt.Errorf("%w: no routes or no expiry", ErrCode)
	}
	return Code{ID: id, Key: key, Name: w.Name, Routes: w.Routes, PSK: psk, Expires: time.Unix(w.Exp, 0)}, nil
}
