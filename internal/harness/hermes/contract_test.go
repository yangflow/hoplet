package hermes

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// recordedVersion is the Hermes version the fixtures were recorded with:
// spools of real Hoplet sessions, sanitized (`hoplet dev sanitize
// --harness hermes`).
const recordedVersion = "0.22.5"

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

func readFixture(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// rpc is a request line, recorded or written by the run.
type rpc struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func parseRPC(t *testing.T, line []byte) rpc {
	t.Helper()
	var r rpc
	if err := json.Unmarshal(line, &r); err != nil {
		t.Fatalf("request %s: %v", line, err)
	}
	return r
}

// replayer drives runs through a recording.
type replayer struct {
	t       *testing.T
	spec    harness.Spec
	io      *fakeStdio
	run     *run
	offset  int64
	golden  []string
	ids     map[string]string // recorded request ID -> the run's
	matched int               // writes of the current run already matched
	entries []Entry
	at      int // the entry being replayed
}

func (p *replayer) start() {
	p.io = newFakeStdio()
	p.run = newRun(p.io, p.spec)
	p.matched = 0
}

// push feeds one bridge line and waits until the run has handled it: the
// position line after it is handled only once everything before it is.
func (p *replayer) push(kind harness.LineKind, data []byte, code int) {
	p.t.Helper()
	before := len(p.io.written())
	p.offset += 2
	p.io.lines <- harness.Line{Kind: kind, Data: data, Offset: p.offset - 1, Code: code}
	final := kind == harness.Exit
	if !final {
		p.io.lines <- harness.Line{Kind: harness.Position, Offset: p.offset}
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-p.run.Events():
			if !ok {
				if final {
					return
				}
				p.t.Fatalf("events closed: %v", p.run.Err())
			}
			if ev.Native != "" {
				p.golden = append(p.golden, "native "+ev.Native)
			}
			if ev.Payload != nil {
				b, _ := json.Marshal(ev.Payload)
				p.golden = append(p.golden, fmt.Sprintf("%T %s", ev.Payload, b))
			}
			if final && ev.Exit != nil {
				p.drain()
				return
			}
			if !final && ev.Offset == p.offset && ev.Payload == nil && ev.Raw == nil {
				p.noteWrites(before)
				return
			}
		case <-timeout:
			p.t.Fatalf("no event for line %s", data)
		}
	}
}

func (p *replayer) drain() {
	for range p.run.Events() {
	}
}

// noteWrites records the writes the run made by itself while handling a
// line; they must each match a later recorded request.
func (p *replayer) noteWrites(before int) {
	for _, w := range p.io.written()[before:] {
		p.golden = append(p.golden, "write "+p.normalize(w))
	}
}

