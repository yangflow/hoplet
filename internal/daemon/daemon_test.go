package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

var echoScript = fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}}

func TestSecondDaemonIsRefused(t *testing.T) {
	h, script := newHome(t, echoScript)
	d, err := Start(context.Background(), testConfig(h, script))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() }) // Close is idempotent; this covers an early Fatal
	if _, err := Start(context.Background(), testConfig(h, script)); !errors.Is(err, ErrRunning) {
		t.Fatalf("second start: %v, want ErrRunning", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	start(t, testConfig(h, script))
	for path, want := range map[string]os.FileMode{home.Socket(h): 0o600, home.TokenFile(h): 0o600, h: 0o700} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", path, fi.Mode().Perm(), want)
		}
	}
}

// rawDial opens a WebSocket to the home's daemon without saying hello.
func rawDial(t *testing.T, h string, header http.Header) (*websocket.Conn, error) {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", home.Socket(h))
		},
	}}
	ws, resp, err := websocket.Dial(timeout(t), "ws://hoplet"+proto.APIPath, &websocket.DialOptions{HTTPClient: hc, HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Cleanup(func() { _ = ws.CloseNow() })
	}
	return ws, err
}

func rawCall(t *testing.T, ws *websocket.Conn, id uint64, method string, params any) proto.ServerFrame {
	t.Helper()
	b, _ := json.Marshal(params)
	f, _ := json.Marshal(proto.ClientFrame{ID: id, Method: method, Params: b})
	if err := ws.Write(timeout(t), websocket.MessageText, f); err != nil {
		t.Fatal(err)
	}
	_, data, err := ws.Read(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	var out proto.ServerFrame
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHelloIsRequiredAndChecked(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	tok, err := home.ReadToken(h)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		method string
		params proto.HelloParams
		code   proto.ErrorCode
	}{
		"no hello":    {proto.MethodSessionsList, proto.HelloParams{}, proto.CodeUnauthorized},
		"wrong token": {proto.MethodHello, proto.HelloParams{ProtocolMajor: proto.ProtocolMajor, DeviceID: "d", Token: "nope"}, proto.CodeUnauthorized},
		"old client":  {proto.MethodHello, proto.HelloParams{ProtocolMajor: proto.ProtocolMajor - 1, DeviceID: "d", Token: tok}, proto.CodeVersion},
		"no device":   {proto.MethodHello, proto.HelloParams{ProtocolMajor: proto.ProtocolMajor, Token: tok}, proto.CodeBadRequest},
	} {
		ws, err := rawDial(t, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		f := rawCall(t, ws, 1, tc.method, tc.params)
		if f.Error == nil || f.Error.Code != tc.code {
			t.Errorf("%s: got %+v, want %s", name, f.Error, tc.code)
		}
		if _, _, err := ws.Read(timeout(t)); err == nil {
			t.Errorf("%s: the connection stayed open", name)
		}
	}

	ws, err := rawDial(t, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := rawCall(t, ws, 1, proto.MethodHello, proto.HelloParams{ProtocolMajor: proto.ProtocolMajor, DeviceID: "d", Token: tok})
	if f.Error != nil {
		t.Fatalf("hello: %v", f.Error)
	}
	var hr proto.HelloResult
	if err := json.Unmarshal(f.Result, &hr); err != nil || hr.Version != "test" || hr.ProtocolMajor != proto.ProtocolMajor {
		t.Fatalf("hello result %+v %v", hr, err)
	}
	if f := rawCall(t, ws, 2, proto.MethodHello, proto.HelloParams{}); f.Error == nil || f.Error.Code != proto.CodeBadRequest {
		t.Fatalf("second hello: %+v", f.Error)
	}
	if f := rawCall(t, ws, 3, "no.such", nil); f.Error == nil || f.Error.Code != proto.CodeBadRequest {
		t.Fatalf("unknown method: %+v", f.Error)
	}

	if _, err := rawDial(t, h, http.Header{"Origin": {"https://example.com"}}); err == nil {
		t.Fatal("a handshake with an Origin header was accepted")
	}
	cl, err := client.Dial(timeout(t), client.Options{Socket: home.Socket(h), Token: tok, DeviceID: "d"})
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	cl.Close()
}

// rawWrite writes a frame without reading anything back.
func rawWrite(t *testing.T, ws *websocket.Conn, f proto.ClientFrame) {
	t.Helper()
	b, _ := json.Marshal(f)
	if err := ws.Write(timeout(t), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func rawRead(t *testing.T, ws *websocket.Conn) proto.ServerFrame {
	t.Helper()
	_, data, err := ws.Read(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	var f proto.ServerFrame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func rawHello(t *testing.T, h string) *websocket.Conn {
	t.Helper()
	tok, err := home.ReadToken(h)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := rawDial(t, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := rawCall(t, ws, 1, proto.MethodHello, proto.HelloParams{ProtocolMajor: proto.ProtocolMajor, DeviceID: "raw", Token: tok}); f.Error != nil {
		t.Fatal(f.Error)
	}
	return ws
}

func TestCancelRightAfterSubscribeEndsTheSubscription(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	ws := rawHello(t, h)
	catalog, _ := json.Marshal(proto.CatalogParams{})
	session, _ := json.Marshal(proto.SessionParams{Session: sess.ID})
	for id, sub := range map[uint64]proto.ClientFrame{
		7: {ID: 7, Method: proto.MethodSubscribeCatalog, Params: catalog},
		8: {ID: 8, Method: proto.MethodSubscribeSession, Params: session},
	} {
		rawWrite(t, ws, sub)
		rawWrite(t, ws, proto.ClientFrame{Cancel: true, Sub: id})
		if f := rawRead(t, ws); f.ID != id || f.Error != nil || f.Result == nil {
			t.Fatalf("%s: response %+v", sub.Method, f)
		}
		for {
			f := rawRead(t, ws)
			if f.Sub != id {
				t.Fatalf("%s: unexpected frame %+v", sub.Method, f)
			}
			if f.End {
				if f.Error != nil {
					t.Fatalf("%s: end with error %+v", sub.Method, f.Error)
				}
				break
			}
		}
	}
}

func TestCancelBeforeHelloClosesTheConnection(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	ws, err := rawDial(t, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	rawWrite(t, ws, proto.ClientFrame{Cancel: true, Sub: 1})
	for {
		if _, _, err := ws.Read(timeout(t)); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("the connection stayed open")
			}
			return
		}
	}
}

func TestSilentConnectionIsClosed(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.lim = limits{hello: 200 * time.Millisecond}
	start(t, cfg)
	ws, err := rawDial(t, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(timeout(t)); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a connection that never said hello stayed open: %v", err)
	}
}

func TestHomeIsTightenedTo0700(t *testing.T) {
	h, script := newHome(t, echoScript)
	if err := os.Chmod(h, 0o755); err != nil {
		t.Fatal(err)
	}
	start(t, testConfig(h, script))
	fi, err := os.Stat(h)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("home mode %v %v", fi.Mode().Perm(), err)
	}
}

func TestArchivedSessionRefusesSendsAsConflict(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	if err := c.Archive(timeout(t), sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "x", Key: "k"}); apiCode(err) != proto.CodeConflict {
		t.Fatalf("send to an archived session: %v", err)
	}
}

func TestQueuedMessageWithReceiptIsDequeuedNotResent(t *testing.T) {
	h, script := newHome(t, echoScript)
	d := start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "hi", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(1))
	// As if the daemon had died after the send and before the dequeue.
	if _, _, err := d.store.Enqueue(timeout(t), sess.ID, "k1", "hi"); err != nil {
		t.Fatal(err)
	}
	// The drainer's send records any second message before it dequeues, so
	// once the row is gone the log shows whether the message was resent.
	waitCond(t, "the queued copy to be dequeued", func() bool {
		q, err := d.store.Queue(context.Background(), sess.ID)
		return err == nil && len(q) == 0
	})
	evs, err := d.store.Events(context.Background(), sess.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	users, turns := 0, 0
	for _, ev := range evs {
		if ev.Type == proto.TypeTurnStarted {
			turns++
		}
		if ev.Type != proto.TypeMessage {
			continue
		}
		p, err := ev.Decode()
		if err != nil {
			t.Fatal(err)
		}
		if m := p.(proto.Message); m.Role == proto.RoleUser && m.Text == "hi" {
			users++
		}
	}
	if users != 1 || turns != 1 {
		t.Fatalf("%d user messages, %d turns:%s", users, turns, describe(evs))
	}
}

func TestSendStreamsTheTurnAndTheCatalog(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")

	cat, err := c.SubscribeCatalog(timeout(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	// next decodes into a fresh item: decoding into a used one would keep
	// fields the new item omits.
	next := func(s *client.Subscription) (proto.CatalogItem, error) {
		var ci proto.CatalogItem
		return ci, s.Next(timeout(t), &ci)
	}
	ci, err := next(cat)
	if err != nil || !ci.Snapshot || len(ci.Sessions) != 0 || ci.Version != 0 {
		t.Fatalf("first catalog item %+v %v", ci, err)
	}
	sess := create(t, c, h)
	if ci, err = next(cat); err != nil || ci.Snapshot || len(ci.Sessions) != 1 || ci.Sessions[0].ID != sess.ID || ci.Version != sess.Version {
		t.Fatalf("catalog item after create %+v %v", ci, err)
	}

	w := watch(t, c, sess.ID, 0)
	if it := w.next(); it.Queue == nil || len(it.Queue.Messages) != 0 || len(it.Events) != 0 {
		t.Fatalf("first item of a new session %+v", it)
	}
	res, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "hello", Key: "k1"})
	if err != nil || res.Queued || res.Turn != 1 || res.Seq != 1 {
		t.Fatalf("send %+v %v", res, err)
	}
	again, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "hello", Key: "k1"})
	if err != nil || again != res {
		t.Fatalf("retried send %+v %v, want %+v", again, err, res)
	}
	w.until(turnEnded(1))
	if got := w.texts(1); len(got) != 2 || got[0] != "hello" || got[1] != "hello" {
		t.Fatalf("turn 1 messages %v", got)
	}

	// A client that was away catches up from its catalog version.
	cat2, err := c.SubscribeCatalog(timeout(t), sess.Version)
	if err != nil {
		t.Fatal(err)
	}
	if ci, err = next(cat2); err != nil || ci.Snapshot || len(ci.Sessions) != 1 || ci.Sessions[0].Status != proto.StatusIdle || ci.Sessions[0].LastSeq == 0 {
		t.Fatalf("catch-up catalog item %+v %v", ci, err)
	}
	if err := cat2.Cancel(timeout(t)); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := next(cat2)
		if errors.Is(err, client.ErrEnded) {
			break
		}
		if err != nil {
			t.Fatalf("after cancel: %v", err)
		}
	}
	// A version from the future is answered with a snapshot.
	cat3, err := c.SubscribeCatalog(timeout(t), 1<<40)
	if err != nil {
		t.Fatal(err)
	}
	if ci, err = next(cat3); err != nil || !ci.Snapshot || len(ci.Sessions) != 1 {
		t.Fatalf("future version %+v %v", ci, err)
	}
}

var approvalTurn = fake.Turn{Steps: []fake.Step{
	{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []fake.Option{
		{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "no", Label: "Deny", Scope: "deny"},
	}}},
	{AwaitApproval: "a1"},
}}

func TestTwoClientsOneApproval(t *testing.T) {
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn}})
	start(t, testConfig(h, script))
	a, b := dial(t, h, "device:a"), dial(t, h, "device:b")
	sess := create(t, a, h)
	w := watch(t, a, sess.ID, 0)
	if _, err := a.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "go", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	errs := make(chan error, 2)
	for _, r := range []struct {
		c   *client.Client
		opt string
	}{{a, "once"}, {b, "no"}} {
		go func() {
			errs <- r.c.Respond(timeout(t), proto.RespondParams{Session: sess.ID, Approval: "a1", Option: r.opt, Key: "r-" + r.opt})
		}()
	}
	var won, lost int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			won++
		case apiCode(err) == proto.CodeConflict:
			lost++
		default:
			t.Fatalf("respond: %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("%d answers won, %d lost; want one each", won, lost)
	}
	w.until(turnEnded(1))
	if n := w.count(proto.TypeApprovalResolved); n != 1 {
		t.Fatalf("%d resolutions", n)
	}
}

