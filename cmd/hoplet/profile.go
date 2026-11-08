package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/proto"
)

const profileUsage = `usage: hoplet profile <command> [flags]

commands:
  list        [--category C] [--status S] [--scope KIND[:VALUE]] [--text T] [--all]
  show        ID
  edit        ID --body FILE|- [--scope KIND[:VALUE]]
  withdraw    ID [--reason R]
  candidates
  accept      ID [--scope KIND[:VALUE]]
  reject      ID
  extraction  [on|off|status]

The profile is what Hoplet remembers long-term. list shows active entries;
--status picks another status and --all lists every status. candidates lists
the changes waiting for you. edit reads the new body from FILE, or standard
input for -. IDs are full entry IDs. Every command takes --json for
machine-readable output.
`

func (a app) profile(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) == 0 {
			fmt.Fprint(a.stderr, profileUsage)
			return 2
		}
		fmt.Fprint(a.stdout, profileUsage)
		return 0
	}
	cmds := map[string]func([]string) int{
		"list": a.profileList, "show": a.profileShow, "edit": a.profileEdit, "withdraw": a.profileWithdraw,
		"candidates": a.profileCandidates, "accept": a.profileAccept, "reject": a.profileReject,
		"extraction": a.profileExtraction,
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "hoplet profile: unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:])
}

func parseScope(s string) (*proto.ProfileScope, error) {
	if s == "" {
		return nil, nil
	}
	kind, value, _ := strings.Cut(s, ":")
	switch kind {
	case "global", "companion", "repo", "host":
	default:
		return nil, fmt.Errorf("bad scope %q: want global, companion:V, repo:V or host:V", s)
	}
	if (kind == "global") != (value == "") {
		return nil, fmt.Errorf("bad scope %q: want global, companion:V, repo:V or host:V", s)
	}
	return &proto.ProfileScope{Kind: kind, Value: value}, nil
}

func (a app) scopeFlag(cmd, s string) (*proto.ProfileScope, bool) {
	sc, err := parseScope(s)
	if err != nil {
		fmt.Fprintf(a.stderr, "hoplet %s: %v\n", cmd, err)
		return nil, false
	}
	return sc, true
}

func newOpID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("new operation ID: %w", err)
	}
	return "cli_" + hex.EncodeToString(b[:]), nil
}

func scopeText(sc proto.ProfileScope) string {
	if sc.Value == "" {
		return sc.Kind
	}
	return sc.Kind + ":" + sc.Value
}

func (a app) profileList(args []string) int {
	fs := a.flags("profile list", profileUsage)
	category := fs.String("category", "", "only this category")
	status := fs.String("status", "", "only this status")
	scope := fs.String("scope", "", "only this scope, KIND[:VALUE]")
	text := fs.String("text", "", "only entries containing this text")
	all := fs.Bool("all", false, "list every status")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	sc, ok := a.scopeFlag("profile list", *scope)
	if !ok {
		return 2
	}
	if *all && *status != "" {
		fmt.Fprintln(a.stderr, "hoplet profile list: --all and --status exclude each other")
		return 2
	}
	return a.withClient("profile list", commandTimeout, func(ctx context.Context, c *client.Client) error {
		list, err := c.ProfileList(ctx, proto.ProfileListParams{Category: *category, Status: *status, All: *all, Scope: sc, Text: *text})
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(list.Entries)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tCATEGORY\tSCOPE\tTITLE")
		for _, e := range list.Entries {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.Status, e.Category, scopeText(e.Scope), e.Title)
		}
		return tw.Flush()
	})
}

