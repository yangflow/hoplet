package pair

import (
	"bytes"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/noise"
)

func newCode(t *testing.T) Code {
	t.Helper()
	id, err := noise.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return Code{
		ID: id.ID(), Key: id.Public(), Name: "studio",
		Routes:  []string{"ws://192.0.2.1:7443", "wss://b.example"},
		PSK:     bytes.Repeat([]byte{7}, noise.PSKSize),
		Expires: time.Unix(1_800_000_000, 0),
	}
}

func TestRoundTrip(t *testing.T) {
	c := newCode(t)
	s := c.String()
	if !strings.HasPrefix(s, Prefix) || strings.ContainsAny(s[len(Prefix):], "+/=") {
		t.Fatalf("not base64url without padding: %s", s)
	}
	got, err := Parse("  " + s + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("got %+v, want %+v", got, c)
	}
	// Expired codes parse: the caller judges expiry.
	c.Expires = time.Unix(1, 0)
	if _, err := Parse(c.String()); err != nil {
		t.Fatalf("expired code: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	other := newCode(t)
	enc := func(json string) string { return Prefix + base64.RawURLEncoding.EncodeToString([]byte(json)) }
	for name, mut := range map[string]func(c *Code) string{
		"prefix":      func(c *Code) string { return "hoplet2:" + c.String()[len(Prefix):] },
		"base64":      func(c *Code) string { return c.String() + "*" },
		"json":        func(*Code) string { return enc("{") },
		"id mismatch": func(c *Code) string { c.ID = other.ID; return c.String() },
		"short key":   func(c *Code) string { c.Key = c.Key[:8]; return c.String() },
		"short psk":   func(c *Code) string { c.PSK = c.PSK[:16]; return c.String() },
		"no name":     func(c *Code) string { c.Name = ""; return c.String() },
		"long name":   func(c *Code) string { c.Name = strings.Repeat("n", noise.MaxNameSize+1); return c.String() },
		"no routes":   func(c *Code) string { c.Routes = nil; return c.String() },
	} {
		c := newCode(t)
		if _, err := Parse(mut(&c)); !errors.Is(err, ErrCode) {
			t.Errorf("%s: %v, want ErrCode", name, err)
		}
	}
}
