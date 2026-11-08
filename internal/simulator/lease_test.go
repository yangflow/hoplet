package simulator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestAcquire(t *testing.T) {
	tests := []struct {
		name      string
		holder    proto.SessionID
		idle      time.Duration
		dead      bool
		caller    proto.SessionID
		wantBusy  bool
		wantAfter proto.SessionID
	}{
		{name: "free", caller: "ses_1", wantAfter: "ses_1"},
		{name: "own reentry", holder: "ses_1", idle: 30 * time.Second, caller: "ses_1", wantAfter: "ses_1"},
		{name: "other holds", holder: "ses_2", idle: 10 * time.Second, caller: "ses_1", wantBusy: true, wantAfter: "ses_2"},
		{name: "just under 60s", holder: "ses_2", idle: 59*time.Second + 999*time.Millisecond, caller: "ses_1", wantBusy: true, wantAfter: "ses_2"},
		{name: "exactly 60s", holder: "ses_2", idle: 60 * time.Second, caller: "ses_1", wantAfter: "ses_1"},
		{name: "over 60s", holder: "ses_2", idle: 61 * time.Second, caller: "ses_1", wantAfter: "ses_1"},
		{name: "holder not alive", holder: "ses_2", idle: time.Second, dead: true, caller: "ses_1", wantAfter: "ses_1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSvc(listRunner())
			s.alive = func(_ context.Context, id proto.SessionID) bool { return !tt.dead || id != tt.holder }
			if tt.holder != "" {
				s.holders["U"] = holderEntry{session: tt.holder, lastInput: testNow.Add(-tt.idle)}
			}
			err := s.acquire(context.Background(), tt.caller, "U")
			var se *Error
			if tt.wantBusy {
				if !errors.As(err, &se) || se.Code != CodeBusy || !strings.Contains(se.Message, string(tt.holder)) {
					t.Fatalf("err = %v, want busy naming %s", err, tt.holder)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := s.holders["U"]; got.session != tt.wantAfter {
				t.Fatalf("holder = %q, want %q", got.session, tt.wantAfter)
			}
			if !tt.wantBusy && !s.holders["U"].lastInput.Equal(testNow) {
				t.Fatalf("lastInput = %v, want refreshed", s.holders["U"].lastInput)
			}
		})
	}
}

func TestAcquireConcurrent(t *testing.T) {
	s := newSvc(listRunner())
	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.acquire(context.Background(), proto.SessionID("ses_"+string(rune('a'+i))), "U")
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if se := (*Error)(nil); !errors.As(err, &se) || se.Code != CodeBusy {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d acquires succeeded, want 1", wins)
	}
}

func TestAcquireConcurrentExpiredHolder(t *testing.T) {
	s := newSvc(listRunner())
	s.holders["U"] = holderEntry{session: "old", lastInput: testNow.Add(-time.Hour)}
	const n = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.acquire(context.Background(), proto.SessionID("ses_"+string(rune('a'+i))), "U") == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d acquires succeeded, want 1", wins)
	}
}

func TestHolderFiltering(t *testing.T) {
	f := listRunner()
	s := newSvc(f)
	s.alive = func(_ context.Context, id proto.SessionID) bool { return id != "ses_dead" }
	s.holders["UDID-A18"] = holderEntry{session: "ses_stale", lastInput: testNow.Add(-60 * time.Second)}
	s.holders["UDID-T1"] = holderEntry{session: "ses_dead", lastInput: testNow}
	s.holders["UDID-A17"] = holderEntry{session: "ses_ok", lastInput: testNow.Add(-59 * time.Second)}
	devs, err := s.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]proto.SessionID{}
	for _, d := range devs {
		got[d.UDID] = d.Holder
	}
	want := map[string]proto.SessionID{"UDID-A18": "", "UDID-T1": "", "UDID-A17": "ses_ok", "UDID-T2": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("holders = %v, want %v", got, want)
	}
	// Omitted device must not resolve to an expired or dead hold.
	for _, c := range []proto.SessionID{"ses_stale", "ses_dead"} {
		var se *Error
		if _, err := s.resolve(context.Background(), c, ""); !errors.As(err, &se) || se.Code != CodeNotFound {
			t.Fatalf("%s: err = %v, want not found", c, err)
		}
	}
	if d, err := s.resolve(context.Background(), "ses_ok", ""); err != nil || d.UDID != "UDID-A17" {
		t.Fatalf("d = %+v, err = %v", d, err)
	}
}

