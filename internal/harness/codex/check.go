package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/verified"
	"github.com/yangflow/hoplet/proto"
)

// checkStepTimeout bounds each step of the self-check.
const checkStepTimeout = 3 * time.Minute

// optionDecline is how a command or file approval is refused.
const optionDecline = "decline"

// errCheckSetup marks a self-check failure before Codex is involved; it is
// not the version's fault.
var errCheckSetup = errors.New("codex: self-check setup failed")

// Checker checks a codex executable on this machine; SelfCheck is the real
// one.
type Checker func(ctx context.Context, bin string, env []string) error

// SelfCheck drives bin through this adapter in a throwaway directory: a
// command asks and is denied, the same command runs without asking after a
// mode switch, a long answer is interrupted, and a new process resumes the
// thread. It uses the user's Codex login and leaves a short session in
// Codex's history.
func SelfCheck(ctx context.Context, bin string, env []string) (err error) {
	dir, err := os.MkdirTemp("", "hoplet-check-")
	if err != nil {
		return errors.Join(errCheckSetup, err)
	}
	defer os.RemoveAll(dir)
	spec := harness.Spec{WorkDir: dir, Env: env, Mode: proto.ModeConfirm, Effort: proto.EffortLow}
	c := &check{ctx: ctx}
	defer func() { err = errors.Join(err, c.stop()) }()
	if err := c.open(bin, spec); err != nil {
		return err
	}
	file := filepath.Join(dir, "check.txt")
	const touch = "Run the shell command `touch check.txt`. Do nothing else."

	if err := c.run.Send(ctx, touch); err != nil {
		return c.fail("send", err)
	}
	p, err := c.until("a command approval", func(p proto.Payload) bool {
		a, ok := p.(proto.ApprovalRequested)
		return ok && a.Kind == proto.ApprovalCommand
	})
	if err != nil {
		return err
	}
	if err := c.run.Respond(ctx, p.(proto.ApprovalRequested).ID, optionDecline); err != nil {
		return c.fail("deny", err)
	}
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}
	if _, err := os.Stat(file); err == nil {
		return errors.New("codex: self-check: a denied command ran")
	}

	if err := c.run.SetMode(ctx, proto.ModeAutoEdit); err != nil {
		return c.fail("set mode", err)
	}
	if err := c.run.Send(ctx, touch); err != nil {
		return c.fail("send", err)
	}
	c.strict = true
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}
	c.strict = false
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("codex: self-check: the command did not run after the mode switch: %w", err)
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
	if err := c.turnEnd(proto.TurnInterrupted); err != nil {
		return err
	}

	spec.Resume = c.run.NativeSession()
	if spec.Resume == "" {
		return errors.New("codex: self-check: no thread to resume")
	}
	if err := c.stop(); err != nil {
		return c.fail("stop", err)
	}
	if err := c.open(bin, spec); err != nil {
		return err
	}
	if err := c.run.Send(ctx, "Reply with the single word ok. Use no tools."); err != nil {
		return c.fail("send after resume", err)
	}
	return c.turnEnd(proto.TurnCompleted)
}

type check struct {
	ctx    context.Context
	run    *run
	end    func() error // stops the process of run
	strict bool         // an approval is a failure, not something to deny
}

func (c *check) fail(step string, err error) error {
	return fmt.Errorf("codex: self-check: %s: %w", step, err)
}

// open starts a process and connects a run to it.
func (c *check) open(bin string, spec harness.Spec) error {
	l, err := (&Harness{Binary: bin}).Launch(spec)
	if err != nil {
		return c.fail("launch", err)
	}
	p, end, err := startProcess(c.ctx, l, io.Discard)
	if err != nil {
		return c.fail("start", err)
	}
	r, err := newRun(p, spec)
	if err != nil {
		return errors.Join(c.fail("start", err), end())
	}
	c.run, c.end = r, end
	go r.loop()
	if err := r.start(c.ctx); err != nil {
		return c.fail("open the thread", err)
	}
	return nil
}

// stop ends the current run and its process.
func (c *check) stop() error {
	if c.run == nil {
		return nil
	}
	err := errors.Join(c.run.Close(), c.end())
	c.run = nil
	return err
}

// until reads events until match accepts one, denying any other approval
// on the way; a turn that ends first is a failure.
func (c *check) until(what string, match func(proto.Payload) bool) (proto.Payload, error) {
	timer := time.NewTimer(checkStepTimeout)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return nil, fmt.Errorf("codex: self-check: %w", c.ctx.Err())
		case <-timer.C:
			return nil, fmt.Errorf("codex: self-check: timed out after %v waiting for %s", checkStepTimeout, what)
		case ev, ok := <-c.run.events:
			if !ok {
				return nil, fmt.Errorf("codex: self-check: the run ended before %s", what)
			}
			if ev.Payload == nil {
				continue
			}
			if match(ev.Payload) {
				return ev.Payload, nil
			}
			switch p := ev.Payload.(type) {
			case proto.Error:
				return nil, fmt.Errorf("codex: self-check: before %s: %s", what, p.Message)
			case proto.ApprovalRequested:
				if c.strict {
					return nil, errors.New("codex: self-check: a command asked for approval after the mode switch")
				}
				_ = c.run.Respond(c.ctx, p.ID, optionDecline)
			case proto.TurnEnded:
				return nil, fmt.Errorf("codex: self-check: the turn ended (%s %s) before %s", p.Reason, p.Message, what)
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
		return fmt.Errorf("codex: self-check: the turn ended %s (%s), want %s", got.Reason, got.Message, want)
	}
	return nil
}

// Verify checks bin with check, unless its version is covered by this
// adapter's recordings or passed here before, and records the outcome. It
// reports whether it checked. Checks of one home run one at a time: waiting,
// if not nil, is called once when another check holds the turn.
func Verify(ctx context.Context, bin string, check func(ctx context.Context, bin string) error, waiting func()) (verified.Outcome, bool, error) {
	v, err := version(ctx, bin)
	if err != nil {
		return verified.Outcome{}, false, err
	}
	return verifyVersion(ctx, stateDir(bin), v, bin, recorded, check, waiting)
}

func verifyVersion(ctx context.Context, dir, v, bin string, known func(string) bool, check func(ctx context.Context, bin string) error, waiting func()) (verified.Outcome, bool, error) {
	if known(v) || verified.Has(dir, v) {
		return verified.Outcome{Version: v}, false, nil
	}
	unlock, err := verified.Lock(ctx, filepath.Join(dir, ".verify.lock"), waiting)
	if err != nil {
		return verified.Outcome{}, false, err
	}
	defer unlock()
	if verified.Has(dir, v) {
		return verified.Outcome{Version: v}, false, nil
	}
	last, ok := verified.Last(dir)
	now := time.Now()
	_, failedBefore := verified.Due(last, ok, v, now)
	err = check(ctx, bin)
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

// notice is what doctor says about the last check while Hoplet runs version
// current: a newer version that failed and that Hoplet did not move to.
func notice(o verified.Outcome, current string) (string, bool) {
	if o.Error == "" || o.Version == current {
		return "", false
	}
	return fmt.Sprintf("Codex %s failed the self-check on %s, so Hoplet stays on %s: %s",
		o.Version, o.At.Format(time.DateOnly), current, o.Error), true
}
