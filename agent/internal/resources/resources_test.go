package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }

const procStat1 = "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3\n"
const procStat2 = "cpu  200 0 200 1400 200 0 0 0 0 0\n"

func TestParseCPUStat(t *testing.T) {
	busy, total, ok := parseCPUStat(procStat1)
	if !ok || busy != 200 || total != 1000 {
		t.Fatalf("busy=%d total=%d ok=%v", busy, total, ok)
	}
	if _, _, ok := parseCPUStat("nonsense"); ok {
		t.Fatal("accepted nonsense")
	}
}

func TestParseMeminfo(t *testing.T) {
	total, avail, ok := parseMeminfo("MemTotal:       16000 kB\nMemFree: 1 kB\nMemAvailable:    4000 kB\n")
	if !ok || total != 16000*1024 || avail != 4000*1024 {
		t.Fatalf("%d %d %v", total, avail, ok)
	}
	if _, _, ok := parseMeminfo("MemTotal: 1 kB\n"); ok {
		t.Fatal("accepted without MemAvailable")
	}
}

func TestParsePidStatHandlesParenthesesInTheName(t *testing.T) {
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime
	// priority nice threads itrealvalue starttime vsize rss
	line := "42 (a) b (c)) S 7 1 1 0 -1 0 0 0 0 0 30 12 0 0 20 0 1 0 100 5000 250 18446744073709551615"
	ppid, ticks, rss, ok := parsePidStat(line)
	if !ok || ppid != 7 || ticks != 42 || rss != 250 {
		t.Fatalf("%d %d %d %v", ppid, ticks, rss, ok)
	}
	if _, _, _, ok := parsePidStat("1 (x) S 0"); ok {
		t.Fatal("accepted a short line")
	}
}

