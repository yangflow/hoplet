package hermes

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/verified"
	"github.com/yangflow/hoplet/proto"
)

// checkStepTimeout bounds each step of the self-check.
const checkStepTimeout = 3 * time.Minute

// errCheckSetup marks a self-check failure before Hermes is involved, such
// as no temporary directory; it is not the version's fault.
var errCheckSetup = errors.New("hermes: self-check setup failed")

// Checker checks a hermes executable on this machine; SelfCheck is the
// real one.
type Checker func(ctx context.Context, bin string, env []string) error

// SelfCheck drives the user's Hermes through this adapter, over a bridge
// run in this process, in a throwaway directory: an edit asks and is
// denied, the same edit goes through without asking in full_auto, and a
// long answer is interrupted. It uses the user's Hermes model and leaves a
// short session in Hermes's history.
func SelfCheck(ctx context.Context, bin string, env []string) (err error) {
	dir, err := os.MkdirTemp("", "hoplet-check-")
	if err != nil {
		return errors.Join(errCheckSetup, err)
	}
	defer os.RemoveAll(dir)
	bridge := startPipe(ctx, BridgeConfig{Hermes: bin, Env: env})
	c := &check{ctx: ctx, run: newRun(bridge, harness.Spec{
		Session: proto.SessionID("hoplet-check-" + rand.Text()[:12]), WorkDir: dir, Env: env, Mode: proto.ModeConfirm,
	})}
	defer func() { err = errors.Join(err, c.run.Close()) }()
	file := filepath.Join(dir, "check.txt")
	const edit = "Use the write_file tool to create check.txt containing ok. Do nothing else."

	if err := c.run.Send(ctx, edit); err != nil {
		return c.fail("send", err)
	}
	p, err := c.until("an edit approval", func(p proto.Payload) bool {
		a, ok := p.(proto.ApprovalRequested)
		return ok && a.Kind == proto.ApprovalFileChange
	})
	if err != nil {
		return err
	}
	if err := c.run.Respond(ctx, p.(proto.ApprovalRequested).ID, "deny"); err != nil {
		return c.fail("deny", err)
	}
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}
	if _, err := os.Stat(file); err == nil {
		return errors.New("hermes: self-check: a denied edit was made")
	}

	if err := c.run.SetMode(ctx, proto.ModeFullAuto); err != nil {
		return c.fail("set mode", err)
	}
	if err := c.run.Send(ctx, edit); err != nil {
		return c.fail("send", err)
	}
	p, err = c.until("a file change", func(p proto.Payload) bool {
		switch v := p.(type) {
		case proto.FileChange:
			return true
		case proto.ApprovalRequested:
			return v.Kind == proto.ApprovalFileChange
		}
		return false
	})
	if err != nil {
		return err
	}
	if _, asked := p.(proto.ApprovalRequested); asked {
		return errors.New("hermes: self-check: an edit asked for approval in full_auto")
	}
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}

	if err := c.run.Send(ctx, "Write the numbers from 1 to 1000 as English words, one per line. Use no tools."); err != nil {
		return c.fail("send", err)
	}
	if _, err := c.until("streamed text", func(p proto.Payload) bool { _, ok := p.(proto.MessageDelta); return ok }); err != nil {
		return err
	}
	if err := c.run.Interrupt(ctx); err != nil {
		return c.fail("interrupt", err)
	}
	return c.turnEnd(proto.TurnInterrupted)
}

type check struct {
	ctx context.Context
	run *run
}

func (c *check) fail(step string, err error) error {
	return fmt.Errorf("hermes: self-check: %s: %w", step, err)
}

// until reads events until match accepts one, denying any other approval
// on the way; a turn that ends first is a failure.
func (c *check) until(what string, match func(proto.Payload) bool) (proto.Payload, error) {
	timer := time.NewTimer(checkStepTimeout)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return nil, fmt.Errorf("hermes: self-check: %w", c.ctx.Err())
		case <-timer.C:
			return nil, fmt.Errorf("hermes: self-check: timed out after %v waiting for %s", checkStepTimeout, what)
		case ev, ok := <-c.run.Events():
			if !ok {
				if err := c.run.Err(); err != nil {
					return nil, fmt.Errorf("hermes: self-check: the bridge stopped before %s: %w", what, err)
				}
				return nil, fmt.Errorf("hermes: self-check: the bridge stopped before %s", what)
			}
			if ev.Payload == nil {
				continue
			}
			if match(ev.Payload) {
				return ev.Payload, nil
			}
			switch p := ev.Payload.(type) {
			case proto.Error:
				return nil, fmt.Errorf("hermes: self-check: before %s: %s", what, p.Message)
			case proto.ApprovalRequested:
				_ = c.run.Respond(c.ctx, p.ID, "deny")
			case proto.TurnEnded:
				return nil, fmt.Errorf("hermes: self-check: the turn ended (%s %s) before %s", p.Reason, p.Message, what)
			}
		}
	}
}

