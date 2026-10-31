package claude

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

// recordedVersion is the claude version the fixtures were recorded with
// (`hoplet dev record`); they are claude's real output, sanitized.
const recordedVersion = "2.1.312"

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

type entry struct {
	Dir  string `json:"dir"`
	Line string `json:"line"`
}

func readFixture(t *testing.T, path string) []entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []entry
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		var e entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// replayer drives a run through a recording.
type replayer struct {
	t      *testing.T
	io     *fakeStdio
	run    *run
	offset int64
	golden []string
}

// push feeds one stdout line and waits until the run has handled it: the
// position line after it is handled only once everything before it is.
func (p *replayer) push(kind harness.LineKind, data []byte) {
	p.t.Helper()
	p.offset += 2
	p.io.lines <- harness.Line{Kind: kind, Data: data, Offset: p.offset}
	p.io.lines <- harness.Line{Kind: harness.Position, Offset: p.offset + 1}
	for {
		select {
		case ev := <-p.run.Events():
			if ev.Offset == p.offset+1 {
				return
			}
			p.record(ev)
		case <-time.After(10 * time.Second):
			p.t.Fatalf("the run did not handle line %d", p.offset)
		}
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
	p.golden = append(p.golden, strings.TrimSpace(line))
}

// command issues the Hoplet command a recorded input line stands for and
// checks the run wrote the same thing; it reports false for recorder-only
// probes no Hoplet command produces.
func (p *replayer) command(in string) bool {
	p.t.Helper()
	ctx := context.Background()
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Request  map[string]any `json:"request"`
		Response struct {
			RequestID string         `json:"request_id"`
			Response  map[string]any `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(in), &msg); err != nil {
		p.t.Fatal(err)
	}
	var err error
	switch msg.Type {
	case "user":
		err = p.run.Send(ctx, msg.Message.Content)
	case "control_response":
		r := msg.Response.Response
		option := optionAllow
		switch {
		case r["behavior"] == "deny":
			option = optionDeny
		case r["updatedPermissions"] != nil:
			option = optionAlways
		}
		err = p.run.Respond(ctx, msg.Response.RequestID, option)
	case "control_request":
		switch sub := msg.Request["subtype"]; sub {
		case "interrupt":
			err = p.run.Interrupt(ctx)
		case "set_model":
			err = p.run.SetModel(ctx, msg.Request["model"].(string))
		case "set_permission_mode":
			mode := msg.Request["mode"].(string)
			p.run.mu.Lock()
			inPlan := p.run.plan
			p.run.mu.Unlock()
			switch {
			case mode == planMode:
				err = p.run.SetPlan(ctx, true)
			case inPlan:
				if err = p.run.SetMode(ctx, hopletMode(mode)); err == nil {
					err = p.run.SetPlan(ctx, false)
				}
			default:
				err = p.run.SetMode(ctx, hopletMode(mode))
			}
		case "apply_flag_settings":
			settings := msg.Request["settings"].(map[string]any)
			effort, ok := settings["effortLevel"].(string)
			if !ok {
				return false
			}
			err = p.run.SetEffort(ctx, proto.Effort(effort))
		default:
			return false // initialize and the recorder's own probes
		}
	default:
		p.t.Fatalf("unexpected input %s", in)
	}
	if err != nil {
		p.t.Fatalf("command for %s: %v", in, err)
	}
	writes := p.io.written()
	got := writes[len(writes)-1]
	if !sameInput(p.t, got, []byte(in)) {
		p.t.Fatalf("for recorded input\n  %s\nthe run wrote\n  %s", in, got)
	}
	p.golden = append(p.golden, "write "+strings.ReplaceAll(string(got), p.run.prefix, "hoplet-x"))
	return true
}

// sameInput compares what the run wrote with what the recorder wrote,
// ignoring the IDs of requests and the text of a denial. An answer must
// carry the same behavior, and every permission update the recorder sent:
// the adapter sends all of claude's suggestions, where one early
// recording sent only some.
func sameInput(t *testing.T, got, want []byte) bool {
	t.Helper()
	var g, w map[string]any
	if json.Unmarshal(got, &g) != nil || json.Unmarshal(want, &w) != nil {
		return false
	}
	if g["type"] == "control_response" {
		gr := g["response"].(map[string]any)
		wr := w["response"].(map[string]any)
		gp, _ := gr["response"].(map[string]any)
		wp, _ := wr["response"].(map[string]any)
		if gr["request_id"] != wr["request_id"] || gp["behavior"] != wp["behavior"] {
			return false
		}
		gu, _ := gp["updatedPermissions"].([]any)
		wu, _ := wp["updatedPermissions"].([]any)
		if (len(gu) == 0) != (len(wu) == 0) {
			return false
		}
		for _, u := range wu {
			found := false
			for _, v := range gu {
				found = found || reflect.DeepEqual(u, v)
			}
			if !found {
				return false
			}
		}
		return true
	}
	delete(g, "request_id")
	delete(w, "request_id")
	return reflect.DeepEqual(g, w)
}

// specs are the settings each recording was made with.
var specs = map[string]harness.Spec{
	"auto_effort": {Mode: proto.ModeConfirm, Effort: proto.EffortHigh},
}

// TestRecordings replays claude's recorded output through the adapter,
// issues the Hoplet commands behind the recorded inputs, and compares the
// normalized events and written lines with the golden files.
func TestRecordings(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", recordedVersion, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".jsonl")
		t.Run(name, func(t *testing.T) {
			spec, ok := specs[name]
			if !ok {
				spec = harness.Spec{Mode: proto.ModeConfirm}
			}
			io := newFakeStdio()
			p := &replayer{t: t, io: io, run: newRun(io, spec)}
			defer p.run.Close()
			for _, e := range readFixture(t, file) {
				switch e.Dir {
				case "out":
					p.push(harness.Stdout, []byte(e.Line))
				case "in":
					p.command(e.Line)
				}
			}
			golden := filepath.Join("testdata", recordedVersion, name+".golden")
			got := strings.Join(p.golden, "\n") + "\n"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Fatalf("events differ from %s (run with -update and review the diff):\n%s", golden, got)
			}
		})
	}
}
