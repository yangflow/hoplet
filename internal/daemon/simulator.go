package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/yangflow/hoplet/internal/simulator"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

// hostSimulator returns the simulator service on macOS and nil on any
// other host, a nil interface that hides the simulator tools.
func hostSimulator(goos string, r simulator.Runner, now func() time.Time, alive func(context.Context, proto.SessionID) bool) tools.Simulator {
	if goos != "darwin" {
		return nil
	}
	return simulator.New(r, now, alive)
}

// sessionAlive reports whether a session exists and is not archived. A
// failed lookup counts as alive, so that it cannot release a live lease.
func sessionAlive(st *store.Store) func(context.Context, proto.SessionID) bool {
	return func(ctx context.Context, id proto.SessionID) bool {
		sess, err := st.Session(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return false
		}
		if err != nil {
			return true
		}
		return sess.ArchivedAt == nil
	}
}