func (a app) profileShow(args []string) int {
	fs := a.flags("profile show", profileUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("profile show", commandTimeout, func(ctx context.Context, c *client.Client) error {
		d, err := c.ProfileGet(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(d)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "entry\t%s\n", d.Entry.ID)
		fmt.Fprintf(tw, "version\t%d\n", d.Entry.Version)
		fmt.Fprintf(tw, "status\t%s\n", d.Entry.Status)
		fmt.Fprintf(tw, "category\t%s\n", d.Entry.Category)
		fmt.Fprintf(tw, "scope\t%s\n", scopeText(d.Entry.Scope))
		for _, ev := range d.Entry.Evidence {
			fmt.Fprintf(tw, "evidence\t%s %s\n", ev.Kind, ev.Ref)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "\n%s\n\nhistory:\n", d.Entry.Body)
		for _, v := range d.History {
			fmt.Fprintf(a.stdout, "  v%d  %s  %s  %s\n", v.Version, v.At.Format("2006-01-02 15:04"), v.Actor, v.Reason)
		}
		return nil
	})
}

func (a app) readBody(name string) ([]byte, error) {
	if name != "-" {
		return os.ReadFile(name) //nolint:gosec // the file the user named on the command line
	}
	if a.stdin != nil {
		return io.ReadAll(a.stdin)
	}
	return io.ReadAll(os.Stdin)
}

// reportWrite prints a saved write; anything else is a failure, so nothing
// unsaved is ever reported as saved.
func (a app) reportWrite(res proto.ProfileWriteResult, asJSON bool, saved string) error {
	if res.Outcome != "applied" || res.Entry == nil {
		if asJSON {
			a.printJSON(res)
		}
		return fmt.Errorf("not saved (%s): %s", res.Code, res.Reason)
	}
	if asJSON {
		a.printJSON(res)
		return nil
	}
	fmt.Fprintf(a.stdout, "%s %s, version %d.\n", saved, res.Entry.ID, res.Entry.Version)
	return nil
}

func (a app) profileEdit(args []string) int {
	fs := a.flags("profile edit", profileUsage)
	bodyFrom := fs.String("body", "", "the file holding the new body, or - for standard input")
	scope := fs.String("scope", "", "a new scope, KIND[:VALUE]")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	if *bodyFrom == "" {
		fmt.Fprintln(a.stderr, "hoplet profile edit: --body is required")
		return 2
	}
	sc, ok := a.scopeFlag("profile edit", *scope)
	if !ok {
		return 2
	}
	body, err := a.readBody(*bodyFrom)
	if err != nil {
		fmt.Fprintf(a.stderr, "hoplet profile edit: %v\n", err)
		return 1
	}
	return a.withClient("profile edit", commandTimeout, func(ctx context.Context, c *client.Client) error {
		d, err := c.ProfileGet(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		op, err := newOpID()
		if err != nil {
			return err
		}
		res, err := c.ProfileUpdate(ctx, proto.ProfileUpdateParams{ID: d.Entry.ID, ExpectedVersion: d.Entry.Version, Body: string(body), Scope: sc, OpID: op})
		if err != nil {
			return err
		}
		return a.reportWrite(res, *asJSON, "Saved")
	})
}

func (a app) profileWithdraw(args []string) int {
	fs := a.flags("profile withdraw", profileUsage)
	reason := fs.String("reason", "", "why the entry no longer holds")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("profile withdraw", commandTimeout, func(ctx context.Context, c *client.Client) error {
		d, err := c.ProfileGet(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		op, err := newOpID()
		if err != nil {
			return err
		}
		res, err := c.ProfileWithdraw(ctx, proto.ProfileWithdrawParams{ID: d.Entry.ID, ExpectedVersion: d.Entry.Version, Reason: *reason, OpID: op})
		if err != nil {
			return err
		}
		return a.reportWrite(res, *asJSON, "Withdrew")
	})
}

func (a app) profileCandidates(args []string) int {
	fs := a.flags("profile candidates", profileUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	return a.withClient("profile candidates", commandTimeout, func(ctx context.Context, c *client.Client) error {
		cs, err := c.ProfileCandidates(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(cs.Candidates)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tACTION\tTARGET\tFROM\tSCOPE\tTITLE")
		for _, cand := range cs.Candidates {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", cand.Entry.ID, cand.Action, cand.Target, cand.Actor, scopeText(cand.Entry.Scope), cand.Entry.Title)
		}
		return tw.Flush()
	})
}

func (a app) profileExtraction(args []string) int {
	fs := a.flags("profile extraction", profileUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 1); !ok {
		return code
	}
	action := "status"
	if fs.NArg() == 1 {
		action = fs.Arg(0)
	}
	if action != "status" && action != "on" && action != "off" {
		fmt.Fprintf(a.stderr, "hoplet profile extraction: unknown action %q\n", action)
		return 2
	}
	return a.withClient("profile extraction", commandTimeout, func(ctx context.Context, c *client.Client) error {
		var (
			st  proto.ProfileExtractionStatus
			err error
		)
		if action == "status" {
			st, err = c.ProfileExtraction(ctx)
		} else {
			st, err = c.SetProfileExtraction(ctx, action == "on")
		}
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(st)
			return nil
		}
		state := "on"
		if !st.Enabled {
			state = "off"
		}
		fmt.Fprintf(a.stdout, "Background extraction: %s\nFailed today: %d\n", state, st.Failed)
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "HARNESS\tTODAY\tNOTE")
		for _, h := range st.Harnesses {
			fmt.Fprintf(tw, "%s\t%d/%d\t%s\n", h.Kind, h.Used, st.DailyLimit, h.Unsupported)
		}
		return tw.Flush()
	})
}

func (a app) profileAccept(args []string) int {
	fs := a.flags("profile accept", profileUsage)
	scope := fs.String("scope", "", "the scope to use instead of the proposed one, KIND[:VALUE]")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	sc, ok := a.scopeFlag("profile accept", *scope)
	if !ok {
		return 2
	}
	return a.withClient("profile accept", commandTimeout, func(ctx context.Context, c *client.Client) error {
		op, err := newOpID()
		if err != nil {
			return err
		}
		e, err := c.ProfileAccept(ctx, proto.ProfileAcceptParams{ID: fs.Arg(0), Scope: sc, OpID: op})
		return a.reportDecision(e, err, *asJSON, "Accepted")
	})
}

func (a app) profileReject(args []string) int {
	fs := a.flags("profile reject", profileUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("profile reject", commandTimeout, func(ctx context.Context, c *client.Client) error {
		op, err := newOpID()
		if err != nil {
			return err
		}
		e, err := c.ProfileReject(ctx, proto.ProfileRejectParams{ID: fs.Arg(0), OpID: op})
		return a.reportDecision(e, err, *asJSON, "Rejected")
	})
}

func (a app) reportDecision(e proto.ProfileEntry, err error, asJSON bool, verb string) error {
	if err != nil {
		return err
	}
	if e.ID == "" {
		return errors.New("the daemon returned no entry")
	}
	if asJSON {
		a.printJSON(e)
		return nil
	}
	fmt.Fprintf(a.stdout, "%s: %s is %s.\n", verb, e.ID, e.Status)
	return nil
}
