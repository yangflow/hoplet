package main

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/proto"
)

const sessionUsage = `usage: hoplet session <command> [flags]

commands:
  new      [--mode code|personal] --harness KIND [--dir DIR] [--permission MODE] [--plan]
           [--fast] [--effort LEVEL] [--title T] [PROMPT]
  list     [--all]
  send     ID TEXT
  watch    ID [--after SEQ]
  wait     ID [--for idle|blocked|done] [--timeout D]
  approve  ID [APPROVAL] --option OPTION
  set      ID [--permission MODE] [--plan on|off] [--fast on|off] [--effort LEVEL]
  interrupt ID     end the running turn
  steer    ID TEXT  add TEXT to the running turn (harnesses that support it)
  stop     ID      stop the session's harness process
  archive  ID
  diff     ID [--stat] [--repo PATH]   what the session changed in its git repositories

ID may be a unique prefix of at least 4 characters. Every command takes
--json for machine-readable output.

diff compares every git repository under the session's directory with how it
was when the session was created, and prints a patch per repository (--stat:
the changed files with line counts). --repo limits it to one repository, by
its path under the directory. Hoplet writes nothing to your repositories; it keeps its own copy of
their content (raw bytes, no filters) under its home directory.

wait conditions: idle (no turn running and nothing queued), blocked
(an approval, a question or a failed queued message waits on you), done
(idle, blocked, errored or archived: nothing happens until you act).
idle never comes for an errored session; use done.

permission modes: confirm (ask before changes and commands), auto_edit (edit
freely, ask before commands), auto_review (the harness asks only for risky
actions), full_auto (never ask). A harness lacking a mode runs the nearest
stricter one. Plan mode stops all changes until you approve a plan. Fast
mode gives faster output at a higher cost. Effort levels: low, medium, high,
xhigh, max, ultra; each harness offers a subset. set applies its changes in
the order permission, plan, fast, effort and stops at the first that fails.
`

// commandTimeout bounds one request of a command that does not follow a
// session.
const commandTimeout = 30 * time.Second

// createTimeout bounds creating a session, which waits for its baseline (up
// to a minute), and asking for a diff, which may snapshot large repositories.
const createTimeout = 2 * time.Minute

func (a app) session(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) == 0 {
			fmt.Fprint(a.stderr, sessionUsage)
			return 2
		}
		fmt.Fprint(a.stdout, sessionUsage)
		return 0
	}
	cmds := map[string]func([]string) int{
		"new": a.sessionNew, "list": a.sessionList, "send": a.sessionSend, "watch": a.sessionWatch,
		"wait": a.sessionWait, "approve": a.sessionApprove, "set": a.sessionSet, "archive": a.sessionArchive,
		"interrupt": a.sessionInterrupt, "steer": a.sessionSteer, "stop": a.sessionStop, "diff": a.sessionDiff,
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "hoplet session: unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:])
}

// withClient runs fn with a connection to the daemon and a context that a
// SIGINT or SIGTERM cancels; timeout 0 means no deadline.
func (a app) withClient(name string, timeout time.Duration, fn func(ctx context.Context, c *client.Client) error) int {
	return a.withClientTimeout(name, timeout, "", fn)
}

// withClientTimeout is withClient for a command whose timeout is part of
// its contract: when the deadline passes, at any step, it prints
// timedOut and exits 3.
func (a app) withClientTimeout(name string, timeout time.Duration, timedOut string, fn func(ctx context.Context, c *client.Client) error) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	expired := func() bool { return timedOut != "" && errors.Is(ctx.Err(), context.DeadlineExceeded) }
	c, err := connect(ctx)
	if err != nil {
		if expired() {
			fmt.Fprintf(a.stderr, "hoplet %s: %s\n", name, timedOut)
			return 3
		}
		return a.fail(name, err)
	}
	defer c.Close()
	if err := fn(ctx, c); err != nil {
		if expired() {
			fmt.Fprintf(a.stderr, "hoplet %s: %s\n", name, timedOut)
			return 3
		}
		var ce codeError
		if errors.As(err, &ce) {
			fmt.Fprintf(a.stderr, "hoplet %s: %v\n", name, ce.err)
			return ce.code
		}
		return a.fail(name, err)
	}
	return 0
}

// codeError is an error that exits with a code other than 1.
type codeError struct {
	code int
	err  error
}

func (e codeError) Error() string { return e.err.Error() }

