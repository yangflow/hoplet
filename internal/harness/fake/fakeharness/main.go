// Command fakeharness is the scriptable harness used by tests. It reads the
// script named by HOPLET_FAKE_SCRIPT and follows it; see package fake.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
)

func main() {
	b, err := os.ReadFile(os.Getenv("HOPLET_FAKE_SCRIPT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeharness:", err)
		os.Exit(2)
	}
	var script fake.Script
	if err := json.Unmarshal(b, &script); err != nil {
		fmt.Fprintln(os.Stderr, "fakeharness: bad script:", err)
		os.Exit(2)
	}
	if err := validate(script); err != nil {
		fmt.Fprintln(os.Stderr, "fakeharness: bad script:", err)
		os.Exit(2)
	}
	session, next := script.Session, 0
	if resume := os.Getenv("HOPLET_FAKE_RESUME"); resume != "" {
		session, next = resume, script.ResumeTurn
	}
	h := &harness{out: json.NewEncoder(os.Stdout), ops: make(chan fake.Op)}
	h.emit(fake.Ev{Ev: "session", ID: session})
	go h.readOps()
	for op := range h.ops {
		switch op.Op {
		case "prompt":
			if next >= len(script.Turns) {
				h.emit(fake.Ev{Ev: "error", Text: "no more scripted turns"})
				continue
			}
			h.runTurn(script.Turns[next], op.Text)
			next++
		case "set_mode":
			h.emit(fake.Ev{Ev: "message", ID: "mode", Text: "mode:" + op.Text})
		case "set_model":
			h.emit(fake.Ev{Ev: "message", ID: "model", Text: "model:" + op.Text})
		case "interrupt":
			// Nothing to interrupt.
		case "steer", "respond", "answer":
			h.emit(fake.Ev{Ev: "error", Text: op.Op + " outside a turn"})
		}
	}
}

// validate checks that every step sets exactly one field.
func validate(s fake.Script) error {
	for i, t := range s.Turns {
		for j, st := range t.Steps {
			n := 0
			for _, set := range []bool{
				st.Emit != nil, st.Raw != "", st.Stderr != "", st.EchoPrompt, st.SleepMS > 0,
				st.AwaitApproval != "", st.AwaitAnswer != "", st.Exit != nil,
				st.Signal != "", st.IgnoreIntr, st.Hang,
			} {
				if set {
					n++
				}
			}
			if n != 1 {
				return fmt.Errorf("turn %d step %d sets %d fields, want exactly 1", i, j, n)
			}
			if st.Signal != "" && st.Signal != "TERM" && st.Signal != "KILL" {
				return fmt.Errorf("turn %d step %d: unknown signal %q", i, j, st.Signal)
			}
		}
	}
	return nil
}

type harness struct {
	mu  sync.Mutex
	out *json.Encoder
	ops chan fake.Op

	ignoreIntr bool // interrupts are ignored for the rest of the turn
}

func (h *harness) emit(ev fake.Ev) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.out.Encode(ev) // stdout closing means the shim is gone; nothing to do
}

func (h *harness) raw(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Println(line)
}

func (h *harness) readOps() {
	defer close(h.ops)
	br := bufio.NewReader(os.Stdin)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var op fake.Op
			if jerr := json.Unmarshal(line, &op); jerr != nil {
				h.emit(fake.Ev{Ev: "error", Text: "bad op: " + jerr.Error()})
			} else {
				h.ops <- op
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintln(os.Stderr, "fakeharness: read ops:", err)
			}
			return
		}
	}
}

// runTurn runs one turn's steps, handling ops that arrive meanwhile.
func (h *harness) runTurn(turn fake.Turn, prompt string) {
	h.ignoreIntr = false
	h.emit(fake.Ev{Ev: "turn_started"})
	for _, st := range turn.Steps {
		switch {
		case st.Emit != nil:
			h.emit(*st.Emit)
		case st.Raw != "":
			h.raw(st.Raw)
		case st.Stderr != "":
			fmt.Fprintln(os.Stderr, st.Stderr)
		case st.EchoPrompt:
			h.emit(fake.Ev{Ev: "message", ID: "echo", Text: prompt})
		case st.Exit != nil:
			os.Exit(*st.Exit)
		case st.Signal != "":
			sig := syscall.SIGTERM
			if st.Signal == "KILL" {
				sig = syscall.SIGKILL
			}
			_ = syscall.Kill(os.Getpid(), sig) // the process dies; failure leaves the turn to time out
			time.Sleep(time.Minute)
		case st.IgnoreIntr:
			h.ignoreIntr = true
		case st.Hang:
			for range h.ops { // ignore everything until killed
			}
			os.Exit(0)
		case st.SleepMS > 0:
			if !h.wait(time.After(time.Duration(st.SleepMS)*time.Millisecond), nil) {
				return
			}
		case st.AwaitApproval != "":
			if !h.wait(nil, func(op fake.Op) bool {
				if op.Op == "respond" && op.ID == st.AwaitApproval {
					h.emit(fake.Ev{Ev: "message", ID: "approval-" + op.ID, Text: "approved:" + op.Option})
					return true
				}
				return false
			}) {
				return
			}
		case st.AwaitAnswer != "":
			if !h.wait(nil, func(op fake.Op) bool {
				if op.Op == "answer" && op.ID == st.AwaitAnswer {
					h.emit(fake.Ev{Ev: "message", ID: "answer-" + op.ID, Text: "answered:" + op.Text})
					return true
				}
				return false
			}) {
				return
			}
		}
	}
	h.emit(fake.Ev{Ev: "turn_ended", Reason: "completed"})
}

// wait blocks until timer fires or done accepts an op. Steers are answered
// with a message; an interrupt ends the turn and wait returns false unless
// the turn ignores interrupts.
func (h *harness) wait(timer <-chan time.Time, done func(fake.Op) bool) bool {
	for {
		select {
		case <-timer:
			return true
		case op, ok := <-h.ops:
			if !ok {
				os.Exit(0)
			}
			switch {
			case op.Op == "interrupt":
				if h.ignoreIntr {
					continue
				}
				h.emit(fake.Ev{Ev: "turn_ended", Reason: "interrupted"})
				return false
			case op.Op == "steer":
				h.emit(fake.Ev{Ev: "message", ID: "steer", Text: "steer:" + op.Text})
			case op.Op == "set_mode":
				h.emit(fake.Ev{Ev: "message", ID: "mode", Text: "mode:" + op.Text})
			case op.Op == "set_model":
				h.emit(fake.Ev{Ev: "message", ID: "model", Text: "model:" + op.Text})
			case done != nil && done(op):
				return true
			default:
				h.emit(fake.Ev{Ev: "error", Text: "unexpected " + op.Op})
			}
		}
	}
}
