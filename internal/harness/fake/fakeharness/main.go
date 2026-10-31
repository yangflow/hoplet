// Command fakeharness is the scriptable harness used by tests. It reads the
// script named by HOPLET_FAKE_SCRIPT and follows it; see package fake.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
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
	h := &harness{out: json.NewEncoder(os.Stdout), ops: make(chan fake.Op)}
	h.emit(fake.Ev{Ev: "session", ID: script.Session})
	go h.readOps()
	next := 0
	for op := range h.ops {
		switch op.Op {
		case "prompt":
			if next >= len(script.Turns) {
				h.emit(fake.Ev{Ev: "error", Text: "no more scripted turns"})
				continue
			}
			h.runTurn(script.Turns[next], op.Text)
			next++
		case "steer", "interrupt", "respond", "answer":
			h.emit(fake.Ev{Ev: "error", Text: op.Op + " outside a turn"})
		}
	}
}

type harness struct {
	mu  sync.Mutex
	out *json.Encoder
	ops chan fake.Op
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
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var op fake.Op
		if err := json.Unmarshal(sc.Bytes(), &op); err != nil {
			h.emit(fake.Ev{Ev: "error", Text: "bad op: " + err.Error()})
			continue
		}
		h.ops <- op
	}
	close(h.ops)
}

// runTurn runs one turn's steps, handling ops that arrive meanwhile.
func (h *harness) runTurn(turn fake.Turn, prompt string) {
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
// with a message; an interrupt ends the turn and wait returns false.
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
				h.emit(fake.Ev{Ev: "turn_ended", Reason: "interrupted"})
				return false
			case op.Op == "steer":
				h.emit(fake.Ev{Ev: "message", ID: "steer", Text: "steer:" + op.Text})
			case done != nil && done(op):
				return true
			default:
				h.emit(fake.Ev{Ev: "error", Text: "unexpected " + op.Op})
			}
		}
	}
}