func (a app) sessionNew(args []string) int {
	fs := a.flags("session new", sessionUsage)
	mode := fs.String("mode", "code", "code or personal")
	kind := fs.String("harness", "", "harness kind")
	dir := fs.String("dir", ".", "working directory of a code session")
	perm := fs.String("permission", "", "confirm, auto_edit, auto_review or full_auto (default: auto_review for code, confirm for personal)")
	plan := fs.Bool("plan", false, "start in plan mode")
	fast := fs.Bool("fast", false, "start in fast mode")
	effort := fs.String("effort", "", "low, medium, high, xhigh, max or ultra (default: the harness's)")
	title := fs.String("title", "", "title")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, -1); !ok {
		return code
	}
	if *kind == "" {
		fmt.Fprintln(a.stderr, "hoplet session new: --harness is required")
		return 2
	}
	p := proto.CreateParams{
		Mode: proto.Mode(*mode), Harness: *kind, Permission: proto.PermissionMode(*perm),
		Plan: *plan, Fast: *fast, Effort: proto.Effort(*effort), Title: *title,
	}
	if p.Mode == proto.ModeCode {
		abs, err := filepath.Abs(*dir)
		if err != nil {
			return a.fail("session new", err)
		}
		p.Directory = abs
	}
	prompt := strings.Join(fs.Args(), " ")
	return a.withClient("session new", createTimeout, func(ctx context.Context, c *client.Client) error {
		sess, err := c.Create(ctx, p)
		if err != nil {
			return err
		}
		out := struct {
			Session proto.Session    `json:"session"`
			Sent    *proto.SendReply `json:"sent,omitempty"`
		}{Session: sess}
		if prompt != "" {
			r, err := c.Send(ctx, proto.SendParams{Session: sess.ID, Text: prompt, Key: newKey()})
			if err != nil {
				return fmt.Errorf("created %s, but sending the prompt failed: %w", sess.ID, err)
			}
			out.Sent = &r
		}
		if *asJSON {
			a.printJSON(out)
			return nil
		}
		fmt.Fprintln(a.stdout, sess.ID)
		return nil
	})
}

func (a app) sessionList(args []string) int {
	fs := a.flags("session list", sessionUsage)
	all := fs.Bool("all", false, "include archived sessions")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	return a.withClient("session list", commandTimeout, func(ctx context.Context, c *client.Client) error {
		ss, err := c.Sessions(ctx)
		if err != nil {
			return err
		}
		shown := []proto.Session{}
		for _, s := range ss {
			if *all || s.ArchivedAt == nil {
				shown = append(shown, s)
			}
		}
		if *asJSON {
			a.printJSON(shown)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tMODE\tHARNESS\tUPDATED\tTITLE / DIRECTORY")
		for _, s := range shown {
			what := s.Title
			if what == "" {
				what = s.Directory
			}
			status := string(s.Status)
			if s.ArchivedAt != nil {
				status = "archived"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, status, s.Mode, s.Harness.Kind, s.UpdatedAt.Local().Format("01-02 15:04"), what)
		}
		return tw.Flush()
	})
}

func (a app) sessionSend(args []string) int {
	fs := a.flags("session send", sessionUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 2, -1); !ok {
		return code
	}
	return a.withClient("session send", commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		r, err := c.Send(ctx, proto.SendParams{Session: id, Text: strings.Join(fs.Args()[1:], " "), Key: newKey()})
		if err != nil {
			return err
		}
		switch {
		case *asJSON:
			a.printJSON(r)
		case r.Queued:
			fmt.Fprintln(a.stdout, "queued behind the running turn")
		default:
			fmt.Fprintf(a.stdout, "sent: turn %d\n", r.Turn)
		}
		return nil
	})
}

func (a app) sessionWatch(args []string) int {
	fs := a.flags("session watch", sessionUsage)
	after := fs.Uint64("after", 0, "start after this event sequence number")
	asJSON := fs.Bool("json", false, "print each event as a JSON line")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("session watch", 0, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		sub, err := c.SubscribeSession(ctx, proto.SessionParams{Session: id, AfterSeq: *after})
		if err != nil {
			return err
		}
		for {
			var it proto.SessionItem
			if err := sub.Next(ctx, &it); err != nil {
				if ctx.Err() != nil {
					return nil // interrupted
				}
				return err
			}
			evs := it.Events
			if it.Snapshot != nil {
				evs = it.Snapshot.Events
				if it.Snapshot.More {
					fmt.Fprintln(a.stderr, "(earlier events not shown)")
				}
			}
			for _, ev := range evs {
				if *asJSON {
					if err := writeJSONLine(a.stdout, ev); err != nil {
						return err
					}
				} else if line := render(ev); line != "" {
					fmt.Fprintln(a.stdout, line)
				}
			}
		}
	})
}

// waitCondition is a `session wait --for` value.
type waitCondition string

