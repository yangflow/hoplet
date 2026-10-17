package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCloneableOrigin(t *testing.T) {
	good := []string{
		"https://github.com/org/repo.git",
		"https://host:8443/org/repo",
		"ssh://git@host/org/repo.git",
		"ssh://git@host:2222/org/repo",
		"git@github.com:org/repo.git",
		"host:org/repo",
	}
	for _, o := range good {
		if _, ok := CloneableOrigin(o); !ok {
			t.Errorf("%q should be cloneable", o)
		}
	}
	bad := []string{
		"",
		"https://user:secret@host/org/repo.git",
		"https://token@host/org/repo.git",
		"ssh://user:secret@host/org/repo",
		"https://host/org/repo?token=abc",
		"https://host/org/repo#frag",
		"http://host/org/repo",
		"git://host/org/repo",
		"file:///tmp/repo",
		"ftp://host/repo",
		"/tmp/repo",
		"../repo",
		"ext::sh -c touch /tmp/x",
		"-oProxyCommand=evil",
		"--upload-pack=evil",
		"https://host/a b",
		"https://host/a\nb",
		"@host:repo",
		"git@:repo",
	}
	for _, o := range bad {
		if _, ok := CloneableOrigin(o); ok {
			t.Errorf("%q should not be cloneable", o)
		}
	}
}

func TestRepoName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/repo.git": "repo",
		"git@github.com:org/My_Repo.git":  "My_Repo",
		"ssh://git@host/org/a.b-c/":       "a.b-c",
		"https://host/":                   "repo",
		"https://host/org/..":             "repo",
	}
	for in, want := range cases {
		if got := RepoName(in); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripSecrets(t *testing.T) {
	cases := map[string]string{
		"fatal: unable to access 'https://user:s3cret@host/o/r.git/': 403": "fatal: unable to access 'https://host/o/r.git/': 403",
		"remote: url https://ghp_abc@host/o/r":                             "remote: url https://host/o/r",
		"GET /x?token=abc123&y=1":                                          "GET /x?token=[removed]",
		"password=hunter2 more":                                            "password=[removed]",
		"Authorization: Basic Zm9vOmJhcg==":                                "Authorization: [removed]",
		"authorization: Bearer abc.def":                                    "authorization: [removed]",
		"sent Bearer xyz now":                                              "sent Bearer [removed]",
		"git@github.com:org/repo.git: Permission denied":                   "git@github.com:org/repo.git: Permission denied",
	}
	for in, want := range cases {
		got := StripSecrets(in)
		if got != want {
			t.Errorf("StripSecrets(%q) = %q, want %q", in, got, want)
		}
		for _, secret := range []string{"s3cret", "ghp_abc", "abc123", "hunter2", "Zm9vOmJhcg", "abc.def", "xyz"} {
			if strings.Contains(got, secret) {
				t.Errorf("StripSecrets(%q) kept %q", in, secret)
			}
		}
	}
	long := strings.Repeat("é", 3000)
	got := StripSecrets(long)
	if len(got) > CloneDetailMax || len(got) < CloneDetailMax-1 {
		t.Fatalf("len = %d", len(got))
	}
	if !strings.HasPrefix(long, got) {
		t.Fatal("truncation broke a rune")
	}
}

func TestCheckDestination(t *testing.T) {
	root := realDir(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "work", "taken"), 0o755))
	must(os.WriteFile(filepath.Join(root, "work", "file"), []byte("x"), 0o644))
	must(os.Symlink(filepath.Join(root, "work"), filepath.Join(root, "link")))
	must(os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "work", "dangling")))
	outside := realDir(t)

	cases := []struct {
		dest   string
		detail string
		ok     bool
	}{
		{filepath.Join(root, "work", "new"), "", true},
		{filepath.Join(root, "new"), "", true},
		{filepath.Join(root, "work", "taken"), DetailExists, false},
		{filepath.Join(root, "work", "file"), DetailExists, false},
		{root, DetailExists, false},
		{filepath.Join(outside, "new"), DetailOutsideRoot, false},
		{filepath.Join(root, "..", "x"), DetailOutsideRoot, false},
		{"relative/path", DetailOutsideRoot, false},
		{"", DetailOutsideRoot, false},
		{filepath.Join(root, "link", "new"), DetailSymlink, false},
		{filepath.Join(root, "work", "dangling"), DetailSymlink, false},
		{filepath.Join(root, "work", "file", "new"), DetailNotWritable, false},
		{filepath.Join(root, "missing", "new"), DetailNotWritable, false},
	}
	for _, c := range cases {
		detail, ok := CheckDestination(root, c.dest)
		if ok != c.ok || detail != c.detail {
			t.Errorf("CheckDestination(%q) = %q, %v; want %q, %v", c.dest, detail, ok, c.detail, c.ok)
		}
	}

	if os.Geteuid() != 0 {
		ro := filepath.Join(root, "ro")
		must(os.Mkdir(ro, 0o500))
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
		if detail, ok := CheckDestination(root, filepath.Join(ro, "new")); ok || detail != DetailNotWritable {
			t.Errorf("read-only parent: %q, %v", detail, ok)
		}
	}
	// The probe leaves nothing behind.
	entries, _ := os.ReadDir(filepath.Join(root, "work"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hoplet-write-") {
			t.Errorf("probe left %s", e.Name())
		}
	}
}

