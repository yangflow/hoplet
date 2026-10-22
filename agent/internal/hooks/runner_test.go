package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type collector struct{ ch chan Result }

func newCollector() *collector { return &collector{ch: make(chan Result, 64)} }

func (c *collector) report(r Result) { c.ch <- r }

func (c *collector) next(t *testing.T) Result {
	t.Helper()
	select {
	case r := <-c.ch:
		return r
	case <-time.After(15 * time.Second):
		t.Fatal("no result")
		return Result{}
	}
}

func event(id, typ, path string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"v": 1, "id": id, "type": typ, "at": "2026-10-22T00:00:00Z",
		"session": map[string]any{"id": "s1", "project": map[string]any{"name": "p", "path": path}},
	})
	return raw
}

func start(t *testing.T, body string, mutate func(*Entry)) (*Runner, *collector, string) {
	t.Helper()
	home := t.TempDir()
	e := Entry{ID: "h", Command: script(t, 0o700, body), Events: []string{"*"}, TimeoutMS: 10000}
	if mutate != nil {
		mutate(&e)
	}
	if err := Validate([]Entry{e}); err != nil {
		t.Fatal(err)
	}
	c := newCollector()
	r := New([]Entry{e}, Options{Home: home, Report: c.report})
	t.Cleanup(r.Close)
	return r, c, home
}

func TestRunPassesTheEventOnStdinAndACleanEnvironment(t *testing.T) {
	t.Setenv("SECRET_FROM_AGENT", "no")
	t.Setenv("HOPLET_TOKEN", "no")
	t.Setenv("MY_VAR", "yes")
	out := filepath.Join(t.TempDir(), "out")
	body := `cat > "` + out + `.in"; env | sort > "` + out + `.env"; echo "$#" > "` + out + `.argc"; pwd -P > "` + out + `.pwd"`
	r, c, home := start(t, body, func(e *Entry) { e.Env = []string{"MY_VAR", "NOT_SET"} })
	ev := event("evt_1", "turn.completed", "")
	r.Handle("h", ev)
	res := c.next(t)
	if !res.OK || res.ExitCode == nil || *res.ExitCode != 0 || res.EventID != "evt_1" {
		t.Fatalf("result = %+v", res)
	}
	in, _ := os.ReadFile(out + ".in")
	if string(in) != string(ev) {
		t.Fatalf("stdin = %q", in)
	}
	env, _ := os.ReadFile(out + ".env")
	for _, want := range []string{"HOPLET_HOOK_ID=h", "HOPLET_EVENT_ID=evt_1", "HOPLET_EVENT_TYPE=turn.completed", "MY_VAR=yes"} {
		if !strings.Contains(string(env), want+"\n") {
			t.Errorf("environment lacks %s:\n%s", want, env)
		}
	}
	for _, name := range strings.Split(strings.TrimSpace(string(env)), "\n") {
		key, _, _ := strings.Cut(name, "=")
		switch key {
		case "PATH", "HOME", "USER", "LANG", "TZ", "TMPDIR", "MY_VAR", "HOPLET_HOOK_ID", "HOPLET_EVENT_ID", "HOPLET_EVENT_TYPE", "PWD", "SHLVL", "_", "OLDPWD":
		default:
			t.Errorf("unexpected variable %s", key)
		}
	}
	if argc, _ := os.ReadFile(out + ".argc"); strings.TrimSpace(string(argc)) != "0" {
		t.Errorf("argc = %q", argc)
	}
	want, _ := filepath.EvalSymlinks(home)
	if pwd, _ := os.ReadFile(out + ".pwd"); strings.TrimSpace(string(pwd)) != want {
		t.Errorf("cwd = %q, want %q", pwd, want)
	}
}

func TestRunUsesTheWatchedProjectDirectory(t *testing.T) {
	project, _ := filepath.EvalSymlinks(t.TempDir())
	out := filepath.Join(t.TempDir(), "pwd")
	home := t.TempDir()
	e := Entry{ID: "h", Command: script(t, 0o700, `pwd -P > "`+out+`"`), Events: []string{"*"}, TimeoutMS: 10000}
	c := newCollector()
	r := New([]Entry{e}, Options{Home: home, Paths: func() []string { return []string{project} }, Report: c.report})
	defer r.Close()
	r.Handle("h", event("evt_1", "session.ended", project))
	c.next(t)
	if pwd, _ := os.ReadFile(out); strings.TrimSpace(string(pwd)) != project {
		t.Fatalf("cwd = %q", pwd)
	}
	// A path the machine does not watch is not a place to run in.
	r.Handle("h", event("evt_2", "session.ended", "/"))
	c.next(t)
	if pwd, _ := os.ReadFile(out); strings.TrimSpace(string(pwd)) == "/" {
		t.Fatal("ran in an unwatched path")
	}
}

func TestRunReportsExitCodes(t *testing.T) {
	r, c, _ := start(t, "echo oops >&2; exit 3", nil)
	r.Handle("h", event("evt_1", "session.ended", ""))
	res := c.next(t)
	if res.OK || res.ExitCode == nil || *res.ExitCode != 3 || res.Stderr != "oops\n" || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	if got := r.Info()[0]; got.Runs != 1 || got.Failed != 1 {
		t.Fatalf("counters = %+v", got)
	}
}

