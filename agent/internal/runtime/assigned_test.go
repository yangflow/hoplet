package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// minting is a driver that is told the ID of a new session; it records the specs it started.
type minting struct {
	nowDriver
	mu    sync.Mutex
	specs []runtime.StartSpec
}

func (d *minting) Start(ctx context.Context, spec runtime.StartSpec) (runtime.Process, error) {
	d.mu.Lock()
	d.specs = append(d.specs, spec)
	d.mu.Unlock()
	return d.nowDriver.Start(ctx, spec)
}

func (d *minting) started() []runtime.StartSpec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]runtime.StartSpec(nil), d.specs...)
}

// assigning is a driver whose vendor assigns the ID of a new session.
type assigning struct {
	minting
	id  string
	err error
}

func (d *assigning) StartNew(ctx context.Context, spec runtime.StartSpec) (string, runtime.Process, error) {
	if d.err != nil {
		return "", nil, d.err
	}
	p, err := d.Start(ctx, spec)
	return d.id, p, err
}

func newCreateManager(t *testing.T, d runtime.Driver) (*runtime.Manager, *recorder) {
	t.Helper()
	rec := newRecorder()
	m := runtime.NewManager(runtime.Config{
		Driver: d,
		Locate: func(string) (runtime.Location, error) { return runtime.Location{Dir: t.TempDir()}, nil },
		Emit:   rec.emit, MaxIdle: 5,
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return m, rec
}

func TestAManagerMintsTheIDOfADriverThatDoesNotAssignOne(t *testing.T) {
	d := &minting{}
	m, rec := newCreateManager(t, d)
	id, err := m.Create(t.TempDir(), "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec.waitState(t, id, runtime.StateReady)
	specs := d.started()
	if len(specs) != 1 || specs[0].SessionID != id || id == "" || !specs[0].New {
		t.Fatalf("id %q, specs %+v", id, specs)
	}
}

func TestTheIDAVendorAssignsKeysTheSessionAndAnswersCreate(t *testing.T) {
	d := &assigning{id: "019e0000-0000-7000-8000-000000000001"}
	m, rec := newCreateManager(t, d)
	id, err := m.Create(t.TempDir(), "x", map[string]any{"mode": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if id != d.id {
		t.Fatalf("id = %q, want the vendor's %q", id, d.id)
	}
	specs := d.started()
	if len(specs) != 1 || specs[0].SessionID != "" || !specs[0].New || specs[0].Config["mode"] != "b" {
		t.Fatalf("specs = %+v", specs)
	}
	rec.waitState(t, id, runtime.StateReady)
	rec.mu.Lock()
	for _, ev := range rec.events {
		if s, ok := ev.(runtime.StateEvent); ok && s.SessionID != id {
			t.Fatalf("an event for another session: %+v", s)
		}
	}
	rec.mu.Unlock()
	if st, _ := m.Info(id); st != runtime.StateReady {
		t.Fatalf("state = %s", st)
	}
	// The session is driven like any other: the next prompt starts no second process.
	before := len(rec.states(id))
	if err := m.Prompt(id, "again"); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "second turn", func() bool {
		s := rec.states(id)
		return len(s) >= before+2 && s[len(s)-1] == runtime.StateReady
	})
	if n := len(d.started()); n != 1 {
		t.Fatalf("starts = %d", n)
	}
}

func TestAFailedStartOfAnAssignedSessionLeavesNoSession(t *testing.T) {
	d := &assigning{err: errors.New("no vendor")}
	m, rec := newCreateManager(t, d)
	if _, err := m.Create(t.TempDir(), "x", nil); err == nil {
		t.Fatal("a failed start was reported as done")
	}
	d.err, d.id = nil, ""
	if _, err := m.Create(t.TempDir(), "x", nil); err == nil {
		t.Fatal("an empty vendor ID was accepted")
	}
	if len(m.States()) != 0 {
		t.Fatalf("states %v", m.States())
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Fatalf("events = %v", rec.events)
	}
}

// mcpAssigning is an assigning driver with the mcp capability.
type mcpAssigning struct{ assigning }

func (d *mcpAssigning) Capabilities() []runtime.Capability {
	return append(d.assigning.Capabilities(), runtime.CapMCP)
}

// A session whose ID the vendor assigns gets an MCP spec made for no ID yet: the driver binds it to the ID.
func TestASessionTheVendorNamesStartsWithAnUnboundMCPSpec(t *testing.T) {
	d := &mcpAssigning{assigning{id: "019e0000-0000-7000-8000-000000000002"}}
	rec := newRecorder()
	var asked []string
	m := runtime.NewManager(runtime.Config{
		Driver: d,
		Locate: func(string) (runtime.Location, error) { return runtime.Location{Dir: t.TempDir()}, nil },
		Emit:   rec.emit, MaxIdle: 5,
		MCP: func(id string) *runtime.MCPSpec {
			asked = append(asked, id)
			return &runtime.MCPSpec{Command: "/bin/hoplet-agent", Args: []string{"mcp"}, Nonce: "n"}
		},
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	id, err := m.Create(t.TempDir(), "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec.waitState(t, id, runtime.StateReady)
	specs := d.started()
	if len(asked) != 1 || asked[0] != "" || len(specs) != 1 || specs[0].MCP == nil || specs[0].MCP.Nonce != "n" {
		t.Fatalf("asked %v, specs %+v", asked, specs)
	}
}