const (
	waitIdle    waitCondition = "idle"
	waitBlocked waitCondition = "blocked"
	waitDone    waitCondition = "done"
)

// progress tracks, from a session's events, whether the latest message
// still has a turn to run: a message is recorded under the turn it starts
// before that turn begins.
type progress struct {
	messageTurn, endedTurn uint32
}

func (p *progress) add(evs []proto.Event) {
	for _, ev := range evs {
		switch ev.Type {
		case proto.TypeMessage:
			if m, err := ev.Decode(); err == nil && m.(proto.Message).Role == proto.RoleUser {
				p.messageTurn = max(p.messageTurn, ev.Turn)
			}
		case proto.TypeTurnEnded:
			p.endedTurn = max(p.endedTurn, ev.Turn)
		}
	}
}

// reached reports whether the session meets cond.
func reached(cond waitCondition, st proto.SessionState, p progress) bool {
	s := st.Session
	failedHead := len(st.Queue) > 0 && st.Queue[0].Error != ""
	blocked := s.Status == proto.StatusWaiting || failedHead
	idle := s.Status == proto.StatusIdle && len(st.Queue) == 0 && p.endedTurn >= p.messageTurn
	switch cond {
	case waitIdle:
		return idle
	case waitBlocked:
		return blocked
	default:
		return idle || blocked || s.Status == proto.StatusErrored || s.ArchivedAt != nil
	}
}

func (a app) sessionWait(args []string) int {
	fs := a.flags("session wait", sessionUsage)
	forFlag := fs.String("for", string(waitDone), "idle, blocked or done")
	timeout := fs.Duration("timeout", 0, "give up after this long (exit 3); 0 waits forever")
	asJSON := fs.Bool("json", false, "print the final state as JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	if *timeout < 0 {
		fmt.Fprintln(a.stderr, "hoplet session wait: --timeout must not be negative")
		return 2
	}
	cond := waitCondition(*forFlag)
	if cond != waitIdle && cond != waitBlocked && cond != waitDone {
		fmt.Fprintf(a.stderr, "hoplet session wait: --for %q: want idle, blocked or done\n", *forFlag)
		return 2
	}
	return a.withClientTimeout("session wait", *timeout, "timed out waiting for "+string(cond), func(ctx context.Context, c *client.Client) error {
		return a.waitFor(ctx, c, fs.Arg(0), cond, *asJSON)
	})
}

// recheckEvery bounds how long wait goes without looking at the session
// state, for changes that produce no subscription item.
const recheckEvery = 2 * time.Second

func (a app) waitFor(ctx context.Context, c *client.Client, arg string, cond waitCondition, asJSON bool) error {
	id, err := resolveID(ctx, c, arg)
	if err != nil {
		return err
	}
	sub, err := c.SubscribeSession(ctx, proto.SessionParams{Session: id})
	if err != nil {
		return err
	}
	var p progress
	for {
		var it proto.SessionItem
		nctx, cancel := context.WithTimeout(ctx, recheckEvery)
		err := sub.Next(nctx, &it)
		cancel()
		switch {
		case err == nil:
			if it.Snapshot != nil {
				p = progress{}
				p.add(it.Snapshot.Events)
			}
			p.add(it.Events)
		case ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded):
			// No item for a while: look at the state anyway.
		default:
			return err
		}
		st, err := c.Session(ctx, id)
		if err != nil {
			return err
		}
		if !reached(cond, st, p) {
			continue
		}
		if asJSON {
			a.printJSON(st)
		} else {
			fmt.Fprintln(a.stdout, describeState(st))
		}
		return nil
	}
}

func describeState(st proto.SessionState) string {
	s := string(st.Session.Status)
	if st.Session.ArchivedAt != nil {
		s = "archived"
	}
	for _, p := range st.Pending {
		s += fmt.Sprintf("; approval %s: %s", p.ID, p.Title)
	}
	if n := len(st.Queue); n > 0 {
		s += fmt.Sprintf("; %d queued", n)
		if e := st.Queue[0].Error; e != "" {
			s += " (the next one failed: " + e + ")"
		}
	}
	return s
}

