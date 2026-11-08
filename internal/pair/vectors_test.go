package pair

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/noise"
)

// TestVectors builds testdata/vectors/pairing.json from this package's real
// parser: with -update it writes the file (go generate), otherwise it fails
// if the file differs.
var updateVectors = flag.Bool("update", false, "rewrite testdata/vectors")

const vectorFile = "../../testdata/vectors/pairing.json"

type validCode struct {
	Name    string   `json:"name"`
	Code    string   `json:"code"`
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Host    string   `json:"host"`
	Routes  []string `json:"routes"`
	PSK     string   `json:"psk"`
	Expires int64    `json:"expires_unix"`
}

type invalidCode struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

type hostID struct {
	Public string `json:"public"`
	ID     string `json:"id"`
}

type pairingVectors struct {
	Description string        `json:"description"`
	Valid       []validCode   `json:"valid"`
	Invalid     []invalidCode `json:"invalid"`
	HostIDs     []hostID      `json:"host_ids"`
}

func testBytes(label string) []byte {
	sum := sha256.Sum256([]byte("hoplet vector pairing " + label))
	return sum[:]
}

func encodeWire(t *testing.T, w wire) string {
	t.Helper()
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return Prefix + b64.EncodeToString(b)
}

func TestVectors(t *testing.T) {
	key := testBytes("host key")
	id, err := noise.IDOf(key)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Unix(1_793_433_600, 0)
	base := Code{
		ID: id, Key: key, Name: "测试 Mac", PSK: testBytes("psk"), Expires: expires,
		Routes: []string{"ws://192.0.2.1:7890", "wss://relay.example.com"},
	}
	good := wire{
		ID: string(id), Key: b64.EncodeToString(key), Name: "Mac",
		Routes: []string{"ws://192.0.2.1:7890"}, PSK: b64.EncodeToString(base.PSK), Exp: expires.Unix(),
	}
	change := func(f func(*wire)) string {
		w := good
		f(&w)
		return encodeWire(t, w)
	}

	v := pairingVectors{Description: "Pairing codes and host IDs. Expiry is the caller's to compare."}
	for _, c := range []struct{ name, code string }{
		{"plain", base.String()},
		{"surrounding_whitespace", "  " + base.String() + "\n"},
	} {
		got, err := Parse(c.code)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		v.Valid = append(v.Valid, validCode{
			Name: c.name, Code: c.code, ID: string(got.ID), Key: hex.EncodeToString(got.Key),
			Host: got.Name, Routes: got.Routes, PSK: hex.EncodeToString(got.PSK), Expires: got.Expires.Unix(),
		})
	}

	for _, c := range []invalidCode{
		{"wrong_prefix", "hoplet2:" + strings.TrimPrefix(base.String(), Prefix)},
		{"bad_base64", Prefix + "***"},
		{"not_json", Prefix + b64.EncodeToString([]byte("not json"))},
		{"key_not_base64", change(func(w *wire) { w.Key = "***" })},
		{"short_key", change(func(w *wire) { w.Key = b64.EncodeToString(key[:31]) })},
		{"id_does_not_match_key", change(func(w *wire) { w.ID = strings.Repeat("a", 26) })},
		{"psk_not_base64", change(func(w *wire) { w.PSK = "***" })},
		{"short_psk", change(func(w *wire) { w.PSK = b64.EncodeToString(base.PSK[:31]) })},
		{"empty_name", change(func(w *wire) { w.Name = "" })},
		{"long_name", change(func(w *wire) { w.Name = strings.Repeat("a", noise.MaxNameSize+1) })},
		{"no_routes", change(func(w *wire) { w.Routes = nil })},
		{"no_expiry", change(func(w *wire) { w.Exp = 0 })},
	} {
		if _, err := Parse(c.Code); !errors.Is(err, ErrCode) {
			t.Fatalf("%s: Parse = %v, want ErrCode", c.Name, err)
		}
		v.Invalid = append(v.Invalid, c)
	}

	for _, label := range []string{"host key", "device key", "third key"} {
		pub := testBytes(label)
		got, err := noise.IDOf(pub)
		if err != nil {
			t.Fatal(err)
		}
		v.HostIDs = append(v.HostIDs, hostID{Public: hex.EncodeToString(pub), ID: string(got)})
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if *updateVectors {
		if err := os.MkdirAll(filepath.Dir(vectorFile), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorFile, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatalf("%v; run make gen", err)
	}
	if !bytes.Equal(have, data) {
		t.Fatal("pairing.json differs from the implementation; run make gen")
	}
}