func TestSuggestDestination(t *testing.T) {
	root := realDir(t)
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// With none watched: directly under the root.
	if got, want := SuggestDestination(nil, root, "repo"), filepath.Join(root, "repo"); got != want {
		t.Fatalf("no projects: %q, want %q", got, want)
	}
	// One project is not "next to each other".
	a := mk("code/a")
	if got, want := SuggestDestination([]string{a}, root, "repo"), filepath.Join(root, "repo"); got != want {
		t.Fatalf("one project: %q, want %q", got, want)
	}
	// Two beside each other: their parent.
	b := mk("code/b")
	c := mk("other/c")
	if got, want := SuggestDestination([]string{a, b, c}, root, "repo"), filepath.Join(root, "code", "repo"); got != want {
		t.Fatalf("siblings: %q, want %q", got, want)
	}
	// A taken name gets a number.
	mk("code/repo")
	if got, want := SuggestDestination([]string{a, b}, root, "repo"), filepath.Join(root, "code", "repo-2"); got != want {
		t.Fatalf("taken: %q, want %q", got, want)
	}
	// Siblings outside the root do not count.
	out1, out2 := realDir(t), realDir(t)
	if got, want := SuggestDestination([]string{out1, out2}, root, "x"), filepath.Join(root, "x"); got != want {
		t.Fatalf("outside: %q, want %q", got, want)
	}
}

// fakeGit puts a `git` on PATH that runs script, so a clone can be slow, noisy or failing on demand.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type cloneRig struct {
	cl         *Cloner
	root, home string
	mu         sync.Mutex
	registered []string
}

func newCloneRig(t *testing.T) *cloneRig {
	t.Helper()
	r := &cloneRig{root: realDir(t), home: realDir(t)}
	r.cl = &Cloner{Home: r.home, Root: r.root, Register: func(p string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.registered = append(r.registered, p)
		return nil
	}}
	return r
}

func (r *cloneRig) journals(t *testing.T) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(r.home, "handoff", "*"+cloneJournalSuffix))
	return m
}

