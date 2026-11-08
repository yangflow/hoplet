package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fakeStdio feeds lines to a run and records what it writes.
type fakeStdio struct {
	lines  chan harness.Line
	mu     sync.Mutex
	writes [][]byte
}

func newFakeStdio() *fakeStdio { return &fakeStdio{lines: make(chan harness.Line)} }

func (f *fakeStdio) WriteLine(_ context.Context, line []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), line...))
	return nil
}

func (f *fakeStdio) written() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.writes...)
}

func (f *fakeStdio) Lines() <-chan harness.Line { return f.lines }
func (f *fakeStdio) Signal(string) error        { return nil }
func (f *fakeStdio) Close() error               { return nil }

// replayer drives runs through a recording.
type replayer struct {
	t      *testing.T
	io     *fakeStdio
	run    *run
	offset int64
	seen   int // writes already compared
	golden []string
}

func (p *replayer) attach(spec harness.Spec) {
	p.t.Helper()
	r, err := newRun(p.io, spec)
	if err != nil {
		p.t.Fatal(err)
	}
	r.prefix = "hoplet-x"
	p.run = r
	go r.loop()
	if err := r.start(context.Background()); err != nil {
		p.t.Fatal(err)
	}
}

// next waits for one event.
func (p *replayer) next() (harness.Event, bool) {
	p.t.Helper()
	select {
	case ev, ok := <-p.run.Events():
		return ev, ok
	case <-time.After(10 * time.Second):
		p.t.Fatalf("no event after line at offset %d", p.offset)
		return harness.Event{}, false
	}
}

// push feeds one line and waits until the run has handled it: the position
// line after it is handled only once everything before it is.
func (p *replayer) push(l harness.Line) {
	p.t.Helper()
	p.offset += 2
	l.Offset = p.offset
	p.io.lines <- l
	if l.Kind == harness.Exit {
		for {
			ev, ok := p.next()
			if !ok {
				return
			}
			p.record(ev)
		}
	}
	p.io.lines <- harness.Line{Kind: harness.Position, Offset: p.offset + 1}
	for {
		ev, ok := p.next()
		if !ok {
			p.t.Fatal("events closed early")
		}
		if ev.Offset == p.offset+1 {
			return
		}
		p.record(ev)
	}
}

func (p *replayer) record(ev harness.Event) {
	if ev.Payload == nil && ev.Native == "" {
		return
	}
	line := ""
	if ev.Native != "" {
		line = "native " + ev.Native + " "
	}
	if ev.Payload != nil {
		b, err := json.Marshal(ev.Payload)
		if err != nil {
			p.t.Fatal(err)
		}
		line += fmt.Sprintf("%T %s", ev.Payload, b)
	}
	if len(ev.Output) > 0 {
		line += fmt.Sprintf(" output=%q", ev.Output)
	}
	p.golden = append(p.golden, strings.TrimSpace(line))
}

// input issues the Hoplet command a recorded input line stands for, then
// checks the run wrote exactly that line.
func (p *replayer) input(in string) {
	p.t.Helper()
	var m struct {
		Method string `json:"method"`
		Params struct {
			Input []textInput `json:"input"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		p.t.Fatal(err)
	}
	var err error
	switch m.Method {
	case "turn/start":
		err = p.run.Send(context.Background(), m.Params.Input[0].Text)
	case "turn/interrupt":
		err = p.run.Interrupt(context.Background())
	}
	if err != nil {
		p.t.Fatalf("command for %s: %v", in, err)
	}
	w := p.io.written()
	if p.seen >= len(w) {
		p.t.Fatalf("the run did not write\n  %s", in)
	}
	var got, want any
	if json.Unmarshal(w[p.seen], &got) != nil || json.Unmarshal([]byte(in), &want) != nil || !reflect.DeepEqual(got, want) {
		p.t.Fatalf("for recorded input\n  %s\nthe run wrote\n  %s", in, w[p.seen])
	}
	p.seen++
	p.golden = append(p.golden, "write "+in)
}

func TestContract(t *testing.T) {
	cases := []struct {
		name   string
		resume string
	}{
		{"basic", ""}, {"interrupt", ""}, {"reattach", ""}, {"resume", "01a1baee-1102-7391-b028-dfa42f438db5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := harness.Spec{WorkDir: "/work", Mode: proto.ModeFullAuto, Resume: c.resume}
			path := filepath.Join("testdata", PinnedVersion, c.name+".jsonl")
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			p := &replayer{t: t, io: newFakeStdio()}
			p.attach(spec)
			sc := bufio.NewScanner(f)
			sc.Buffer(nil, 4<<20)
			for sc.Scan() {
				var e Entry
				if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
					t.Fatal(err)
				}
				switch e.Dir {
				case "in":
					p.input(e.Line)
				case "out":
					p.push(harness.Line{Kind: harness.Stdout, Data: []byte(e.Line)})
				case "exit":
					p.push(harness.Line{Kind: harness.Exit, Code: -1})
				case "reattach":
					native := p.run.NativeSession()
					if err := p.run.Close(); err != nil {
						t.Fatal(err)
					}
					p.attach(harness.Spec{WorkDir: "/work", Mode: proto.ModeFullAuto, State: harness.State{InTurn: true, Native: native}})
				}
			}
			if w := p.io.written(); p.seen != len(w) {
				t.Fatalf("the run wrote lines the recording does not have: %q", w[p.seen:])
			}
			got := strings.Join(p.golden, "\n") + "\n"
			golden := strings.TrimSuffix(path, ".jsonl") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("events differ from %s:\n%s", golden, got)
			}
		})
	}
}
