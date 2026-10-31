package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
)

func TestMaintenanceRunsWithTheHarnessEnvironment(t *testing.T) {
	cfg := testConfig(newHome(t, fake.Script{}))
	ran := make(chan []string, 4)
	cfg.MaintenanceEvery = time.Millisecond
	cfg.Maintenance = func(ctx context.Context, env []string) {
		select {
		case ran <- env:
		case <-ctx.Done():
		}
	}
	start(t, cfg) // closing it waits for the maintenance goroutine
	for range 2 {
		if env := <-ran; !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
			t.Fatalf("maintenance env %v", env)
		}
	}
}

func TestMaintainStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	n := 0
	go func() {
		defer close(done)
		maintain(ctx, func(context.Context, []string) {
			if n++; n == 3 {
				cancel()
			}
		}, time.Millisecond, func() []string { return nil })
	}()
	<-done
	if n != 3 {
		t.Fatalf("ran %d times", n)
	}
}