func (a app) sessionApprove(args []string) int {
	fs := a.flags("session approve", sessionUsage)
	option := fs.String("option", "", "the option to answer with")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 2); !ok {
		return code
	}
	return a.withClient("session approve", commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		st, err := c.Session(ctx, id)
		if err != nil {
			return err
		}
		var req *proto.ApprovalRequested
		switch {
		case fs.NArg() > 1:
			for i := range st.Pending {
				if st.Pending[i].ID == fs.Arg(1) {
					req = &st.Pending[i]
				}
			}
			if req == nil {
				return fmt.Errorf("no pending approval %q", fs.Arg(1))
			}
		case len(st.Pending) == 1:
			req = &st.Pending[0]
		case len(st.Pending) == 0:
			return errors.New("nothing waits for approval")
		default:
			ids := make([]string, len(st.Pending))
			for i, p := range st.Pending {
				ids[i] = p.ID
			}
			return codeError{2, fmt.Errorf("several approvals wait (%s); name one", strings.Join(ids, ", "))}
		}
		if *option == "" {
			opts := make([]string, len(req.Options))
			for i, o := range req.Options {
				opts[i] = fmt.Sprintf("%s (%s)", o.ID, o.Label)
			}
			return codeError{2, fmt.Errorf("approval %s: %s; choose --option from %s", req.ID, req.Title, strings.Join(opts, ", "))}
		}
		err = c.Respond(ctx, proto.RespondParams{Session: id, Approval: req.ID, Option: *option, Key: newKey()})
		var ae *proto.APIError
		if errors.As(err, &ae) && ae.Code == proto.CodeConflict {
			return fmt.Errorf("approval %s was already answered", req.ID)
		}
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(map[string]string{"session": string(id), "approval": req.ID, "option": *option})
		} else {
			fmt.Fprintf(a.stdout, "answered %s with %s\n", req.ID, *option)
		}
		return nil
	})
}

func (a app) sessionArchive(args []string) int {
	fs := a.flags("session archive", sessionUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("session archive", commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		if err := c.Archive(ctx, id); err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(map[string]string{"archived": string(id)})
		} else {
			fmt.Fprintln(a.stdout, "archived", id)
		}
		return nil
	})
}

func (a app) sessionSet(args []string) int {
	fs := a.flags("session set", sessionUsage)
	perm := fs.String("permission", "", "confirm, auto_edit, auto_review or full_auto")
	plan := fs.String("plan", "", "on or off")
	fast := fs.String("fast", "", "on or off")
	effort := fs.String("effort", "", "low, medium, high, xhigh, max or ultra")
	asJSON := fs.Bool("json", false, "print the session as JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	for _, f := range []struct{ name, value string }{{"plan", *plan}, {"fast", *fast}} {
		if f.value != "" && f.value != "on" && f.value != "off" {
			fmt.Fprintf(a.stderr, "hoplet session set: --%s %q: want on or off\n", f.name, f.value)
			return 2
		}
	}
	if *perm != "" && !proto.PermissionMode(*perm).Valid() {
		fmt.Fprintf(a.stderr, "hoplet session set: --permission %q: want confirm, auto_edit, auto_review or full_auto\n", *perm)
		return 2
	}
	if *effort != "" && !proto.Effort(*effort).Valid() {
		fmt.Fprintf(a.stderr, "hoplet session set: --effort %q: want low, medium, high, xhigh, max or ultra\n", *effort)
		return 2
	}
	if *perm == "" && *plan == "" && *fast == "" && *effort == "" {
		fmt.Fprintln(a.stderr, "hoplet session set: nothing to change; give --permission, --plan, --fast or --effort")
		return 2
	}
	return a.withClient("session set", commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		if *perm != "" {
			if err := c.SetPermission(ctx, proto.SetPermissionParams{Session: id, Permission: proto.PermissionMode(*perm)}); err != nil {
				return err
			}
		}
		if *plan != "" {
			if err := c.SetPlan(ctx, proto.SetPlanParams{Session: id, Plan: *plan == "on"}); err != nil {
				return err
			}
		}
		if *fast != "" {
			if err := c.SetFast(ctx, proto.SetFastParams{Session: id, Fast: *fast == "on"}); err != nil {
				return err
			}
		}
		if *effort != "" {
			if err := c.SetEffort(ctx, proto.SetEffortParams{Session: id, Effort: proto.Effort(*effort)}); err != nil {
				return err
			}
		}
		st, err := c.Session(ctx, id)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(st.Session)
			return nil
		}
		fmt.Fprintln(a.stdout, describeMode(st.Session))
		return nil
	})
}

// describeMode is a session's permission mode, plan mode, fast mode and
// effort.
func describeMode(s proto.Session) string {
	out := "permission " + string(s.Permission)
	if s.Plan {
		out += ", plan mode on"
	}
	if s.Fast {
		out += ", fast mode on"
	}
	if s.Effort != "" {
		out += ", effort " + string(s.Effort)
	}
	return out
}

