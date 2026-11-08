package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

const testPlatform = "test-platform"

// release is a fake release host.
type release struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	stable string
	bodies map[string][]byte
	sums   map[string]string // overrides of the published checksum
	gets   int               // binary downloads served
	verify error             // what Source.Verify returns
}

func newRelease(t *testing.T) *release {
	t.Helper()
	r := &release{t: t, bodies: map[string][]byte{}, sums: map[string]string{}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *release) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "stable":
		_, _ = w.Write([]byte(r.stable + "\n"))
	case len(parts) == 2 && parts[1] == "manifest.json" && r.bodies[parts[0]] != nil:
		body := r.bodies[parts[0]]
		sum := sha256.Sum256(body)
		checksum := hex.EncodeToString(sum[:])
		if o, ok := r.sums[parts[0]]; ok {
			checksum = o
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"platforms": map[string]any{testPlatform: map[string]any{"checksum": checksum, "size": len(body)}}})
	case len(parts) == 3 && parts[1] == testPlatform && parts[2] == "claude" && r.bodies[parts[0]] != nil:
		r.gets++
		_, _ = w.Write(r.bodies[parts[0]])
	default:
		http.NotFound(w, req)
	}
}

// publish makes version the stable channel's and returns its binary.
func (r *release) publish(version string) []byte {
	r.t.Helper()
	bin, _ := fakeClaude(r.t, version)
	body, err := os.ReadFile(bin)
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stable = version
	r.bodies[version] = body
	return body
}

func (r *release) source() Source {
	return Source{BaseURL: r.srv.URL, Platform: testPlatform, Client: r.srv.Client(), Verify: func(context.Context, string) error { return r.verify }}
}

// ageUpdate moves the recorded follow-up back in time.
func ageUpdate(t *testing.T, home string, by time.Duration) {
	t.Helper()
	u, ok := lastUpdate(BinaryPath(home))
	if !ok {
		t.Fatal("no update.json")
	}
	u.At = u.At.Add(-by)
	if err := verified.Save(stateDir(BinaryPath(home)), u); err != nil {
		t.Fatal(err)
	}
}

// checker is a fake self-check that records the binaries it checked.
type checker struct {
	err     error
	checked []string
	content [][]byte // what each checked file held
}

func (c *checker) check(_ context.Context, bin string) error {
	c.checked = append(c.checked, bin)
	b, _ := os.ReadFile(bin)
	c.content = append(c.content, b)
	return c.err
}

