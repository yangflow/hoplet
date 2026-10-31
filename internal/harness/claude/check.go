package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// checkModel is the model the self-check runs: the cheapest, since only
// the protocol is under test.
const checkModel = "haiku"

// checkStepTimeout bounds each step of the self-check.
const checkStepTimeout = 90 * time.Second

// SelfCheck drives the claude binary at bin through this adapter on this
// machine, in a throwaway directory with the cheapest model, and returns
// nil when every step behaves as the adapter expects: a turn, a command
// approval and its denial, a permission mode change, an edit, an interrupt
// and a resume. It uses the user's claude login and a little of its quota.
// Each run leaves a short session in the user's claude history (keyed by a
// temporary directory). It never answers "always", so it changes no claude
// settings.
//
// The check needs claude to ask before running a shell command. If the
// user's own settings or hooks auto-allow it, the check fails safely and
// Hoplet stays on its current copy.
func SelfCheck(ctx context.Context, bin string, env []string) (err error) {
	dir, err := os.MkdirTemp("", "hoplet-check-")
	if err != nil {
		return fmt.Errorf("claude: self-check: %w", err)
	}
	defer os.RemoveAll(dir)
	h := &Harness{Binary: bin}
	c, err := startCheck(ctx, h, harness.Spec{Mode: proto.ModeConfirm, WorkDir: dir, Env: env, Model: checkModel})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.close()) }()

	// A command needs approval in confirm mode; denying it ends the turn.
	if err := c.run.Send(ctx, "Use the Bash tool to run exactly this command: mkdir hoplet-self-check-denied"); err != nil {
		return c.fail("send", err)
	}
	a, err := c.until("a command approval", func(p proto.Payload) bool {
		r, ok := p.(proto.ApprovalRequested)
		return ok && r.Kind == proto.ApprovalCommand
	})
	if err != nil {
		return err
	}
	if err := c.run.Respond(ctx, a.(proto.ApprovalRequested).ID, optionDeny); err != nil {
		return c.fail("deny", err)
	}
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(dir, "hoplet-self-check-denied")); statErr == nil {
		return errors.New("claude: self-check: a denied command ran")
	}
	native := c.run.NativeSession()
	if native == "" {
		return errors.New("claude: self-check: claude announced no session ID")
	}

	// After switching to auto_edit an edit needs no approval.
	if err := c.run.SetMode(ctx, proto.ModeAutoEdit); err != nil {
		return c.fail("set mode", err)
	}
	if err := c.run.Send(ctx, "Use the Write tool to create a file named check.txt containing the word ok. Do nothing else."); err != nil {
		return c.fail("send", err)
	}
	p, err := c.until("a file change", func(p proto.Payload) bool {
		if r, ok := p.(proto.ApprovalRequested); ok {
			return r.Kind == proto.ApprovalFileChange // reported below as the failure it is
		}
		_, ok := p.(proto.FileChange)
		return ok
	})
	if err != nil {
		return err
	}
	if _, ok := p.(proto.ApprovalRequested); ok {
		return errors.New("claude: self-check: an edit asked for approval after switching to auto_edit")
	}
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}

	// An interrupt ends a long turn.
	if err := c.run.Send(ctx, "Write the numbers from 1 to 400 as English words, one per line. Use no tools."); err != nil {
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
	if err := c.close(); err != nil {
		return err
	}

	// A new process resumes the same session.
	nc, err := startCheck(ctx, h, harness.Spec{Mode: proto.ModeConfirm, WorkDir: dir, Env: env, Model: checkModel, Resume: native})
	if err != nil {
		return err
	}
	c = nc
	if err := c.run.Send(ctx, "Reply with exactly the word: resumed"); err != nil {
		return c.fail("send", err)
	}
	if err := c.turnEnd(proto.TurnCompleted); err != nil {
		return err
	}
	if got := c.run.NativeSession(); got != native {
		return fmt.Errorf("claude: self-check: resuming %s continued session %q", native, got)
	}
	return nil
}

// check is one claude process under self-check.
type check struct {
	ctx context.Context
	io  *harness.ExecStdio
	run harness.Run
}

func startCheck(ctx context.Context, h *Harness, spec harness.Spec) (*check, error) {
	l, err := h.Launch(spec)
	if err != nil {
		return nil, fmt.Errorf("claude: self-check: %w", err)
	}
	io, err := harness.StartExec(ctx, l.Command, l.Env, l.Dir)
	if err != nil {
		return nil, fmt.Errorf("claude: self-check: %w", err)
	}
	run, err := h.Attach(ctx, spec, io)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("claude: self-check: %w", err), io.Close())
	}
	return &check{ctx: ctx, io: io, run: run}, nil
}

func (c *check) close() error {
	if c == nil || c.run == nil {
		return nil
	}
	err := c.run.Close()
	c.run = nil
	return err
}

func (c *check) fail(step string, err error) error {
	return fmt.Errorf("claude: self-check: %s: %w", step, err)
}

// until reads events until match accepts one (returned) or a turn ends.
func (c *check) until(what string, match func(proto.Payload) bool) (proto.Payload, error) {
	timer := time.NewTimer(checkStepTimeout)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return nil, fmt.Errorf("claude: self-check: %w", c.ctx.Err())
		case <-timer.C:
			return nil, fmt.Errorf("claude: self-check: no %s within %v", what, checkStepTimeout)
		case ev, ok := <-c.run.Events():
			if !ok {
				return nil, fmt.Errorf("claude: self-check: claude stopped before %s: %w", what, c.run.Err())
			}
			if ev.Payload == nil {
				continue
			}
			if e, isErr := ev.Payload.(proto.Error); isErr {
				return nil, fmt.Errorf("claude: self-check: before %s: %s", what, e.Message)
			}
			if match(ev.Payload) {
				return ev.Payload, nil
			}
			// Nothing here is for the user to answer: deny and keep
			// waiting, so a surprise approval fails the step, not hangs it.
			if r, isReq := ev.Payload.(proto.ApprovalRequested); isReq {
				if err := c.run.Respond(c.ctx, r.ID, optionDeny); err != nil {
					return nil, c.fail("deny", err)
				}
				continue
			}
			if end, ended := ev.Payload.(proto.TurnEnded); ended {
				return nil, fmt.Errorf("claude: self-check: the turn ended (%s %s) before %s", end.Reason, end.Message, what)
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
		return fmt.Errorf("claude: self-check: the turn ended %s (%s), want %s", got.Reason, got.Message, want)
	}
	return nil
}