// sessionOneShot runs a command that takes only a session ID (and, with
// text, one more argument) and prints done.
func (a app) sessionOneShot(name string, args []string, withText bool, call func(ctx context.Context, c *client.Client, id proto.SessionID, text string) error, done string) int {
	fs := a.flags(name, sessionUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	n := 1
	if withText {
		n = 2
	}
	if code, ok := parse(fs, args, n, n); !ok {
		return code
	}
	return a.withClient(name, commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		if err := call(ctx, c, id, fs.Arg(1)); err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(map[string]string{done: string(id)})
		} else {
			fmt.Fprintln(a.stdout, done, id)
		}
		return nil
	})
}

func (a app) sessionInterrupt(args []string) int {
	return a.sessionOneShot("session interrupt", args, false, func(ctx context.Context, c *client.Client, id proto.SessionID, _ string) error {
		return c.Interrupt(ctx, id)
	}, "interrupted")
}

func (a app) sessionSteer(args []string) int {
	return a.sessionOneShot("session steer", args, true, func(ctx context.Context, c *client.Client, id proto.SessionID, text string) error {
		return c.Steer(ctx, proto.SteerParams{Session: id, Text: text})
	}, "steered")
}

func (a app) sessionStop(args []string) int {
	return a.sessionOneShot("session stop", args, false, func(ctx context.Context, c *client.Client, id proto.SessionID, _ string) error {
		return c.Stop(ctx, id)
	}, "stopped")
}

func (a app) sessionDiff(args []string) int {
	fs := a.flags("session diff", sessionUsage)
	stat := fs.Bool("stat", false, "list the changed files with line counts instead of the patch")
	repo := fs.String("repo", "", "only this repository, by its path under the session's directory")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("session diff", createTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		d, err := c.Diff(ctx, proto.DiffParams{Session: id, Repo: *repo, Patch: !*stat})
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(d)
			return nil
		}
		a.printDiff(d, *stat)
		return nil
	})
}

// printDiff writes a session diff: a header line per repository, then its
// patch, or with stat its files.
func (a app) printDiff(d proto.SessionDiff, stat bool) {
	if d.BaselineError != "" {
		fmt.Fprintf(a.stdout, "baseline: %s\n", d.BaselineError)
	}
	if len(d.Repos) == 0 {
		fmt.Fprintln(a.stdout, "no repositories")
		return
	}
	for _, r := range d.Repos {
		header := "== " + r.Path
		if r.Branch != "" {
			header += " (" + r.Branch + ")"
		}
		fmt.Fprintf(a.stdout, "%s %s\n", header, r.State)
		switch {
		case r.Error != "":
			fmt.Fprintf(a.stdout, "error: %s\n", r.Error)
		case r.State == proto.RepoMissing:
			fmt.Fprintln(a.stdout, "the repository is gone")
		case len(r.Files) == 0 && r.State == proto.RepoNew:
			fmt.Fprintln(a.stdout, "no files")
		case len(r.Files) == 0:
			fmt.Fprintln(a.stdout, "no changes")
		case stat:
			for _, f := range r.Files {
				fmt.Fprintln(a.stdout, statLine(f))
			}
		default:
			fmt.Fprint(a.stdout, r.Patch)
			if r.Patch != "" && !strings.HasSuffix(r.Patch, "\n") {
				fmt.Fprintln(a.stdout)
			}
			if r.Truncated {
				fmt.Fprintln(a.stdout, "(patch truncated)")
			}
			for _, f := range r.Files {
				if f.Filter != "" {
					fmt.Fprintf(a.stdout, "%s: changed through filter %s, no patch shown\n", f.Path, f.Filter)
				}
			}
		}
		if r.FilesTruncated {
			fmt.Fprintf(a.stdout, "(only the first %d files are listed)\n", len(r.Files))
		}
		for _, f := range r.Skipped {
			fmt.Fprintf(a.stdout, "skipped (%s): %s", f.Reason, f.Path)
			if f.Size > 0 {
				fmt.Fprintf(a.stdout, ", %d bytes", f.Size)
			}
			fmt.Fprintln(a.stdout)
		}
		if r.SkippedTruncated {
			fmt.Fprintf(a.stdout, "(only the first %d skipped files are listed)\n", len(r.Skipped))
		}
	}
}

// statLine describes one changed file as "+A -D status path".
func statLine(f proto.FileDiff) string {
	adds, dels := strconv.Itoa(f.Additions), strconv.Itoa(f.Deletions)
	if f.Binary {
		adds, dels = "-", "-"
	}
	path := f.Path
	if f.OldPath != "" {
		path = f.OldPath + " -> " + f.Path
	}
	if f.Filter != "" {
		path += " (filter " + f.Filter + ")"
	}
	return fmt.Sprintf("+%s -%s %s %s", adds, dels, f.Status, path)
}