func (c *check) turnEnd(want proto.TurnEndReason) error {
	p, err := c.until("the turn end", func(p proto.Payload) bool { _, ok := p.(proto.TurnEnded); return ok })
	if err != nil {
		return err
	}
	if got := p.(proto.TurnEnded); got.Reason != want {
		return fmt.Errorf("hermes: self-check: the turn ended %s (%s), want %s", got.Reason, got.Message, want)
	}
	return nil
}

// pipe is a bridge run in this process, seen as a Stdio.
type pipe struct {
	in     *io.PipeWriter
	lines  chan harness.Line
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	err    error // what Bridge returned; written before done closes
}

func startPipe(ctx context.Context, cfg BridgeConfig) *pipe {
	ctx, cancel := context.WithCancel(ctx)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &pipe{in: inW, lines: make(chan harness.Line, 64), cancel: cancel, done: make(chan struct{})}
	cfg.In, cfg.Out = inR, outW
	go func() {
		err := Bridge(ctx, cfg)
		if !errors.Is(err, context.Canceled) {
			p.err = err
		}
		outW.CloseWithError(err)
		inR.Close()
	}()
	go func() {
		defer close(p.done)
		defer close(p.lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, harness.MaxLine)
		for sc.Scan() {
			p.lines <- harness.Line{Kind: harness.Stdout, Data: append([]byte(nil), sc.Bytes()...)}
		}
		p.lines <- harness.Line{Kind: harness.Exit}
	}()
	return p
}

func (p *pipe) WriteLine(_ context.Context, line []byte) error {
	_, err := p.in.Write(append(append([]byte(nil), line...), '\n'))
	return err
}

func (p *pipe) Lines() <-chan harness.Line { return p.lines }
func (p *pipe) Signal(string) error        { return nil }

// Close stops the bridge, interrupting a turn it saw start, and returns the
// error the bridge ended with, if any.
func (p *pipe) Close() error {
	var err error
	p.once.Do(func() {
		p.cancel()
		p.in.Close()
		for range p.lines {
		}
		<-p.done
		err = p.err
	})
	return err
}

// Verify checks the Hermes the environment finds with check, unless its
// version is covered by this adapter's recordings or passed here before,
// and records the outcome in dir (Hoplet's directory for this adapter). A
// version that failed is checked at most once a day and not after
// verified.MaxAttempts self-check failures, unless force is set. It
// reports whether it checked; a cancelled check records nothing.
func Verify(ctx context.Context, dir string, env []string, check Checker, force bool) (verified.Outcome, bool, error) {
	bin, err := Find(env)
	if err != nil {
		return verified.Outcome{}, false, err
	}
	v, err := version(ctx, bin, env)
	if err != nil {
		return verified.Outcome{}, false, err
	}
	if recorded(v) || verified.Has(dir, v) {
		return verified.Outcome{Version: v}, false, nil
	}
	last, ok := verified.Last(dir)
	now := time.Now()
	due, failedBefore := verified.Due(last, ok, v, now)
	if !due && !force {
		return last, false, nil
	}
	err = check(ctx, bin, env)
	if err != nil && ctx.Err() != nil {
		return verified.Outcome{}, false, ctx.Err() // shutting down: not the version's fault
	}
	if errors.Is(err, errCheckSetup) {
		return verified.Outcome{}, false, err // a local failure: not the version's fault either
	}
	if err != nil {
		err = errors.Join(verified.ErrSelfCheck, err)
	} else if err := verified.Add(dir, v); err != nil {
		return verified.Outcome{}, false, err
	}
	o := verified.Next(last, failedBefore, "", v, err, now)
	if serr := verified.Save(dir, o); serr != nil {
		return o, true, serr
	}
	return o, true, nil
}