func TestParsePS(t *testing.T) {
	got := parsePS("    1     0  1024   0.5\n  200     1 2048 12.0\nbad line\n")
	if len(got) != 2 || got[1].PID != 200 || got[1].RSS != 2048*1024 || got[1].PCPU != 12.0 || got[0].PPID != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestParseVMStat(t *testing.T) {
	const vm = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               1000.
Pages active:                             5000.
Pages inactive:                           2000.
Pages speculative:                         500.
Pages wired down:                         3000.
`
	n, ok := parseVMStat(vm)
	if !ok || n != 3500*16384 {
		t.Fatalf("%d %v", n, ok)
	}
	if _, ok := parseVMStat("Pages free: 1.\n"); ok {
		t.Fatal("accepted without page size")
	}
}

func TestSessionsSumTheSubtreeAndCountChildren(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 0, RSS: 1000, CPU: fp(50)},
		{PID: 10, PPID: 1, RSS: 100, CPU: fp(1)},
		{PID: 11, PPID: 10, RSS: 10, CPU: fp(2)},
		{PID: 20, PPID: 1, RSS: 500, CPU: fp(3)}, // the shell
		{PID: 21, PPID: 20, RSS: 5, CPU: nil},
	}
	got := Sessions(procs, map[string][]int{
		"a":    {10, 20, 0},
		"b":    {99},
		"c":    {11, 11},
		"gone": {},
	})
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	a := got[0]
	if a.SessionID != "a" || a.RSS != 615 || a.Children != 2 || a.CPU == nil || *a.CPU != 6 {
		t.Fatalf("a = %+v", a)
	}
	if c := got[1]; c.SessionID != "c" || c.RSS != 10 || c.Children != 0 {
		t.Fatalf("c = %+v", c)
	}
}

func TestSessionsAreCappedAndSortedByMemory(t *testing.T) {
	var procs []Proc
	roots := map[string][]int{}
	for i := 1; i <= MaxSessions+5; i++ {
		procs = append(procs, Proc{PID: i, RSS: uint64(i)})
		roots["s"+strconv.Itoa(i)] = []int{i}
	}
	got := Sessions(procs, roots)
	if len(got) != MaxSessions || got[0].RSS != MaxSessions+5 || got[1].RSS != MaxSessions+4 {
		t.Fatalf("len=%d first=%d", len(got), got[0].RSS)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProcReaderComputesCPUFromDifferences(t *testing.T) {
	root := t.TempDir()
	pid := func(ticks int) string {
		return "5 (x) S 1 1 1 0 -1 0 0 0 0 0 " + itoa(ticks) + " 0 0 0 20 0 1 0 100 5000 10 0"
	}
	write(t, filepath.Join(root, "stat"), procStat1)
	write(t, filepath.Join(root, "meminfo"), "MemTotal: 1000 kB\nMemAvailable: 250 kB\n")
	write(t, filepath.Join(root, "5", "stat"), pid(0))
	write(t, filepath.Join(root, "notapid", "stat"), "x")
	r := newProcReader(root)
	r.pageSize = 4096

	first, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.CPU != nil || first.Procs[0].CPU != nil {
		t.Fatalf("first reading has CPU: %+v", first)
	}
	if first.MemTotal != 1000*1024 || first.MemUsed != 750*1024 || first.Procs[0].RSS != 10*4096 || first.Procs[0].PPID != 1 {
		t.Fatalf("%+v", first)
	}

	write(t, filepath.Join(root, "stat"), procStat2) // +200 busy of +1000 total
	write(t, filepath.Join(root, "5", "stat"), pid(100))
	second, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.CPU == nil || *second.CPU != 20 {
		t.Fatalf("machine cpu = %v", second.CPU)
	}
	if p := second.Procs[0]; p.CPU == nil || *p.CPU != 10 {
		t.Fatalf("process cpu = %v", p.CPU)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

type fakeReader struct {
	r   Reading
	err error
}

func (f *fakeReader) Read(context.Context) (Reading, error) { return f.r, f.err }

func TestSamplerSendsOneSampleWithSessionsAndDisk(t *testing.T) {
	now := time.UnixMilli(5000)
	var got []Sample
	s := New(Config{
		Reader:   &fakeReader{r: Reading{CPU: fp(7), MemUsed: 1, MemTotal: 2, Procs: []Proc{{PID: 3, PPID: 1, RSS: 9}}}},
		DiskPath: "/x",
		Disk: func(p string) (uint64, uint64, error) {
			if p != "/x" {
				t.Fatalf("disk path %q", p)
			}
			return 40, 100, nil
		},
		Roots: func() map[string][]int { return map[string][]int{"s1": {3}} },
		Send:  func(s Sample) { got = append(got, s) },
		Now:   func() time.Time { return now },
		Logf:  func(string, ...any) {},
	})
	s.Tick(context.Background())
	if len(got) != 1 {
		t.Fatalf("sent %d", len(got))
	}
	x := got[0]
	if x.At != now || *x.CPU != 7 || x.DiskFree != 40 || x.DiskTotal != 100 || len(x.Sessions) != 1 || x.Sessions[0].RSS != 9 {
		t.Fatalf("%+v", x)
	}
}

func TestSamplerSendsNothingWhenTheReadFailsAndLogsOnce(t *testing.T) {
	var logs int
	sent := 0
	s := New(Config{
		Reader: &fakeReader{err: errors.New("boom")},
		Send:   func(Sample) { sent++ },
		Logf:   func(string, ...any) { logs++ },
	})
	s.Tick(context.Background())
	s.Tick(context.Background())
	if sent != 0 || logs != 1 {
		t.Fatalf("sent=%d logs=%d", sent, logs)
	}
}

func TestSamplerRunTicksUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	s := New(Config{
		Reader:   &fakeReader{},
		Interval: time.Millisecond,
		Disk:     func(string) (uint64, uint64, error) { return 0, 0, nil },
		Send: func(Sample) {
			if n++; n == 3 {
				cancel()
			}
		},
	})
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