func TestRunKillsAScriptThatOverrunsItsTimeout(t *testing.T) {
	r, c, home := start(t, "sleep 30 & sleep 30", func(e *Entry) { e.TimeoutMS = 200 })
	began := time.Now()
	r.Handle("h", event("evt_1", "session.ended", ""))
	res := c.next(t)
	if res.OK || res.Error != ErrTimeout || res.ExitCode != nil {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(began) > 8*time.Second {
		t.Fatalf("took %s", time.Since(began))
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "hooks")); len(entries) != 0 {
		t.Fatalf("journal not cleaned: %v", entries)
	}
}

func TestRunEscalatesToKillWhenSigtermIsIgnored(t *testing.T) {
	r, c, _ := start(t, `trap '' TERM; while :; do sleep 1; done`, func(e *Entry) { e.TimeoutMS = 200 })
	r.Handle("h", event("evt_1", "session.ended", ""))
	if res := c.next(t); res.Error != ErrTimeout {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunCapsStderrAtACharacterBoundary(t *testing.T) {
	body := `i=0; while [ $i -lt 600 ]; do printf 'é'; i=$((i+1)); done >&2`
	r, c, _ := start(t, body, nil)
	r.Handle("h", event("evt_1", "session.ended", ""))
	res := c.next(t)
	if len(res.Stderr) > StderrBytes || len(res.Stderr) < StderrBytes-1 || strings.ContainsRune(res.Stderr, '\uFFFD') {
		t.Fatalf("stderr is %d bytes", len(res.Stderr))
	}
}

func TestRunDoesNotBlockOnLargeOutput(t *testing.T) {
	r, c, _ := start(t, "head -c 3000000 /dev/zero; head -c 300000 /dev/zero >&2", nil)
	r.Handle("h", event("evt_1", "session.ended", ""))
	res := c.next(t)
	if !res.OK || len(res.Stderr) != StderrBytes {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunReportsASpawnFailure(t *testing.T) {
	r, c, _ := start(t, "", nil)
	if err := os.Remove(r.hooks["h"].entry.Command); err != nil {
		t.Fatal(err)
	}
	r.Handle("h", event("evt_1", "session.ended", ""))
	if res := c.next(t); res.OK || res.Error != ErrSpawnFailed {
		t.Fatalf("result = %+v", res)
	}
}

func TestHandleChecksTheHookTheTypeAndTheProject(t *testing.T) {
	r, c, _ := start(t, "exit 0", func(e *Entry) {
		e.Events = []string{"turn.completed"}
		e.Projects = []string{"/work/a"}
	})
	cases := []struct {
		name, hook string
		ev         json.RawMessage
		dropped    bool
	}{
		{"another hook", "other", event("evt_1", "turn.completed", "/work/a"), true},
		{"another type", "h", event("evt_2", "session.ended", "/work/a"), true},
		{"another project", "h", event("evt_3", "turn.completed", "/work/b"), true},
		{"a sibling prefix", "h", event("evt_4", "turn.completed", "/work/ab"), true},
		{"no project", "h", event("evt_5", "turn.completed", ""), true},
		{"the project", "h", event("evt_6", "turn.completed", "/work/a"), false},
		{"below the project", "h", event("evt_7", "turn.completed", "/work/a/sub"), false},
		{"the test event", "h", event("evt_8", "hook.test", ""), false},
	}
	for _, tc := range cases {
		r.Handle(tc.hook, tc.ev)
		res := c.next(t)
		if (res.Error == ErrDropped) != tc.dropped {
			t.Errorf("%s: result = %+v", tc.name, res)
		}
	}
	if got := r.Info()[0]; got.Runs != 3 {
		t.Fatalf("runs = %d", got.Runs)
	}
}

func TestQueueDropsBeyondSixteenAndCounts(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	r, c, _ := start(t, `while [ ! -e "`+gate+`" ]; do sleep 0.05; done`, nil)
	id := func(i int) string { return "evt_" + string(rune('A'+i)) }
	r.Handle("h", event(id(0), "session.ended", ""))
	time.Sleep(500 * time.Millisecond) // the first event is running; sixteen more can wait
	for i := 1; i <= QueueSize+3; i++ {
		r.Handle("h", event(id(i), "session.ended", ""))
	}
	dropped := 0
	for i := 0; i < 3; i++ {
		if res := c.next(t); res.Error == ErrDropped {
			dropped++
		}
	}
	if dropped != 3 || r.Info()[0].Dropped != 3 {
		t.Fatalf("dropped = %d, counter = %d", dropped, r.Info()[0].Dropped)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < QueueSize+1; i++ {
		if res := c.next(t); !res.OK {
			t.Fatalf("result = %+v", res)
		}
	}
}

func TestOneRunAtATime(t *testing.T) {
	dir := t.TempDir()
	body := `echo start >> "` + dir + `/log"; sleep 0.2; echo end >> "` + dir + `/log"`
	r, c, _ := start(t, body, nil)
	r.Handle("h", event("evt_1", "session.ended", ""))
	r.Handle("h", event("evt_2", "session.ended", ""))
	c.next(t)
	c.next(t)
	log, _ := os.ReadFile(dir + "/log")
	if string(log) != "start\nend\nstart\nend\n" {
		t.Fatalf("runs overlapped: %q", log)
	}
}

func TestSweepKillsWhatARecordNamesOnlyWhileItsStartMatches(t *testing.T) {
	home := t.TempDir()
	if err := writeRecord(home, record{RunID: "r1", HookID: "h", PID: 1 << 30, Start: "never"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir(home), "half.json.tmp"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := Sweep(home); n != 0 {
		t.Fatalf("killed %d", n)
	}
	if entries, _ := os.ReadDir(journalDir(home)); len(entries) != 0 {
		t.Fatalf("records left: %v", entries)
	}
}
