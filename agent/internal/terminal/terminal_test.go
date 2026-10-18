package terminal

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeClock is a Clock that only moves when a test advances it.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.timers = append(c.timers, fakeTimer{c.now.Add(d), ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.timers[:0]
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			t.ch <- c.now
		} else {
			keep = append(keep, t)
		}
	}
	c.timers = keep
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

type closedEvent struct {
	id, reason string
	code       *int
}

// collector records the Events of a Manager.
type collector struct {
	mu     sync.Mutex
	mgr    *Manager
	ack    bool
	out    map[string]*bytes.Buffer
	frames []int
	seqs   []int64
	closed chan closedEvent
}

func newCollector() *collector {
	return &collector{out: map[string]*bytes.Buffer{}, closed: make(chan closedEvent, 16)}
}

func (c *collector) events() Events {
	return Events{
		Output: func(id string, seq int64, data []byte) {
			c.mu.Lock()
			if c.out[id] == nil {
				c.out[id] = &bytes.Buffer{}
			}
			c.out[id].Write(data)
			c.frames = append(c.frames, len(data))
			c.seqs = append(c.seqs, seq)
			ack := c.ack
			c.mu.Unlock()
			if ack {
				go c.mgr.Ack(id, int64(len(data)))
			}
		},
		Closed: func(id, reason string, code *int) { c.closed <- closedEvent{id, reason, code} },
	}
}

func (c *collector) text(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b := c.out[id]; b != nil {
		return b.String()
	}
	return ""
}

func (c *collector) total(id string) int { return len(c.text(id)) }

func (c *collector) waitText(t *testing.T, id, want string) {
	t.Helper()
	waitUntil(t, func() bool { return strings.Contains(c.text(id), want) }, "output containing "+want)
}