func simctlCalls(f *fakeRunner, verbs ...string) []call {
	var out []call
	for _, c := range f.calls {
		if len(c.args) > 1 && c.args[0] == "simctl" {
			for _, v := range verbs {
				if c.args[1] == v {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func TestLaunch(t *testing.T) {
	ctx := context.Background()
	t.Run("install then launch", func(t *testing.T) {
		f := listRunner()
		s := newSvc(f)
		if err := s.Launch(ctx, "ses_1", "UDID-A18", "com.example.app", "/work/App.app"); err != nil {
			t.Fatal(err)
		}
		got := simctlCalls(f, "install", "launch")
		want := []call{
			{"xcrun", []string{"simctl", "install", "UDID-A18", "/work/App.app"}},
			{"xcrun", []string{"simctl", "launch", "UDID-A18", "com.example.app"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("calls = %+v", got)
		}
		if s.holders["UDID-A18"].session != "ses_1" {
			t.Fatal("lease not taken")
		}
	})
	t.Run("no app path skips install", func(t *testing.T) {
		f := listRunner()
		s := newSvc(f)
		if err := s.Launch(ctx, "ses_1", "UDID-A18", "com.example.app", ""); err != nil {
			t.Fatal(err)
		}
		if got := simctlCalls(f, "install"); len(got) != 0 {
			t.Fatalf("install called: %+v", got)
		}
		if got := simctlCalls(f, "launch"); len(got) != 1 {
			t.Fatalf("launch calls = %+v", got)
		}
	})
	t.Run("omitted device uses held", func(t *testing.T) {
		f := listRunner()
		s := newSvc(f)
		s.holders["UDID-A18"] = holderEntry{session: "ses_1", lastInput: testNow}
		if err := s.Launch(ctx, "ses_1", "", "com.example.app", ""); err != nil {
			t.Fatal(err)
		}
		if got := simctlCalls(f, "launch"); len(got) != 1 || got[0].args[2] != "UDID-A18" {
			t.Fatalf("launch calls = %+v", got)
		}
	})
	t.Run("refused lease runs nothing", func(t *testing.T) {
		f := listRunner()
		s := newSvc(f)
		s.holders["UDID-A18"] = holderEntry{session: "ses_2", lastInput: testNow}
		err := s.Launch(ctx, "ses_1", "UDID-A18", "com.example.app", "/work/App.app")
		var se *Error
		if !errors.As(err, &se) || se.Code != CodeBusy || !strings.Contains(se.Message, "ses_2") {
			t.Fatalf("err = %v", err)
		}
		if got := simctlCalls(f, "install", "launch"); len(got) != 0 {
			t.Fatalf("calls = %+v", got)
		}
	})
	t.Run("not booted", func(t *testing.T) {
		f := listRunner()
		s := newSvc(f)
		err := s.Launch(ctx, "ses_1", "UDID-A17", "com.example.app", "")
		var se *Error
		if !errors.As(err, &se) || se.Code != CodeNotBooted {
			t.Fatalf("err = %v", err)
		}
		if _, held := s.holders["UDID-A17"]; held || len(simctlCalls(f, "install", "launch")) != 0 {
			t.Fatal("lease taken or command run on an unbooted device")
		}
	})
	t.Run("failed install skips launch and keeps lease", func(t *testing.T) {
		f := listRunner()
		f.hook = func(_ string, args []string) ([]byte, error, bool) {
			if len(args) > 1 && args[1] == "install" {
				return nil, errors.New("bad bundle"), true
			}
			return nil, nil, false
		}
		s := newSvc(f)
		if err := s.Launch(ctx, "ses_1", "UDID-A18", "com.example.app", "/work/App.app"); err == nil {
			t.Fatal("want error")
		}
		if got := simctlCalls(f, "launch"); len(got) != 0 {
			t.Fatalf("launch ran: %+v", got)
		}
		if s.holders["UDID-A18"].session != "ses_1" {
			t.Fatal("lease dropped")
		}
	})
	t.Run("failed launch keeps lease", func(t *testing.T) {
		f := listRunner()
		f.hook = func(_ string, args []string) ([]byte, error, bool) {
			if len(args) > 1 && args[1] == "launch" {
				return nil, errors.New("no such app"), true
			}
			return nil, nil, false
		}
		s := newSvc(f)
		if err := s.Launch(ctx, "ses_1", "UDID-A18", "com.example.app", ""); err == nil {
			t.Fatal("want error")
		}
		if s.holders["UDID-A18"].session != "ses_1" {
			t.Fatal("lease dropped")
		}
	})
}

func TestOpenURL(t *testing.T) {
	ctx := context.Background()
	f := listRunner()
	s := newSvc(f)
	s.holders["UDID-A18"] = holderEntry{session: "ses_2", lastInput: testNow}
	if err := s.OpenURL(ctx, "ses_1", "UDID-A18", "myapp://x?y=1"); err != nil {
		t.Fatal(err)
	}
	got := simctlCalls(f, "openurl")
	want := []call{{"xcrun", []string{"simctl", "openurl", "UDID-A18", "myapp://x?y=1"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v", got)
	}
	if s.holders["UDID-A18"].session != "ses_2" {
		t.Fatal("OpenURL must not touch the lease")
	}
	var se *Error
	if err := s.OpenURL(ctx, "ses_1", "UDID-A17", "myapp://x"); !errors.As(err, &se) || se.Code != CodeNotBooted {
		t.Fatalf("err = %v", err)
	}
}