func TestCommandsReachTheHarness(t *testing.T) {
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn}})
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	sess := create(t, c, h)
	if err := c.Interrupt(ctx, sess.ID); apiCode(err) != proto.CodeNotRunning {
		t.Fatalf("interrupt before start: %v", err)
	}
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "go", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	if err := c.Steer(ctx, proto.SteerParams{Session: sess.ID, Text: "faster"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPermission(ctx, proto.SetPermissionParams{Session: sess.ID, Permission: proto.ModeFullAuto}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return len(w.texts(1)) >= 3 })
	if got := w.texts(1); got[1] != "steer:faster" || got[2] != "mode:full_auto" {
		t.Fatalf("turn 1 messages %v", got)
	}
	if err := c.Respond(ctx, proto.RespondParams{Session: sess.ID, Approval: "a1", Option: "maybe", Key: "r1"}); apiCode(err) != proto.CodeInvalid {
		t.Fatalf("respond with a bad option: %v", err)
	}
	if err := c.Interrupt(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(1))
	if err := c.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Archive(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	ss, err := c.Sessions(ctx)
	if err != nil || len(ss) != 1 || ss[0].ArchivedAt == nil || ss[0].Permission != proto.ModeFullAuto {
		t.Fatalf("sessions %+v %v", ss, err)
	}
}

func TestErrorsAreClassified(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	for name, tc := range map[string]struct {
		err  error
		code proto.ErrorCode
	}{
		"unknown session": {func() error {
			_, err := c.Send(ctx, proto.SendParams{Session: "nope", Text: "x", Key: "k"})
			return err
		}(), proto.CodeNotFound},
		"no key": {func() error {
			_, err := c.Send(ctx, proto.SendParams{Session: "nope", Text: "x"})
			return err
		}(), proto.CodeInvalid},
		"no text": {func() error {
			_, err := c.Send(ctx, proto.SendParams{Session: "nope", Key: "k", Text: ""})
			return err
		}(), proto.CodeInvalid},
		"unknown harness": {func() error {
			_, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModePersonal, Harness: "nope"})
			return err
		}(), proto.CodeInvalid},
		"relative directory": {func() error {
			_, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModeCode, Harness: "fake", Directory: "work"})
			return err
		}(), proto.CodeInvalid},
		"code without directory": {func() error {
			_, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModeCode, Harness: "fake"})
			return err
		}(), proto.CodeInvalid},
		"subscribe unknown session": {func() error {
			_, err := c.SubscribeSession(ctx, proto.SessionParams{Session: "nope"})
			return err
		}(), proto.CodeNotFound},
		"send key too long": {func() error {
			_, err := c.Send(ctx, proto.SendParams{Session: "nope", Text: "x", Key: strings.Repeat("k", store.MaxQueueKey+1)})
			return err
		}(), proto.CodeInvalid},
		"send key with #": {func() error {
			_, err := c.Send(ctx, proto.SendParams{Session: "nope", Text: "x", Key: "m2#1"})
			return err
		}(), proto.CodeInvalid},
		"events of unknown session": {func() error {
			_, err := c.EventsBefore(ctx, proto.EventsParams{Session: "nope", BeforeSeq: 10})
			return err
		}(), proto.CodeNotFound},
		"queue of unknown session": {func() error {
			_, err := c.Queue(ctx, "nope")
			return err
		}(), proto.CodeNotFound},
	} {
		if got := apiCode(tc.err); got != tc.code {
			t.Errorf("%s: %v, want %s", name, tc.err, tc.code)
		}
	}
	if _, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModePersonal, Harness: "fake"}); err != nil {
		t.Fatalf("personal session without a directory: %v", err)
	}
}