// normalize hides what differs from run to run: request IDs and the random
// keys Hermes uses for idempotency.
func (p *replayer) normalize(line []byte) string {
	var v map[string]any
	if json.Unmarshal(line, &v) != nil {
		return string(line)
	}
	v["id"] = "x"
	if params, ok := v["params"].(map[string]any); ok {
		for _, k := range []string{"submission_id"} {
			if _, ok := params[k]; ok {
				params[k] = "x"
			}
		}
		if v["method"] == "session.mutate" {
			params["request_id"] = "x"
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// rewriteID gives a recorded response the ID the run used for the request.
func (p *replayer) rewriteID(line []byte) []byte {
	var v map[string]json.RawMessage
	if json.Unmarshal(line, &v) != nil || v["id"] == nil || v["method"] != nil {
		return line
	}
	var id string
	if json.Unmarshal(v["id"], &id) != nil {
		return line
	}
	ours, ok := p.ids[id]
	if !ok {
		return line
	}
	v["id"], _ = json.Marshal(ours)
	b, _ := json.Marshal(v)
	return b
}

// request handles a recorded request: one the run already wrote, or one a
// Hoplet command makes it write.
func (p *replayer) request(line []byte) {
	p.t.Helper()
	want := parseRPC(p.t, line)
	writes := p.io.written()
	if p.matched == len(writes) {
		p.command(want)
		writes = p.io.written()
		if p.matched == len(writes) {
			p.t.Fatalf("no write for recorded request %s", line)
		}
		for _, w := range writes[p.matched:] {
			p.golden = append(p.golden, "write "+p.normalize(w))
		}
	}
	got := parseRPC(p.t, writes[p.matched])
	p.matched++
	if !sameRequest(got, want) {
		p.t.Fatalf("for recorded request\n  %s\nthe run wrote\n  %s", line, writes[p.matched-1])
	}
	p.ids[want.ID] = got.ID
}

// command issues the Hoplet command that produced a recorded request.
func (p *replayer) command(want rpc) {
	p.t.Helper()
	ctx := context.Background()
	str := func(k string) string { s, _ := want.Params[k].(string); return s }
	var err error
	switch want.Method {
	case "prompt.submit":
		err = p.run.Send(ctx, str("text"))
	case "approval.respond":
		err = p.run.Respond(ctx, str("prompt_id"), str("choice"))
	case "session.interrupt":
		err = p.run.Interrupt(ctx)
	case "session.steer":
		err = p.run.Steer(ctx, str("text"))
	case "config.set":
		switch str("key") {
		case "yolo":
			err = p.run.SetMode(ctx, p.modeAt(str("value") == "1"))
		case "service_tier":
			err = p.run.SetFast(ctx, str("value") == "fast")
		default:
			p.t.Fatalf("unexpected config.set %v", want.Params)
		}
	case "session.mutate":
		payload, _ := want.Params["payload"].(map[string]any)
		switch str("operation") {
		case "reasoning":
			effort, _ := payload["effort"].(string)
			err = p.run.SetEffort(ctx, proto.Effort(effort))
		case "model":
			model, _ := payload["model"].(string)
			provider, _ := payload["provider"].(string)
			err = p.run.SetModel(ctx, model, provider)
		default:
			p.t.Fatalf("unexpected session.mutate %v", want.Params)
		}
	default:
		p.t.Fatalf("the run never wrote recorded request %s", want.Method)
	}
	if err != nil {
		p.t.Fatalf("command for %s: %v", want.Method, err)
	}
}

// modeAt is the permission mode a recorded yolo switch belongs to: the
// edit policy the next recorded request sets, or the run's current one.
func (p *replayer) modeAt(yolo bool) proto.PermissionMode {
	policy := p.run.policy
	for _, e := range p.entries[p.at+1:] {
		if e.Dir != "in" {
			continue
		}
		r := parseRPC(p.t, []byte(e.Line))
		if r.Method == "session.mutate" && r.Params["operation"] == "edit_policy" {
			payload, _ := r.Params["payload"].(map[string]any)
			policy, _ = payload["policy"].(string)
		}
		break
	}
	switch {
	case yolo:
		return proto.ModeFullAuto
	case policy == editWorkspace:
		return proto.ModeAutoEdit
	}
	return proto.ModeConfirm
}

// sameRequest compares what the run wrote with what was recorded, except
// the request ID and the keys that are random per write.
func sameRequest(got, want rpc) bool {
	if got.Method != want.Method {
		return false
	}
	clean := func(m map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			switch k {
			case "submission_id":
				continue
			case "request_id":
				if want.Method == "session.mutate" {
					continue
				}
			}
			out[k] = v
		}
		return out
	}
	return reflect.DeepEqual(clean(got.Params), clean(want.Params))
}

// specs are the settings each recording's session was created with.
var specs = map[string]harness.Spec{
	"modes": {Mode: proto.ModeAutoEdit},
}

// TestRecordings replays the bridge's recorded output through the adapter,
// issues the Hoplet command behind each recorded request it did not write
// by itself, checks every write against the recording, and compares the
// events with a golden file (go test -update rewrites them).
func TestRecordings(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", recordedVersion, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings: %v", err)
	}
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		t.Run(name, func(t *testing.T) {
			entries := readFixture(t, path)
			spec, ok := specs[name]
			if !ok {
				spec = harness.Spec{Mode: proto.ModeConfirm}
			}
			spec.WorkDir = "/work"
			spec.Session = recordedSession(t, entries)
			p := &replayer{t: t, spec: spec, ids: map[string]string{}, entries: entries}
			p.start()
			for i, e := range entries {
				p.at = i
				switch e.Dir {
				case "out":
					p.push(harness.Stdout, p.rewriteID([]byte(e.Line)), 0)
				case "in":
					p.request([]byte(e.Line))
				case "exit":
					code, _ := strconv.Atoi(e.Line)
					p.push(harness.Exit, nil, code)
					if writes := p.io.written(); p.matched != len(writes) {
						t.Fatalf("unrecorded writes: %s", writes[p.matched:])
					}
					if i == len(entries)-1 {
						break
					}
					// The manager starts a new bridge that resumes.
					p.run.mu.Lock()
					native, inTurn := p.run.native, p.run.busy
					p.run.mu.Unlock()
					_ = p.run.Close()
					p.spec.Resume, p.spec.State = native, harness.State{Native: native, InTurn: inTurn}
					p.golden = append(p.golden, "-- new bridge")
					p.start()
				}
			}
			if writes := p.io.written(); p.matched != len(writes) {
				t.Fatalf("unrecorded writes: %s", writes[p.matched:])
			}
			_ = p.run.Close()
			golden := strings.TrimSuffix(path, ".jsonl") + ".golden"
			got := strings.Join(p.golden, "\n") + "\n"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			b, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != got {
				t.Fatalf("events differ from %s (go test -update rewrites it):\n%s", golden, diff(string(b), got))
			}
		})
	}
}

// recordedSession is the Hoplet session ID a recording's session.create
// used as its request ID.
func recordedSession(t *testing.T, entries []Entry) proto.SessionID {
	t.Helper()
	for _, e := range entries {
		if e.Dir != "in" {
			continue
		}
		r := parseRPC(t, []byte(e.Line))
		if r.Method == "session.create" {
			id, _ := r.Params["request_id"].(string)
			return proto.SessionID(id)
		}
	}
	return "recorded"
}

func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\n- %s\n+ %s", i+1, a, b)
		}
	}
	return ""
}
