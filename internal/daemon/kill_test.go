package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// helperEnv makes the test binary run a daemon instead of the tests: a real
// daemon process with the fake harness, which the production binary does
// not ship.
const helperEnv = "HOPLET_DAEMON_HELPER"

func runHelper() int {
	cfg := Config{
		Home: os.Getenv("HOPLET_HOME"), Version: "helper", Exe: os.Getenv("HOPLET_TEST_HOPLET"),
		Harnesses: map[string]harness.Harness{"fake": &fake.Harness{
			Binary: os.Getenv("HOPLET_TEST_FAKE"), Script: os.Getenv("HOPLET_TEST_SCRIPT"),
		}},
		Env: env.Static{"PATH=" + os.Getenv("PATH")},
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	d, err := Start(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	select {
	case <-sig:
	case <-d.Done():
	}
	if err := d.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// startHelper starts a daemon process for home; it is stopped when the test
// ends if it is still running.
func startHelper(t *testing.T, home, script string) *exec.Cmd {
	t.Helper()
	logf, err := os.OpenFile(filepath.Join(home, "helper.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"=1", "HOPLET_HOME="+home,
		"HOPLET_TEST_HOPLET="+hopletExe, "HOPLET_TEST_FAKE="+fakeExe, "HOPLET_TEST_SCRIPT="+script)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	})
	return cmd
}

// dialRetry dials the home's daemon until one serves.
func dialRetry(t *testing.T, home string) *client.Client {
	t.Helper()
	var c *client.Client
	waitCond(t, "a serving daemon", func() bool {
		o, err := client.HomeOptions(home, "device:test")
		if err != nil {
			return false
		}
		c, err = client.Dial(context.Background(), o)
		return err == nil
	})
	t.Cleanup(func() { c.Close() })
	return c
}

// TestKill9MidTurnResumes kills a real daemon process with SIGKILL in the
// middle of a turn and checks that the next daemon picks the turn up from
// the same harness: the client resumes from its cursor and every event of
// the turn is in the log exactly once.
func TestKill9MidTurnResumes(t *testing.T) {
	steps := []fake.Step{{Emit: &fake.Ev{Ev: "message", ID: "s0", Text: "s0"}}}
	want := []string{"go", "s0"}
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("s%d", i)
		steps = append(steps, fake.Step{SleepMS: 100}, fake.Step{Emit: &fake.Ev{Ev: "message", ID: id, Text: id}})
		want = append(want, id)
	}
	home, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: steps}}})

	first := startHelper(t, home, script)
	c := dialRetry(t, home)
	sess := create(t, c, home)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "go", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return len(w.texts(1)) >= 4 })
	if err := first.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	select {
	case <-c.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the client did not notice the daemon's death")
	}
	seen := w.events

	startHelper(t, home, script)
	c2 := dialRetry(t, home)
	w2 := watch(t, c2, sess.ID, seen[len(seen)-1].Seq)
	w2.until(turnEnded(1))
	if w2.snap != nil {
		t.Fatal("a small gap was answered with a snapshot")
	}

	all := watch(t, c2, sess.ID, 0)
	all.until(turnEnded(1))
	if got := all.texts(1); !slices.Equal(got, want) {
		t.Fatalf("messages of turn 1:\n got %v\nwant %v", got, want)
	}
	joined := append(slices.Clone(seen), w2.events...)
	if len(joined) != len(all.events) {
		t.Fatalf("resumed view has %d events, the log %d", len(joined), len(all.events))
	}
	for i, ev := range all.events {
		if ev.Seq != uint64(i+1) || joined[i].Seq != ev.Seq {
			t.Fatalf("event %d has seq %d (resumed view %d)%s", i, ev.Seq, joined[i].Seq, describe(all.events))
		}
	}
	spool, err := os.ReadFile(filepath.Join(home, "sessions", string(sess.ID), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(spool), `"k":"start"`); n != 1 {
		t.Fatalf("the harness was started %d times, want once", n)
	}
}

// TestRestartKeepsServing checks a gracefully stopped daemon's successor
// takes over the socket and serves the same sessions.
func TestRestartKeepsServing(t *testing.T) {
	home, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}})
	first := startHelper(t, home, script)
	c := dialRetry(t, home)
	sess := create(t, c, home)
	if err := first.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("daemon exit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "daemon.sock")); err != nil {
		t.Fatalf("the socket file is gone after a clean stop: %v", err)
	}
	startHelper(t, home, script)
	c2 := dialRetry(t, home)
	ss, err := c2.Sessions(timeout(t))
	if err != nil || len(ss) != 1 || ss[0].ID != sess.ID {
		t.Fatalf("sessions %v %v", ss, err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Sessions(deadline); err == nil {
		t.Fatal("the old connection still answers")
	}
}