func link(t *testing.T, home string) string {
	t.Helper()
	target, err := os.Readlink(BinaryPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestInstallVerifiedVersionSkipsTheSelfCheck(t *testing.T) {
	rel := newRelease(t)
	rel.publish("2.1.312")
	home := t.TempDir()
	c := &checker{}
	got, err := Install(context.Background(), home, rel.source(), c.check)
	if err != nil || got != (Installed{Version: "2.1.312"}) || len(c.checked) != 0 {
		t.Fatalf("install %+v %v, checked %v", got, err, c.checked)
	}
	if target := link(t, home); target != "claude-2.1.312" {
		t.Fatalf("link %q", target)
	}
	fi, err := os.Stat(BinaryPath(home))
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary %v %v", fi, err)
	}
	if info, err := (&Harness{Binary: BinaryPath(home)}).Probe(context.Background()); err != nil || info.Version != "2.1.312" || !info.Supported {
		t.Fatalf("probe of the installed copy %+v %v", info, err)
	}
	// Installing again replaces the link in place.
	if _, err := Install(context.Background(), home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
}

func TestInstallChecksAnUnknownVersionOnce(t *testing.T) {
	rel := newRelease(t)
	rel.publish("2.1.400")
	home := t.TempDir()
	c := &checker{}
	got, err := Install(context.Background(), home, rel.source(), c.check)
	if err != nil || got != (Installed{Version: "2.1.400", Checked: true}) || len(c.checked) != 1 {
		t.Fatalf("install %+v %v, checked %v", got, err, c.checked)
	}
	if info, err := (&Harness{Binary: BinaryPath(home)}).Probe(context.Background()); err != nil || !info.Supported {
		t.Fatalf("a version that passed here is unsupported: %+v %v", info, err)
	}
	// The pass is remembered.
	got, err = Install(context.Background(), home, rel.source(), c.check)
	if err != nil || got.Checked || len(c.checked) != 1 {
		t.Fatalf("second install %+v %v, checked %v", got, err, c.checked)
	}
}

func TestInstallRefusesAVersionThatFailsTheSelfCheck(t *testing.T) {
	rel := newRelease(t)
	rel.publish("2.1.312")
	home := t.TempDir()
	if _, err := Install(context.Background(), home, rel.source(), (&checker{}).check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.400")
	c := &checker{err: errors.New("no approval came")}
	if _, err := Install(context.Background(), home, rel.source(), c.check); err == nil || !strings.Contains(err.Error(), "failed the self-check") || !strings.Contains(err.Error(), "no approval came") {
		t.Fatalf("failed check installed: %v", err)
	}
	if target := link(t, home); target != "claude-2.1.312" {
		t.Fatalf("a refused install changed the link to %q", target)
	}
	if _, err := os.Stat(BinaryPath(home) + "-2.1.400"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused version was kept: %v", err)
	}
}

func TestFollow(t *testing.T) {
	rel := newRelease(t)
	ctx := context.Background()
	home := t.TempDir()
	rel.publish("2.1.312")
	c := &checker{}

	if _, ok := Follow(ctx, home, rel.source(), c.check); ok {
		t.Fatal("followed without a copy of Hoplet's")
	}
	if _, err := Install(ctx, home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	if _, ok := Follow(ctx, home, rel.source(), c.check); ok {
		t.Fatal("followed the same version")
	}

	// The stable channel moves; the follow-up checks and moves too.
	newer := rel.publish("2.1.400")
	u, ok := Follow(ctx, home, rel.source(), c.check)
	if !ok || u.Error != "" || u.From != "2.1.312" || u.Version != "2.1.400" || len(c.checked) != 1 {
		t.Fatalf("follow %+v %v, checked %v", u, ok, c.checked)
	}
	// The check ran on a private copy inside bin/ with the source's content.
	if got := c.checked[0]; filepath.Dir(got) != filepath.Dir(BinaryPath(home)) {
		t.Fatalf("checked %s, outside bin/", got)
	}
	if !bytes.Equal(c.content[0], newer) {
		t.Fatal("the checked copy differs from the release")
	}
	if target := link(t, home); target != "claude-2.1.400" {
		t.Fatalf("link %q", target)
	}
	if _, err := os.Stat(BinaryPath(home) + "-2.1.312"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old copy is still there: %v", err)
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Supported || info.Attention || !strings.Contains(info.Notice, "followed from 2.1.312") {
		t.Fatalf("probe after follow %+v %v", info, err)
	}

	// A version that fails stays out, once.
	rel.publish("2.1.500")
	c.err = errors.New("interrupt ignored")
	u, ok = Follow(ctx, home, rel.source(), c.check)
	if !ok || !strings.Contains(u.Error, "interrupt ignored") || len(c.checked) != 2 {
		t.Fatalf("failed follow %+v %v, checked %v", u, ok, c.checked)
	}
	if target := link(t, home); target != "claude-2.1.400" {
		t.Fatalf("a failed follow moved the link to %q", target)
	}
	info, err = (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Attention || !strings.Contains(info.Notice, "2.1.500") || !strings.Contains(info.Notice, "stays on 2.1.400") {
		t.Fatalf("probe after a failed follow %+v %v", info, err)
	}
	if !strings.Contains(info.Notice, "attempt 1 of 3") {
		t.Fatalf("notice %q lacks the attempt", info.Notice)
	}
}

func TestFollowGivesUpAfterThreeAttempts(t *testing.T) {
	rel := newRelease(t)
	ctx := context.Background()
	home := t.TempDir()
	rel.publish("2.1.312")
	c := &checker{}
	if _, err := Install(ctx, home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.500")
	c.err = errors.New("flaky")
	for i := 1; i <= 3; i++ {
		u, ok := Follow(ctx, home, rel.source(), c.check)
		if !ok || u.Attempts != i || len(c.checked) != i {
			t.Fatalf("attempt %d: %+v %v, checked %d", i, u, ok, len(c.checked))
		}
		ageUpdate(t, home, 24*time.Hour) // a day passes
	}
	if _, ok := Follow(ctx, home, rel.source(), c.check); ok || len(c.checked) != 3 {
		t.Fatalf("a given-up version was checked again: %d", len(c.checked))
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Attention || !strings.Contains(info.Notice, "attempt 3 of 3") || !strings.Contains(info.Notice, "will not try this version again") {
		t.Fatalf("notice %+v %v", info, err)
	}
}

func TestFollowRecordsNothingWhenCancelled(t *testing.T) {
	rel := newRelease(t)
	home := t.TempDir()
	rel.publish("2.1.312")
	c := &checker{}
	if _, err := Install(context.Background(), home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.400")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.err = context.Canceled
	if u, ok := Follow(ctx, home, rel.source(), c.check); ok || u != (Update{}) {
		t.Fatalf("a cancelled follow recorded %+v %v", u, ok)
	}
	if _, ok := lastUpdate(BinaryPath(home)); ok {
		t.Fatal("a cancelled follow wrote update.json")
	}
	c.err = nil
	n := len(c.checked)
	if u, ok := Follow(context.Background(), home, rel.source(), c.check); !ok || u.Error != "" || len(c.checked) != n+1 {
		t.Fatalf("the next follow %+v %v, checked %d", u, ok, len(c.checked))
	}
}

func TestInstallPrunesCrashedTempCopies(t *testing.T) {
	rel := newRelease(t)
	home := t.TempDir()
	rel.publish("2.1.312")
	dir := filepath.Dir(BinaryPath(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".claude-crashed")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), home, rel.source(), (&checker{}).check); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover temp copy kept: %v", err)
	}
}

func TestFollowTriesAFailedVersionOncePerDay(t *testing.T) {
	rel := newRelease(t)
	ctx := context.Background()
	home := t.TempDir()
	rel.publish("2.1.312")
	c := &checker{}
	if _, err := Install(ctx, home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.500")
	c.err = errors.New("flaky")
	if _, ok := Follow(ctx, home, rel.source(), c.check); !ok || len(c.checked) != 1 {
		t.Fatalf("first follow, checked %d", len(c.checked))
	}
	// A restarted daemon follows again at once: no second check.
	if _, ok := Follow(ctx, home, rel.source(), c.check); ok || len(c.checked) != 1 {
		t.Fatalf("a failure just recorded was retried: checked %d", len(c.checked))
	}
	ageUpdate(t, home, 19*time.Hour)
	if _, ok := Follow(ctx, home, rel.source(), c.check); ok || len(c.checked) != 1 {
		t.Fatalf("retried after 19h: checked %d", len(c.checked))
	}
	ageUpdate(t, home, 2*time.Hour)
	if u, ok := Follow(ctx, home, rel.source(), c.check); !ok || u.Attempts != 2 || len(c.checked) != 2 {
		t.Fatalf("after 21h: %+v %v, checked %d", u, ok, len(c.checked))
	}
	// A newer version is not held back by the failed one.
	rel.publish("2.1.600")
	c.err = nil
	if u, ok := Follow(ctx, home, rel.source(), c.check); !ok || u.Error != "" || u.Version != "2.1.600" || len(c.checked) != 3 {
		t.Fatalf("newer version: %+v %v, checked %d", u, ok, len(c.checked))
	}
}

func TestInstallWithoutACheckerRefusesAnUnverifiedVersion(t *testing.T) {
	rel := newRelease(t)
	rel.publish("2.1.400")
	home := t.TempDir()
	_, err := Install(context.Background(), home, rel.source(), nil)
	if err == nil || !strings.Contains(err.Error(), "no self-check was given") {
		t.Fatalf("install without a checker: %v", err)
	}
	if _, err := os.Lstat(BinaryPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an unverified version was linked: %v", err)
	}
	// A verified version needs no checker.
	rel.publish("2.1.312")
	if _, err := Install(context.Background(), home, rel.source(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSelfCheckFailureIsMarked(t *testing.T) {
	rel := newRelease(t)
	rel.publish("2.1.400")
	c := &checker{err: errors.New("boom")}
	_, err := Install(context.Background(), t.TempDir(), rel.source(), c.check)
	if !errors.Is(err, ErrSelfCheck) || !errors.Is(err, c.err) {
		t.Fatalf("error %v is not ErrSelfCheck wrapping the cause", err)
	}
}

func TestNoticeForAnUncountedFailure(t *testing.T) {
	u := Update{At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Version: "2.1.500", Error: "claude: disk full"}
	got, attention := notice(u, "2.1.400")
	if !attention || !strings.Contains(got, "could not move to claude 2.1.500 on 2026-11-01: claude: disk full; it will try again") || strings.Contains(got, "attempt") {
		t.Fatalf("notice %q %v", got, attention)
	}
}

func TestFollowRetriesAfterTheClockWentBack(t *testing.T) {
	rel := newRelease(t)
	ctx := context.Background()
	home := t.TempDir()
	rel.publish("2.1.312")
	c := &checker{err: errors.New("flaky")}
	if _, err := Install(ctx, home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.500")
	if _, ok := Follow(ctx, home, rel.source(), c.check); !ok {
		t.Fatal("first follow did nothing")
	}
	ageUpdate(t, home, -48*time.Hour) // recorded "in the future"
	if _, ok := Follow(ctx, home, rel.source(), c.check); !ok || len(c.checked) != 2 {
		t.Fatalf("a failure dated in the future blocked the retry: checked %d", len(c.checked))
	}
}

func TestInstallDoesNotDownloadWhatIsInPlace(t *testing.T) {
	rel := newRelease(t)
	rel.publish("2.1.312")
	home := t.TempDir()
	for range 2 {
		if _, err := Install(context.Background(), home, rel.source(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if rel.gets != 1 {
		t.Fatalf("downloaded %d times", rel.gets)
	}
}

func TestFollowsTheChannelBack(t *testing.T) {
	rel := newRelease(t)
	home := t.TempDir()
	c := &checker{}
	rel.publish("2.1.400")
	if _, err := Install(context.Background(), home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.312")
	if u, ok := Follow(context.Background(), home, rel.source(), c.check); !ok || u.Error != "" || link(t, home) != "claude-2.1.312" {
		t.Fatalf("follow %+v %v, link %s", u, ok, link(t, home))
	}
}

func TestBadDownloadsDoNotSwitch(t *testing.T) {
	tests := []struct {
		name  string
		spoil func(r *release)
		want  string
	}{
		{"checksum", func(r *release) { r.sums["2.1.400"] = strings.Repeat("a", 64) }, "checksum mismatch"},
		{"signature", func(r *release) { r.verify = errors.New("not signed") }, "not signed"},
		{"another version", func(r *release) { r.bodies["2.1.400"] = r.bodies["2.1.312"] }, "reports version"},
		{"no build", func(r *release) { r.stable = "2.1.401"; r.bodies["2.1.401"] = nil }, "2.1.401"},
		{"strange channel", func(r *release) { r.stable = "../evil" }, "unexpected version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel := newRelease(t)
			home := t.TempDir()
			c := &checker{}
			rel.publish("2.1.312")
			if _, err := Install(context.Background(), home, rel.source(), c.check); err != nil {
				t.Fatal(err)
			}
			rel.publish("2.1.400")
			tt.spoil(rel)
			if _, err := Install(context.Background(), home, rel.source(), c.check); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v", err)
			}
			if target := link(t, home); target != "claude-2.1.312" {
				t.Fatalf("link %q", target)
			}
			if len(c.checked) != 0 {
				t.Fatalf("self-checked a bad download: %v", c.checked)
			}
			left, _ := filepath.Glob(filepath.Join(filepath.Dir(BinaryPath(home)), ".claude-*"))
			if len(left) != 0 {
				t.Fatalf("left %q", left)
			}
		})
	}
}

func TestFailedDownloadIsNotCounted(t *testing.T) {
	rel := newRelease(t)
	home := t.TempDir()
	c := &checker{}
	rel.publish("2.1.312")
	if _, err := Install(context.Background(), home, rel.source(), c.check); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.400")
	rel.sums["2.1.400"] = strings.Repeat("a", 64)
	u, ok := Follow(context.Background(), home, rel.source(), c.check)
	if !ok || !strings.Contains(u.Error, "checksum mismatch") || u.Attempts != 0 || len(c.checked) != 0 {
		t.Fatalf("follow %+v %v, checked %v", u, ok, c.checked)
	}
}
