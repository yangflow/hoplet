package store

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
	"time"
)

func TestDevices(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for i, id := range []string{"abcd1111", "abcd2222", "efgh3333"} {
		if err := s.PairDevice(ctx, Device{ID: id, PublicKey: []byte{byte(i)}, Name: "dev " + id, PairedAt: t0.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PairDevice(ctx, Device{Name: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("pair without id: %v", err)
	}

	if err := s.RevokeDevice(ctx, "abcd2222", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(ctx, "abcd2222", t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	d, err := s.Device(ctx, "abcd2222")
	if err != nil || d.RevokedAt == nil || !d.RevokedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("revoked device %+v %v", d, err)
	}
	if err := s.RevokeDevice(ctx, "nope", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke missing: %v", err)
	}
	if _, err := s.Device(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing device: %v", err)
	}

	for prefix, want := range map[string][]string{
		"":         {"abcd1111", "abcd2222", "efgh3333"},
		"abcd":     {"abcd1111", "abcd2222"},
		"abcd2":    {"abcd2222"},
		"efgh3333": {"efgh3333"},
		"zzzz":     nil,
		"%":        nil,
	} {
		list, err := s.Devices(ctx, prefix)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, d := range list {
			ids = append(ids, d.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Errorf("prefix %q: %v, want %v", prefix, ids, want)
		}
	}

	// Pairing again renames, takes the new time and lifts the revocation.
	if err := s.PairDevice(ctx, Device{ID: "abcd2222", PublicKey: []byte{1}, Name: "renamed", PairedAt: t0.Add(3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Device(ctx, "abcd2222")
	if d.Name != "renamed" || d.RevokedAt != nil || !d.PairedAt.Equal(t0.Add(3*time.Hour)) || !reflect.DeepEqual(d.PublicKey, []byte{1}) {
		t.Fatalf("re-paired device %+v", d)
	}
}

func TestHosts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	b := Host{ID: "idb", PublicKey: []byte{2}, Name: "b", Routes: []string{"ws://192.0.2.1:7443"}, AddedAt: t0}
	if err := s.AddHost(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHost(ctx, Host{ID: "ida", PublicKey: []byte{1}, Name: "a", AddedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHost(ctx, Host{ID: "idc", PublicKey: []byte{3}, Name: "b", AddedAt: t0}); !errors.Is(err, ErrExists) {
		t.Fatalf("taken name: %v", err)
	}
	if err := s.AddHost(ctx, Host{ID: "idd", PublicKey: []byte{3}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no name: %v", err)
	}
	// The same ID replaces name and routes.
	b2 := Host{ID: "idb", PublicKey: []byte{2}, Name: "bee", Routes: []string{"wss://b.example", "ws://192.0.2.1:7443"}, AddedAt: t0.Add(time.Hour)}
	if err := s.AddHost(ctx, b2); err != nil {
		t.Fatal(err)
	}
	list, err := s.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Host{{ID: "ida", PublicKey: []byte{1}, Name: "a", Routes: []string{}, AddedAt: t0}, b2}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("hosts %+v, want %+v", list, want)
	}

	if err := s.RemoveHost(ctx, "bee"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveHost(ctx, "ida"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveHost(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if list, _ := s.Hosts(ctx); len(list) != 0 {
		t.Fatalf("hosts left: %+v", list)
	}
}

// TestPeersAfterUpgrade opens a schema-9 store with this build.
func TestPeersAfterUpgrade(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old := fstest.MapFS{}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names[:9] {
		b, err := fs.ReadFile(migrations, name)
		if err != nil {
			t.Fatal(err)
		}
		old[name[len("migrations/"):]] = &fstest.MapFile{Data: b}
	}
	s, err := open(ctx, dir, old)
	if err != nil {
		t.Fatal(err)
	}
	oldSession(t, s, codeSession("a"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Session(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.PairDevice(ctx, Device{ID: "x", PublicKey: []byte{1}, Name: "x", PairedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHost(ctx, Host{ID: "y", PublicKey: []byte{1}, Name: "y", AddedAt: t0}); err != nil {
		t.Fatal(err)
	}
}