func TestCloneRealRepository(t *testing.T) {
	src := realDir(t)
	run(t, src, "git", "init", "-q", "-b", "main")
	run(t, src, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "first")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, src, "git", "add", "f.txt")
	run(t, src, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "second")

	r := newCloneRig(t)
	r.cl.allowLocal = true
	dest := filepath.Join(r.root, "code", "clone")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := r.cl.Clone(context.Background(), "h1", src, dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != dest {
		t.Fatalf("path = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err == nil {
		t.Fatal("--no-checkout wrote a working tree")
	}
	if out, err := gitLine(context.Background(), dest, "rev-list", "--count", "--all"); err != nil || out != "2" {
		t.Fatalf("history = %q, %v", out, err)
	}
	if out, _ := gitLine(context.Background(), dest, "config", "--get", "remote.origin.url"); out != src {
		t.Fatalf("origin = %q", out)
	}
	if len(r.registered) != 1 || r.registered[0] != dest {
		t.Fatalf("registered = %v", r.registered)
	}
	if j := r.journals(t); len(j) != 0 {
		t.Fatalf("journal left: %v", j)
	}
	// The destination is taken now.
	_, err = r.cl.Clone(context.Background(), "h2", src, dest, nil)
	var f *CloneFailure
	if !errors.As(err, &f) || f.Reason != CloneDestinationTaken {
		t.Fatalf("second clone: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, ".git")); statErr != nil {
		t.Fatal("a refused clone touched the existing directory")
	}
}

func TestCloneProductionRefusesLocalPaths(t *testing.T) {
	src := realDir(t)
	run(t, src, "git", "init", "-q")
	r := newCloneRig(t)
	for _, origin := range []string{src, "file://" + src, "ext::sh -c true"} {
		dest := filepath.Join(r.root, "x")
		_, err := r.cl.Clone(context.Background(), "h1", origin, dest, nil)
		if err == nil {
			t.Fatalf("%q was cloned", origin)
		}
		if _, statErr := os.Stat(dest); statErr == nil {
			t.Fatalf("%q left a directory", origin)
		}
		if c := r.cl.Check(context.Background(), origin, dest); c.Reason != CloneNoOrigin {
			t.Fatalf("check %q = %v", origin, c)
		}
	}
}

func TestCloneFailureRemovesDirectory(t *testing.T) {
	cases := []struct {
		name, stderr, reason string
	}{
		{"access", "fatal: Could not read from remote repository.", CloneNoAccess},
		{"host key", "Host key verification failed.", CloneNoAccess},
		{"space", "fatal: write error: No space left on device", CloneNoSpace},
		{"other", "fatal: something odd with https://u:pw@h/r.git", CloneGitFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeGit(t, "echo '"+c.stderr+"' >&2; exit 128")
			r := newCloneRig(t)
			dest := filepath.Join(r.root, "repo")
			_, err := r.cl.Clone(context.Background(), "h1", "https://example.com/o/r.git", dest, nil)
			var f *CloneFailure
			if !errors.As(err, &f) || f.Reason != c.reason {
				t.Fatalf("err = %v, want %s", err, c.reason)
			}
			if c.reason == CloneGitFailed {
				if f.Detail == "" || strings.Contains(f.Detail, "pw") {
					t.Fatalf("detail = %q", f.Detail)
				}
			} else if f.Detail != "" {
				t.Fatalf("detail on %s: %q", c.reason, f.Detail)
			}
			if _, statErr := os.Stat(dest); statErr == nil {
				t.Fatal("directory left behind")
			}
			if len(r.journals(t)) != 0 || len(r.registered) != 0 {
				t.Fatal("journal or project left behind")
			}
		})
	}
}

func TestCloneCancelKillsGitAndRemovesDirectory(t *testing.T) {
	fakeGit(t, "mkdir -p \"$(eval echo \\${$#})/.git\"; exec sleep 30")
	r := newCloneRig(t)
	dest := filepath.Join(r.root, "repo")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.cl.Clone(ctx, "h1", "https://example.com/o/r.git", dest, nil)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(r.journals(t)) == 0 || !exists(filepath.Join(dest, ".git")) {
		if time.Now().After(deadline) {
			t.Fatal("clone did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		var f *CloneFailure
		if !errors.As(err, &f) || f.Reason != CloneCancelled {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not end the clone")
	}
	if exists(dest) || len(r.journals(t)) != 0 || len(r.registered) != 0 {
		t.Fatal("cancel left something behind")
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestCloneCancelLeavesNeighbours(t *testing.T) {
	fakeGit(t, "exec sleep 30")
	r := newCloneRig(t)
	neighbour := filepath.Join(r.root, "keep")
	if err := os.Mkdir(neighbour, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.cl.Clone(ctx, "h1", "https://example.com/o/r.git", filepath.Join(r.root, "repo"), nil)
	var f *CloneFailure
	if !errors.As(err, &f) || f.Reason != CloneCancelled {
		t.Fatalf("err = %v", err)
	}
	if !exists(neighbour) || exists(filepath.Join(r.root, "repo")) {
		t.Fatal("cancel removed the wrong directory")
	}
}

func TestCloneTimeouts(t *testing.T) {
	t.Run("stall", func(t *testing.T) {
		fakeGit(t, "exec sleep 30")
		r := newCloneRig(t)
		r.cl.Stall = 300 * time.Millisecond
		dest := filepath.Join(r.root, "repo")
		start := time.Now()
		_, err := r.cl.Clone(context.Background(), "h1", "https://example.com/o/r.git", dest, nil)
		var f *CloneFailure
		if !errors.As(err, &f) || f.Reason != CloneTimeout {
			t.Fatalf("err = %v", err)
		}
		if time.Since(start) > 10*time.Second || exists(dest) {
			t.Fatal("stall did not end and clean up")
		}
	})
	t.Run("progress restarts the stall timer", func(t *testing.T) {
		fakeGit(t, "for i in 1 2 3 4 5 6; do printf 'Receiving objects:  %s%% (1/2), 1.00 MiB | 1.00 MiB/s\\r' $i >&2; sleep 0.2; done; exit 0")
		r := newCloneRig(t)
		r.cl.Stall = 500 * time.Millisecond
		var mu sync.Mutex
		var got []int64
		_, err := r.cl.Clone(context.Background(), "h1", "https://example.com/o/r.git", filepath.Join(r.root, "repo"), func(received, total int64) {
			mu.Lock()
			got = append(got, received)
			mu.Unlock()
		})
		if err != nil {
			t.Fatalf("a clone that kept writing timed out: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(got) == 0 || got[0] != 1<<20 {
			t.Fatalf("progress = %v", got)
		}
		// 6 lines in about 1.2 s: at most four a second.
		if len(got) > 6 {
			t.Fatalf("progress not throttled: %v", got)
		}
	})
	t.Run("total", func(t *testing.T) {
		fakeGit(t, "while true; do printf 'Receiving objects: 1%% (1/2), 1.00 MiB\\r' >&2; sleep 0.05; done")
		r := newCloneRig(t)
		r.cl.Total = 400 * time.Millisecond
		dest := filepath.Join(r.root, "repo")
		_, err := r.cl.Clone(context.Background(), "h1", "https://example.com/o/r.git", dest, nil)
		var f *CloneFailure
		if !errors.As(err, &f) || f.Reason != CloneTimeout {
			t.Fatalf("err = %v", err)
		}
		if exists(dest) {
			t.Fatal("directory left behind")
		}
	})
}

func TestCloneProgressThrottle(t *testing.T) {
	fakeGit(t, "i=0; while [ $i -lt 40 ]; do printf 'Receiving objects: 1%% (1/2), 2.50 KiB\\r' >&2; i=$((i+1)); sleep 0.025; done")
	r := newCloneRig(t)
	n := 0
	start := time.Now()
	if _, err := r.cl.Clone(context.Background(), "h1", "https://example.com/o/r.git", filepath.Join(r.root, "repo"), func(received, total int64) {
		n++
		if received != 2560 || total != -1 {
			t.Errorf("received=%d total=%d", received, total)
		}
	}); err != nil {
		t.Fatal(err)
	}
	max := int(time.Since(start)/cloneProgressEvery) + 1
	if n < 1 || n > max {
		t.Fatalf("%d reports in %v (max %d)", n, time.Since(start), max)
	}
}

func TestCheck(t *testing.T) {
	r := newCloneRig(t)
	ctx := context.Background()

	c := r.cl.Check(ctx, "file:///etc", "")
	if c.Reason != CloneNoOrigin || c.CloneInto == "" {
		t.Fatalf("scheme: %+v", c)
	}
	if c := r.cl.Check(ctx, "https://u:p@h/o/r", ""); c.Reason != CloneNoOrigin {
		t.Fatalf("credentials: %+v", c)
	}

	taken := filepath.Join(r.root, "taken")
	if err := os.Mkdir(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGit(t, "exit 0")
	if c := r.cl.Check(ctx, "https://example.com/o/r.git", taken); c.Reason != CloneDestinationTaken || c.Detail != DetailExists || c.CloneInto != taken {
		t.Fatalf("taken: %+v", c)
	}
	// Suggested destination: under the root, named after the repository.
	c = r.cl.Check(ctx, "https://example.com/o/r.git", "")
	if c.Reason != CloneOK || c.CloneInto != filepath.Join(r.root, "r") {
		t.Fatalf("ok: %+v", c)
	}
	if exists(c.CloneInto) {
		t.Fatal("a check created the destination")
	}

	fakeGit(t, "echo 'fatal: could not read from remote' >&2; exit 128")
	if c := r.cl.Check(ctx, "git@example.com:o/r.git", ""); c.Reason != CloneNoAccess {
		t.Fatalf("no access: %+v", c)
	}
}

func writeJournal(t *testing.T, r *cloneRig, j cloneJournal) {
	t.Helper()
	if err := r.cl.writeJournal(j); err != nil {
		t.Fatal(err)
	}
}

func TestRecover(t *testing.T) {
	r := newCloneRig(t)
	origin := "https://example.com/o/r.git"
	mk := func(name string, files ...string) string {
		d := filepath.Join(r.root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			p := filepath.Join(d, f)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			content := ""
			if strings.HasSuffix(f, "config") {
				content = "[remote \"origin\"]\n\turl = " + origin + "\n"
			}
			if strings.HasSuffix(f, "other-config") {
				content = "[remote \"origin\"]\n\turl = https://elsewhere/x\n"
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	empty := mk("empty")
	partial := mk("partial", ".git/HEAD")
	partialCfg := mk("partial-cfg", ".git/HEAD", ".git/config")
	foreignGit := mk("foreign-git", ".git/HEAD", ".git/config")
	if err := os.WriteFile(filepath.Join(foreignGit, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://elsewhere/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFiles := mk("with-files", ".git/HEAD", "notes.txt")
	complete := mk("complete", ".git/HEAD", ".git/config")
	gone := filepath.Join(r.root, "gone")
	outside := realDir(t)
	link := filepath.Join(r.root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	for i, j := range []cloneJournal{
		{Destination: empty},
		{Destination: partial},
		{Destination: partialCfg},
		{Destination: foreignGit},
		{Destination: withFiles},
		{Destination: complete, Complete: true},
		{Destination: gone},
		{Destination: outside},
		{Destination: link},
		{Destination: "relative"},
	} {
		j.HandoffID = "h" + string(rune('a'+i))
		j.Origin = origin
		writeJournal(t, r, j)
	}
	if err := os.WriteFile(filepath.Join(r.home, "handoff", "bad"+cloneJournalSuffix), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.cl.Recover()

	for _, d := range []string{empty, partial, partialCfg} {
		if exists(d) {
			t.Errorf("%s survived recovery", d)
		}
	}
	for name, d := range map[string]string{
		"foreign git": foreignGit, "with files": withFiles, "complete": complete, "outside": outside, "link target": outside,
	} {
		if !exists(d) {
			t.Errorf("%s was removed: %s", name, d)
		}
	}
	if !exists(link) {
		t.Error("a link was removed")
	}
	if j := r.journals(t); len(j) != 0 {
		t.Errorf("journals left: %v", j)
	}
	if len(r.registered) != 1 || r.registered[0] != complete {
		t.Errorf("registered = %v", r.registered)
	}
}

func TestJournalIsNotAnInstallRecord(t *testing.T) {
	r := newCloneRig(t)
	writeJournal(t, r, cloneJournal{HandoffID: "h1", Origin: "o", Destination: filepath.Join(r.root, "x")})
	raw, _ := os.ReadFile(r.cl.journalPath("h1"))
	var j cloneJournal
	if err := json.Unmarshal(raw, &j); err != nil || j.Destination == "" {
		t.Fatalf("journal = %s, %v", raw, err)
	}
	inst := &Installer{Home: r.home, Projects: newFakeProjects()}
	if recs := inst.records(); len(recs) != 0 {
		t.Fatalf("install records = %v", recs)
	}
	pending, err := inst.Recover(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatalf("Recover = %v, %v", pending, err)
	}
	if !exists(r.cl.journalPath("h1")) {
		t.Fatal("the installer removed a clone journal")
	}
}

func TestCloneRejectsBadHandoffID(t *testing.T) {
	r := newCloneRig(t)
	_, err := r.cl.Clone(context.Background(), "../escape", "https://example.com/o/r.git", filepath.Join(r.root, "x"), nil)
	if err == nil {
		t.Fatal("accepted a path as handoff id")
	}
}

func TestReasonOf(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"libressl", "fatal: unable to access 'https://github.com/o/r.git/': LibreSSL SSL_ERROR_SYSCALL, errno 54", CloneGitFailed},
		{"ssl connect", "fatal: unable to access 'https://h/r.git/': SSL_connect: Connection reset by peer", CloneGitFailed},
		{"gnutls", "error: gnutls_handshake() failed: The TLS connection was non-properly terminated.", CloneGitFailed},
		{"resolve", "fatal: unable to access 'https://h/r.git/': Could not resolve host: h", CloneGitFailed},
		{"connect timed out", "fatal: unable to access 'https://h/r.git/': Connection timed out after 300001 milliseconds", CloneGitFailed},
		{"operation timed out", "ssh: connect to host h port 22: Operation timed out\nfatal: Could not read from remote repository.", CloneGitFailed},
		{"reset", "error: RPC failed; curl 56 Recv failure: Connection reset by peer", CloneGitFailed},
		{"early eof", "fatal: early EOF\nfatal: index-pack failed", CloneGitFailed},
		{"hung up", "fatal: the remote end hung up unexpectedly", CloneGitFailed},
		{"failed to connect", "fatal: unable to access 'https://h/r.git/': Failed to connect to h port 443", CloneGitFailed},
		{"ssh refused", "ssh: connect to host h port 22: Connection refused\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.", CloneGitFailed},
		{"not found", "remote: Repository not found.\nfatal: repository 'https://h/o/r.git/' not found", CloneNoAccess},
		{"not found with unable", "fatal: unable to access 'https://h/o/r.git/': The requested URL returned error: 404", CloneNoAccess},
		{"auth failed", "remote: Invalid username or password.\nfatal: Authentication failed for 'https://h/o/r.git/'", CloneNoAccess},
		{"publickey", "git@h: Permission denied (publickey).\nfatal: Could not read from remote repository.", CloneNoAccess},
		{"no prompts", "fatal: could not read Username for 'https://h': terminal prompts disabled", CloneNoAccess},
		{"password", "fatal: could not read Password for 'https://u@h': terminal prompts disabled", CloneNoAccess},
		{"host key", "Host key verification failed.\nfatal: Could not read from remote repository.", CloneNoAccess},
		{"denied with reset", "git@h: Permission denied (publickey).\nConnection reset by peer", CloneNoAccess},
		{"unnamed unreadable", "fatal: Could not read from remote repository.", CloneNoAccess},
		{"no space", "fatal: write error: No space left on device", CloneNoSpace},
		{"taken", "fatal: destination path 'x' already exists and is not an empty directory.", CloneDestinationTaken},
		{"other", "fatal: something odd", CloneGitFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reasonOf(c.text); got != c.want {
				t.Errorf("reasonOf(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}
