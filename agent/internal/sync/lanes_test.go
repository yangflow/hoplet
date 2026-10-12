package sync

import (
	"testing"
	"time"
)

func TestLaneOf(t *testing.T) {
	bulk := []any{SessionAppend{}, WorkspaceSnapshot{}, SessionWorkspaceFile{}, SessionWorkspaceDiff{}}
	for _, msg := range bulk {
		if laneOf(msg) != LaneBulk {
			t.Errorf("%T should be bulk", msg)
		}
	}
	control := []any{PingMsg{}, DeviceHello{}, RuntimeState{}, SessionWorkspace{}, SessionWorkspaceRejected{}, HandoffPrepared{}, HandoffVerified{}, HandoffFailed{}, DeviceProjects{}}
	for _, msg := range control {
		if laneOf(msg) != LaneControl {
			t.Errorf("%T should be control", msg)
		}
	}
}

// A control message waits for the message being written, not for the bulk queue behind it.
func TestWriterServesControlBeforeQueuedBulk(t *testing.T) {
	control := make(chan outbound, 8)
	bulk := make(chan outbound, 8)
	stop := make(chan struct{})
	defer close(stop)

	gate := make(chan struct{})
	written := make(chan string, 8)
	go runWriter(stop, control, bulk, func(o outbound) error {
		name := o.msg.(string)
		if name == "bulk-1" {
			<-gate // the first bulk message is on the wire
		}
		written <- name
		return nil
	})

	send := func(queue chan outbound, name string) chan error {
		o := outbound{msg: name, done: make(chan error, 1)}
		queue <- o
		return o.done
	}
	send(bulk, "bulk-1")
	time.Sleep(20 * time.Millisecond) // let the writer take bulk-1
	send(bulk, "bulk-2")
	send(bulk, "bulk-3")
	controlDone := send(control, "control-1")
	close(gate)

	var order []string
	for i := 0; i < 4; i++ {
		select {
		case name := <-written:
			order = append(order, name)
		case <-time.After(2 * time.Second):
			t.Fatalf("wrote only %v", order)
		}
	}
	want := []string{"bulk-1", "control-1", "bulk-2", "bulk-3"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
	if err := <-controlDone; err != nil {
		t.Fatal(err)
	}
}

func TestWriteMsgFailsWhenTheConnectionIsClosed(t *testing.T) {
	c := &Client{}
	cs := &connState{
		control: make(chan outbound),
		bulk:    make(chan outbound),
		stop:    make(chan struct{}),
	}
	cs.close()
	if err := c.writeMsg(cs, PingMsg{}); err != errConnClosed {
		t.Fatalf("got %v", err)
	}
}
