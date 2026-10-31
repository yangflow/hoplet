package logfile

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRotatesAndKeepsN(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "d.log")
	l, err := Open(p, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n", "eeee\n"} {
		if _, err := l.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "eeee\n" {
		t.Errorf("current %q", got)
	}
	if got := read(t, p+".1"); got != "cccc\ndddd\n" {
		t.Errorf(".1 %q", got)
	}
	if got := read(t, p+".2"); got != "aaaa\nbbbb\n" {
		t.Errorf(".2 %q", got)
	}
	if _, err := os.Stat(p + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".3 exists: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode().Perm(), err)
	}
	if _, err := l.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestReopenAppendsAndCountsExistingSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.log")
	if err := os.WriteFile(p, []byte("12345678\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(p, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Write([]byte("next\n")); err != nil {
		t.Fatal(err)
	}
	if read(t, p+".1") != "12345678\n" || read(t, p) != "next\n" {
		t.Fatalf("did not rotate a nearly full file: %q / %q", read(t, p+".1"), read(t, p))
	}
}

func TestOversizedWriteGoesWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.log")
	l, err := Open(p, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	big := strings.Repeat("z", 20)
	if _, err := l.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	if read(t, p) != big {
		t.Fatal("an oversized write was split or dropped")
	}
}

func TestConcurrentWritesStayWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.log")
	l, err := Open(p, 200, 50)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if _, err := l.Write([]byte("0123456789\n")); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Every line, across the current file and all kept copies, is whole,
	// and none is lost (50 copies are more than 400 lines need).
	lines := 0
	for i := 0; i <= 50; i++ {
		f := p
		if i > 0 {
			f = p + "." + strconv.Itoa(i)
		}
		body := read(t, f)
		if body == "" {
			continue
		}
		for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			if line != "0123456789" {
				t.Fatalf("torn line %q in %s", line, f)
			}
			lines++
		}
	}
	if lines != 400 {
		t.Fatalf("%d lines, want 400", lines)
	}
}

func TestRotationRecoversOnceTheBlockerIsGone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.log")
	l, err := Open(p, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Write([]byte("aaaaaaaa\n")); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(p+".1", "x")
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("bbbb\n")); err == nil {
		t.Fatal("the failed rotation was not reported")
	}
	if err := os.RemoveAll(p + ".1"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("cccc\n")); err != nil {
		t.Fatalf("write after recovery: %v", err)
	}
	if got := read(t, p); got != "cccc\n" {
		t.Fatalf("current %q", got)
	}
	if got := read(t, p+".1"); got != "aaaaaaaa\nbbbb\n" {
		t.Fatalf(".1 %q", got)
	}
}

func TestPersistentRotationFailureStillLogs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.log")
	l, err := Open(p, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Write([]byte("aaaaaaaa\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p+".1", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"first long line\n", "second long line\n"} {
		n, err := l.Write([]byte(line))
		if err == nil || n != len(line) {
			t.Fatalf("write %q: n=%d err=%v, want the line written and the rotation error", line, n, err)
		}
	}
	if got := read(t, p); got != "aaaaaaaa\nfirst long line\nsecond long line\n" {
		t.Fatalf("file %q", got)
	}
}

func TestRotationFailureIsReportedOnceAndBacksOff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.log")
	l, err := Open(p, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var report strings.Builder
	l.report = &report
	if _, err := l.Write([]byte(strings.Repeat("a", 90))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p+".1", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte(strings.Repeat("b", 20))); err == nil { // size 110: fails, retry at 120
		t.Fatal("the failed rotation was not reported to the caller")
	}
	for range 5 { // sizes 112..130 reach 120 once; each failure returns an error
		_, _ = l.Write([]byte("cc"))
	}
	if n := strings.Count(report.String(), "\n"); n != 1 || !strings.Contains(report.String(), "rename") {
		t.Fatalf("report %q, want one line", report.String())
	}
	// Backoff: with the blocker gone, a write below the retry size does
	// not rotate, one at it does.
	if err := os.RemoveAll(p + ".1"); err != nil {
		t.Fatal(err)
	}
	size := l.size
	l.retryAt = size + 4
	if _, err := l.Write([]byte("dd")); err != nil || read(t, p+".1") != "" {
		t.Fatalf("rotated before the retry size: %v", err)
	}
	if _, err := l.Write([]byte("ee")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("f")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "f" {
		t.Fatalf("current %q after the retry", got)
	}
	// A later streak is reported again.
	if l.reported {
		t.Fatal("the success did not end the streak")
	}
}
