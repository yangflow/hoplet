package main

import (
	"fmt"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

// render formats an event as one or more lines for `session watch`; it
// returns "" for events not worth a line (streaming deltas, usage).
func render(ev proto.Event) string {
	p, err := ev.Decode()
	if err != nil {
		return fmt.Sprintf("[%d] %s (cannot decode: %v)", ev.Seq, ev.Type, err)
	}
	var s string
	switch p := p.(type) {
	case proto.TurnStarted:
		s = fmt.Sprintf("── turn %d ──", ev.Turn)
	case proto.TurnEnded:
		s = fmt.Sprintf("── turn %d %s ──", ev.Turn, p.Reason)
		if p.Message != "" {
			s += " " + p.Message
		}
	case proto.Message:
		switch p.Role {
		case proto.RoleUser:
			s = "> " + indent(p.Text, "  ")
		default:
			s = p.Text
		}
	case proto.Thinking:
		s = "(thinking) " + firstLine(p.Text)
	case proto.ToolCall:
		s = fmt.Sprintf("· %s %s [%s]", p.Name, firstLine(p.Summary), p.Status)
	case proto.FileChange:
		s = fmt.Sprintf("~ %s %s", p.Op, p.Path)
	case proto.ApprovalRequested:
		opts := make([]string, len(p.Options))
		for i, o := range p.Options {
			opts[i] = o.ID
		}
		s = fmt.Sprintf("! approval %s: %s", p.ID, p.Title)
		if p.Detail != "" {
			s += "\n  " + indent(p.Detail, "  ")
		}
		s += "\n  options: " + strings.Join(opts, ", ")
	case proto.ApprovalResolved:
		s = fmt.Sprintf("✓ approval %s %s", p.ID, p.Outcome)
		if p.OptionID != "" {
			s += " (" + p.OptionID + ")"
		}
		if p.DecidedBy != "" {
			s += " by " + p.DecidedBy
		}
	case proto.Question:
		s = fmt.Sprintf("? question %s: %s", p.ID, p.Text)
		if len(p.Options) > 0 {
			s += "\n  options: " + strings.Join(p.Options, " | ")
		}
	case proto.Error:
		s = "error: " + p.Message
	case proto.MessageDelta, proto.Usage:
		return ""
	default:
		s = string(ev.Type)
	}
	return s
}

func firstLine(s string) string {
	line, _, more := strings.Cut(strings.TrimSpace(s), "\n")
	if more {
		return line + " …"
	}
	return line
}

func indent(s, prefix string) string { return strings.ReplaceAll(s, "\n", "\n"+prefix) }