func TestQueueWaitsForTheTurnAndKeepsOrder(t *testing.T) {
	echo := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn, echo, echo}})
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	sess := create(t, c, h)
	w := watch(t, c, sess.ID, 0)
	w.next()
	if _, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "first", Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	for _, k := range []string{"m2", "m3", "m4"} {
		res, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "text " + k, Key: k})
		if err != nil || !res.Queued {
			t.Fatalf("send %s while busy: %+v %v", k, res, err)
		}
	}
	if res, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: "text m2", Key: "m2"}); err != nil || !res.Queued {
		t.Fatalf("retried queued send %+v %v", res, err)
	}
	if err := c.MoveQueued(ctx, proto.QueueMoveParams{Session: sess.ID, Key: "m3", Position: 0}); err != nil {
		t.Fatal(err)
	}
	if err := c.EditQueued(ctx, proto.QueueEditParams{Session: sess.ID, Key: "m2", Text: "edited"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteQueued(ctx, proto.QueueKeyParams{Session: sess.ID, Key: "m4"}); err != nil {
		t.Fatal(err)
	}
	q, err := c.Queue(ctx, sess.ID)
	if err != nil || len(q) != 2 || q[0].Key != "m3" || q[1].Key != "m2" || q[1].Text != "edited" {
		t.Fatalf("queue %+v %v", q, err)
	}
	w.until(func(w *watcher) bool { return len(w.queue) == 2 && w.queue[1].Text == "edited" })

	if err := c.Respond(ctx, proto.RespondParams{Session: sess.ID, Approval: "a1", Option: "once", Key: "r1"}); err != nil {
		t.Fatal(err)
	}
	w.until(turnEnded(3))
	if got := w.texts(2); len(got) != 2 || got[0] != "text m3" {
		t.Fatalf("turn 2 messages %v", got)
	}
	if got := w.texts(3); len(got) != 2 || got[0] != "edited" {
		t.Fatalf("turn 3 messages %v", got)
	}
	w.until(func(w *watcher) bool { return len(w.queue) == 0 })
}

func TestFailedQueuedMessageHoldsTheQueue(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	// A harness that cannot launch: every send to it fails.
	cfg.Harnesses["broken"] = &fake.Harness{}
	d := start(t, cfg)
	c := dial(t, h, "device:a")
	ctx := timeout(t)
	sess, err := c.Create(ctx, proto.CreateParams{Mode: proto.ModePersonal, Harness: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	w := watch(t, c, sess.ID, 0)
	w.next()
	// Queued as if a turn had been running when it was sent.
	for _, k := range []string{"m1", "m2"} {
		if _, _, err := d.store.Enqueue(ctx, sess.ID, k, "text "+k); err != nil {
			t.Fatal(err)
		}
	}
	w.until(func(w *watcher) bool { return len(w.queue) == 2 && w.queue[0].Error != "" })
	if w.queue[1].Error != "" {
		t.Fatalf("the second message was tried too: %+v", w.queue)
	}
	if err := c.EditQueued(ctx, proto.QueueEditParams{Session: sess.ID, Key: "m1", Text: "retry"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.queue[0].Text == "retry" && w.queue[0].Error != "" })
	if err := c.DeleteQueued(ctx, proto.QueueKeyParams{Session: sess.ID, Key: "m1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return len(w.queue) == 1 && w.queue[0].Key == "m2" && w.queue[0].Error != "" })
}

// appendPayloads appends events straight to the store.
func appendPayloads(t *testing.T, d *Daemon, id proto.SessionID, turn uint32, ps ...proto.Payload) {
	t.Helper()
	req := store.AppendRequest{Session: id, Turn: turn}
	for _, p := range ps {
		req.Entries = append(req.Entries, store.Entry{Payload: p})
	}
	if _, err := d.store.Append(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

// appendTurn appends turn n with five events: start, three messages, end.
func appendTurn(t *testing.T, d *Daemon, id proto.SessionID, n uint32) {
	t.Helper()
	msg := proto.Message{ID: "m", Role: proto.RoleAssistant, Text: "x"}
	appendPayloads(t, d, id, n, proto.TurnStarted{}, msg, msg, msg, proto.TurnEnded{Reason: proto.TurnCompleted})
}

func TestSessionCatchUpReplaysSmallGapsAndSnapshotsLargeOnes(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.lim = limits{gapEvents: 5, gapBytes: 1 << 20, snapshotTurns: 2}
	d := start(t, cfg)
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	for n := uint32(1); n <= 4; n++ {
		appendTurn(t, d, sess.ID, n) // seqs 1..20; turn 3 starts at 11
	}

	w := watch(t, c, sess.ID, 17)
	if it := w.next(); it.Snapshot != nil || len(it.Events) != 3 || it.Events[0].Seq != 18 {
		t.Fatalf("small gap: %+v", it)
	}
	next := uint32(5)
	for name, after := range map[string]uint64{"many events": 0, "cursor past the end": 99} {
		w := watch(t, c, sess.ID, after)
		it := w.next()
		if it.Snapshot == nil || it.Events != nil {
			t.Fatalf("%s: no snapshot: %+v", name, it)
		}
		evs := it.Snapshot.Events
		last := it.Snapshot.Session.LastSeq
		if uint64(len(evs)) != 10 || evs[len(evs)-1].Seq != last || !it.Snapshot.More {
			t.Fatalf("%s: snapshot of %d events %d..%d, last seq %d, more %v", name, len(evs), evs[0].Seq, evs[len(evs)-1].Seq, last, it.Snapshot.More)
		}
		// Live events follow the snapshot without a gap.
		appendTurn(t, d, sess.ID, next)
		next++
		w.until(func(w *watcher) bool { return len(w.events) == 15 })
		if w.events[10].Seq != last+1 {
			t.Fatalf("%s: live events start at %d, want %d", name, w.events[10].Seq, last+1)
		}
	}

	// Few but large events also get a snapshot.
	cfg2 := cfg
	cfg2.lim.gapBytes = 100
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	start(t, cfg2)
	c2 := dial(t, h, "device:a")
	w = watch(t, c2, sess.ID, 25)
	if it := w.next(); it.Snapshot == nil {
		t.Fatalf("large gap of few events: %+v", it)
	}
}

func TestSlowClientIsDisconnected(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.lim = limits{page: 1, outbound: 64 << 10}
	d := start(t, cfg)
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	tok, err := home.ReadToken(h)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := rawDial(t, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(1 << 20)
	if f := rawCall(t, ws, 1, proto.MethodHello, proto.HelloParams{ProtocolMajor: proto.ProtocolMajor, DeviceID: "slow", Token: tok}); f.Error != nil {
		t.Fatal(f.Error)
	}
	rawCall(t, ws, 2, proto.MethodSubscribeSession, proto.SessionParams{Session: sess.ID})
	if d.srv.active.Load() != 2 {
		t.Fatalf("%d connections", d.srv.active.Load())
	}
	// The slow client reads nothing more while 100 events of 32 KiB commit.
	big := strings.Repeat("x", 32<<10)
	for range 100 {
		appendPayloads(t, d, sess.ID, 0, proto.Message{ID: "m", Role: proto.RoleAssistant, Text: big})
	}
	waitCond(t, "the slow connection to close", func() bool { return d.srv.active.Load() == 1 })
	items := 0
	for {
		_, data, err := ws.Read(timeout(t))
		if err != nil {
			break
		}
		var f proto.ServerFrame
		if json.Unmarshal(data, &f) == nil && f.Sub == 2 {
			items++
		}
	}
	if items >= 100 {
		t.Fatalf("the slow client got all %d items", items)
	}
	// The other client is unaffected.
	if _, err := c.Sessions(timeout(t)); err != nil {
		t.Fatal(err)
	}
}