func (c *collector) waitClosed(t *testing.T) closedEvent {
	t.Helper()
	select {
	case e := <-c.closed:
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("terminal did not close")
		return closedEvent{}
	}
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newManager(t *testing.T, mutate func(*Config)) (*Manager, *collector) {
	t.Helper()
	col := newCollector()
	cfg := Config{
		Home:       t.TempDir(),
		DeviceID:   "dev-1",
		Shell:      "/bin/sh",
		FlushEvery: time.Millisecond,
		KillGrace:  300 * time.Millisecond,
		Events:     col.events(),
		Log:        func(string, ...any) {},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	m := New(cfg)
	col.mgr = m
	t.Cleanup(func() { m.CloseAll(ReasonClosed) })
	return m, col
}

func open(t *testing.T, m *Manager, id, session string) string {
	t.Helper()
	dir := t.TempDir()
	if err := m.Open(id, session, dir, 80, 24, nil); err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	return dir
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitDead(t *testing.T, pid int) {
	t.Helper()
	waitUntil(t, func() bool { return !alive(pid) }, "process "+strconv.Itoa(pid)+" to die")
}

var childPID = regexp.MustCompile(`CHILD=(\d+)\b`)

func waitChild(t *testing.T, col *collector, id string) int {
	t.Helper()
	var pid int
	waitUntil(t, func() bool {
		// the echoed command line holds "CHILD=$!", only the output holds digits
		if m := childPID.FindStringSubmatch(col.text(id)); m != nil {
			pid, _ = strconv.Atoi(m[1])
			return true
		}
		return false
	}, "the child pid in "+strconv.Quote(col.text(id)))
	return pid
}

func TestShellRunsCommandsInTheSessionDirectory(t *testing.T) {
	m, col := newManager(t, nil)
	dir := open(t, m, "t1", "s1")

	m.Input("t1", []byte("echo hoplet-$((20+22)); pwd\n"))
	col.waitText(t, "t1", "hoplet-42")
	col.waitText(t, "t1", filepath.Base(dir))

	m.Close("t1", ReasonClosed)
	if e := col.waitClosed(t); e.id != "t1" || e.reason != ReasonClosed {
		t.Fatalf("closed = %+v", e)
	}
	if m.Active() != 0 {
		t.Fatalf("active = %d", m.Active())
	}
}

func TestEnvironmentIsRestricted(t *testing.T) {
	t.Setenv("HOPLET_TOKEN", "secret")
	t.Setenv("TERM", "dumb")
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")

	m.Input("t1", []byte("echo \"T=$TERM H=${HOPLET_TOKEN-unset}\"\n"))
	col.waitText(t, "t1", "T=xterm-256color H=unset")
}

func TestShellEnvStripsHopletAndSetsTerm(t *testing.T) {
	got := shellEnv([]string{"A=1", "HOPLET_URL=x", "TERM=dumb", "HOPLET_TOKEN=y"})
	want := []string{"A=1", "TERM=xterm-256color"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("env = %v", got)
	}
}

func TestPickShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	if got := pickShell(""); got != "/bin/sh" {
		t.Fatalf("shell = %q", got)
	}
	t.Setenv("SHELL", "relative/zsh")
	if got := pickShell(""); got != "/bin/sh" {
		t.Fatalf("relative $SHELL gave %q", got)
	}
	t.Setenv("SHELL", "/nonexistent/shell")
	if got := pickShell(""); got != "/bin/sh" {
		t.Fatalf("missing $SHELL gave %q", got)
	}
	if got := pickShell("/custom"); got != "/custom" {
		t.Fatalf("configured shell gave %q", got)
	}
}

func TestOpenRefusesWhatIsNotADirectory(t *testing.T) {
	m, _ := newManager(t, nil)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{file, filepath.Join(t.TempDir(), "missing")} {
		var oe *OpenError
		if err := m.Open("t1", "s1", dir, 80, 24, nil); !errors.As(err, &oe) || oe.Reason != ReasonNotADirectory {
			t.Fatalf("open %s: %v", dir, err)
		}
	}
	if m.Active() != 0 {
		t.Fatalf("active = %d", m.Active())
	}
}

func TestOpenRefusesInvalidIDs(t *testing.T) {
	m, _ := newManager(t, nil)
	if err := m.Open("../etc/passwd", "s1", t.TempDir(), 80, 24, nil); err == nil {
		t.Fatal("an ID that names a path was accepted")
	}
}

func TestGuardRefusesBeforeAnythingStarts(t *testing.T) {
	m, _ := newManager(t, nil)
	guard := func() error { return &OpenError{ReasonTurnRunning} }
	var oe *OpenError
	if err := m.Open("t1", "s1", t.TempDir(), 80, 24, guard); !errors.As(err, &oe) || oe.Reason != ReasonTurnRunning {
		t.Fatalf("open: %v", err)
	}
	if m.Active() != 0 {
		t.Fatalf("active = %d", m.Active())
	}
	if entries, _ := os.ReadDir(journalDir(m.cfg.Home)); len(entries) != 0 {
		t.Fatalf("journal = %v", entries)
	}
}

func TestACloseDuringOpenAbandonsIt(t *testing.T) {
	m, _ := newManager(t, nil)
	guard := func() error {
		m.Close("t1", ReasonTurnStarted) // a turn starts while the terminal opens
		return nil
	}
	var oe *OpenError
	if err := m.Open("t1", "s1", t.TempDir(), 80, 24, guard); !errors.As(err, &oe) || oe.Reason != ReasonTurnStarted {
		t.Fatalf("open: %v", err)
	}
	if m.Active() != 0 {
		t.Fatalf("active = %d", m.Active())
	}
}

func TestLimitsOnePerSessionAndThreePerMachine(t *testing.T) {
	m, _ := newManager(t, nil)
	open(t, m, "t1", "s1")
	var oe *OpenError
	if err := m.Open("t1b", "s1", t.TempDir(), 80, 24, nil); !errors.As(err, &oe) || oe.Reason != ReasonBusy {
		t.Fatalf("second terminal for a session: %v", err)
	}
	open(t, m, "t2", "s2")
	open(t, m, "t3", "s3")
	if err := m.Open("t4", "s4", t.TempDir(), 80, 24, nil); !errors.As(err, &oe) || oe.Reason != ReasonBusy {
		t.Fatalf("fourth terminal: %v", err)
	}
	if m.Active() != 3 {
		t.Fatalf("active = %d", m.Active())
	}
}

func TestResize(t *testing.T) {
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")
	m.Resize("t1", 100, 30)
	m.Resize("t1", 9999, 30) // out of range: ignored
	m.Input("t1", []byte("stty size\n"))
	col.waitText(t, "t1", "30 100")
}

func TestShellExitReportsTheExitCode(t *testing.T) {
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")
	m.Input("t1", []byte("echo bye; exit 3\n"))
	e := col.waitClosed(t)
	if e.reason != ReasonExit || e.code == nil || *e.code != 3 {
		t.Fatalf("closed = %+v", e)
	}
	if !strings.Contains(col.text("t1"), "bye") {
		t.Fatalf("the last output was lost: %q", col.text("t1"))
	}
}

func TestOutputIsFramedAndBackPressured(t *testing.T) {
	const window = 8192
	m, col := newManager(t, func(c *Config) { c.Window = window })
	open(t, m, "t1", "s1")

	m.Input("t1", []byte("yes hoplet\n"))
	waitUntil(t, func() bool { return col.total("t1") >= window }, "the window to fill")
	time.Sleep(300 * time.Millisecond)
	if got := col.total("t1"); got > window {
		t.Fatalf("%d bytes sent without an ack, window is %d", got, window)
	}

	// Acks resume the flow.
	col.mu.Lock()
	col.ack = true
	col.mu.Unlock()
	m.Ack("t1", int64(col.total("t1")))
	waitUntil(t, func() bool { return col.total("t1") > 4*window }, "output to resume after the ack")

	col.mu.Lock()
	defer col.mu.Unlock()
	for i, n := range col.frames {
		if n == 0 || n > ChunkBytes {
			t.Fatalf("frame %d has %d bytes", i, n)
		}
		if col.seqs[i] != int64(i) {
			t.Fatalf("frame %d has seq %d", i, col.seqs[i])
		}
	}
}

func TestInputFloodClosesTheTerminal(t *testing.T) {
	clock := newFakeClock()
	m, col := newManager(t, func(c *Config) { c.Clock = clock })
	open(t, m, "t1", "s1")

	chunk := append([]byte("#"), append(bytes.Repeat([]byte("x"), ChunkBytes-2), '\n')...)
	for i := 0; i < InputRate/ChunkBytes; i++ {
		m.Input("t1", chunk) // exactly the budget of one second
	}
	select {
	case e := <-col.closed:
		t.Fatalf("closed too early: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
	m.Input("t1", chunk)
	if e := col.waitClosed(t); e.reason != ReasonInputFlood {
		t.Fatalf("closed = %+v", e)
	}
}

func TestOversizedInputClosesTheTerminal(t *testing.T) {
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")
	m.Input("t1", make([]byte, ChunkBytes+1))
	if e := col.waitClosed(t); e.reason != ReasonInputFlood {
		t.Fatalf("closed = %+v", e)
	}
}

func TestInputBudgetRefills(t *testing.T) {
	clock := newFakeClock()
	m, col := newManager(t, func(c *Config) { c.Clock = clock })
	col.mu.Lock()
	col.ack = true // the echo of the input must not stall the shell
	col.mu.Unlock()
	open(t, m, "t1", "s1")
	chunk := append([]byte("#"), append(bytes.Repeat([]byte("x"), ChunkBytes-2), '\n')...)
	tm := m.lookup("t1")
	for round := 0; round < 3; round++ {
		for i := 0; i < InputRate/ChunkBytes; i++ {
			m.Input("t1", chunk)
		}
		waitUntil(t, func() bool { return len(tm.in) == 0 }, "the shell to take the input")
		clock.Advance(time.Second)
	}
	select {
	case e := <-col.closed:
		t.Fatalf("closed: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestIdleClose(t *testing.T) {
	clock := newFakeClock()
	m, col := newManager(t, func(c *Config) { c.Clock = clock })
	open(t, m, "t1", "s1")

	waitUntil(t, func() bool { return clock.pending() == 2 }, "the idle and age timers")
	clock.Advance(59 * time.Minute)
	m.Input("t1", []byte("\n")) // activity resets the idle time
	clock.Advance(time.Minute)  // the first idle timer fires and finds activity
	waitUntil(t, func() bool { return clock.pending() == 2 }, "the idle timer to be re-armed")
	select {
	case e := <-col.closed:
		t.Fatalf("closed despite input: %+v", e)
	default:
	}
	clock.Advance(59 * time.Minute)
	if e := col.waitClosed(t); e.reason != ReasonIdle {
		t.Fatalf("closed = %+v", e)
	}
}

func TestMaxAgeClose(t *testing.T) {
	clock := newFakeClock()
	m, col := newManager(t, func(c *Config) { c.Clock = clock })
	open(t, m, "t1", "s1")

	for step := 0; step < 16; step++ {
		waitUntil(t, func() bool { return clock.pending() == 2 }, "the timers")
		m.Input("t1", []byte("\n")) // never idle
		clock.Advance(30 * time.Minute)
	}
	if e := col.waitClosed(t); e.reason != ReasonMaxAge {
		t.Fatalf("closed = %+v", e)
	}
}

func TestCloseKillsTheWholeProcessTree(t *testing.T) {
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")

	m.Input("t1", []byte("sleep 300 & echo CHILD=$!\n"))
	child := waitChild(t, col, "t1")
	if !alive(child) {
		t.Fatal("the background job is not running")
	}
	m.Close("t1", ReasonClosed)
	col.waitClosed(t)
	waitDead(t, child)
}

func TestCloseKillsDescendantsThatLeftTheGroup(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is needed to start a process in its own session")
	}
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")

	m.Input("t1", []byte("perl -MPOSIX -e 'POSIX::setsid(); exec q(sleep), q(301)' & echo CHILD=$!\n"))
	child := waitChild(t, col, "t1")
	waitUntil(t, func() bool { return alive(child) }, "the detached job")
	time.Sleep(200 * time.Millisecond) // let perl exec sleep

	m.Close("t1", ReasonClosed)
	col.waitClosed(t)
	waitDead(t, child)
}

func TestCloseEscalatesToSIGKILL(t *testing.T) {
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")

	m.Input("t1", []byte("trap '' HUP; sleep 300 & echo CHILD=$! ; wait\n"))
	child := waitChild(t, col, "t1")

	started := time.Now()
	m.Close("t1", ReasonClosed)
	col.waitClosed(t)
	if time.Since(started) < 250*time.Millisecond {
		t.Fatalf("closed in %v, before the grace period", time.Since(started))
	}
	waitDead(t, child)
}

func TestCloseAllAndCloseSessionWaitForTheTerminals(t *testing.T) {
	m, col := newManager(t, nil)
	open(t, m, "t1", "s1")
	open(t, m, "t2", "s2")

	m.CloseSession("s1", ReasonDeleted)
	if m.Active() != 1 {
		t.Fatalf("active = %d", m.Active())
	}
	if e := col.waitClosed(t); e.id != "t1" || e.reason != ReasonDeleted {
		t.Fatalf("closed = %+v", e)
	}
	m.CloseAll(ReasonOwnerOffline)
	if m.Active() != 0 {
		t.Fatalf("active = %d", m.Active())
	}
	if e := col.waitClosed(t); e.id != "t2" || e.reason != ReasonOwnerOffline {
		t.Fatalf("closed = %+v", e)
	}
}

func TestJournalHoldsARecordWhileOpen(t *testing.T) {
	m, col := newManager(t, nil)
	dir := open(t, m, "t1", "s1")
	path := filepath.Join(journalDir(m.cfg.Home), "t1.json")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"terminalId":"t1"`, `"pid":`, `"start":`, `"cwd":` + strconv.Quote(dir)} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("record %s lacks %s", raw, want)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", info.Mode().Perm())
	}
	m.Input("t1", []byte("echo typed-secret\n"))
	col.waitText(t, "t1", "typed-secret")
	if strings.Contains(string(raw), "typed-secret") {
		t.Fatal("the record holds content")
	}

	m.Close("t1", ReasonClosed)
	col.waitClosed(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record outlived the terminal: %v", err)
	}
}

func TestSweepKillsRecordedProcessesAndDeletesTheRecords(t *testing.T) {
	home := t.TempDir()
	live := exec.Command("sleep", "300")
	live.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	liveDone := make(chan struct{})
	go func() { _ = live.Wait(); close(liveDone) }()

	recycled := exec.Command("sleep", "300")
	if err := recycled.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recycled.Process.Kill(); _ = recycled.Wait() })

	start, ok := startTime(live.Process.Pid)
	if !ok {
		t.Fatal("no start time")
	}
	if err := writeRecord(home, record{TerminalID: "live", PID: live.Process.Pid, Start: start, Cwd: "/"}); err != nil {
		t.Fatal(err)
	}
	// a record whose PID now belongs to another process must not kill it
	if err := writeRecord(home, record{TerminalID: "recycled", PID: recycled.Process.Pid, Start: "an older start", Cwd: "/"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir(home), "half.json.tmp"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if n := Sweep(home); n != 1 {
		t.Fatalf("swept %d, want 1", n)
	}
	select {
	case <-liveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorded process is still running")
	}
	if !alive(recycled.Process.Pid) {
		t.Fatal("a process with another start time was killed")
	}
	if entries, _ := os.ReadDir(journalDir(home)); len(entries) != 0 {
		t.Fatalf("records left: %v", entries)
	}
	if n := Sweep(t.TempDir()); n != 0 {
		t.Fatalf("an empty home swept %d", n)
	}
}
