// Package extract turns what happened in a session into long-term memory: in
// the background, through one isolated harness run at a time, and always
// through the memory service's rules.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

const (
	// MaxMaterial is the most session log one run reads, in bytes.
	MaxMaterial = 32 << 10

	truncated = " [truncated]\n"
	pageSize  = 256
)

// Material is the part of a session's log one run reads. To is the last
// event it accounts for; Text is empty when none of them is material.
type Material struct {
	Text string
	To   uint64
}

// Select reads the events from..to of a session, stopping at an event
// boundary at MaxMaterial; what does not fit is left for the next batch. A
// single event too long for a batch of its own is cut and marked.
func Select(ctx context.Context, st *store.Store, id proto.SessionID, from, to uint64) (Material, error) {
	var b strings.Builder
	after := from - 1
	for after < to {
		evs, err := st.Events(ctx, id, after, pageSize)
		if err != nil {
			return Material{}, err
		}
		if len(evs) == 0 {
			return Material{Text: b.String(), To: to}, nil
		}
		for _, e := range evs {
			if e.Seq > to {
				return Material{Text: b.String(), To: to}, nil
			}
			line, err := lineOf(e)
			if err != nil {
				return Material{}, err
			}
			switch {
			case b.Len()+len(line) <= MaxMaterial:
				b.WriteString(line)
			case b.Len() == 0:
				return Material{Text: cut(line), To: e.Seq}, nil
			default:
				return Material{Text: b.String(), To: e.Seq - 1}, nil
			}
			after = e.Seq
		}
	}
	return Material{Text: b.String(), To: to}, nil
}

// cut shortens a line to exactly fit a batch, keeping valid UTF-8.
func cut(line string) string {
	return strings.ToValidUTF8(line[:MaxMaterial-len(truncated)], "") + truncated
}

// lineOf is an event's line of material, "" when it is not material. Every
// line starts with the sequence number a model cites it by. The text is
// quoted so that it stays on its line and cannot pose as another one.
func lineOf(e proto.Event) (string, error) {
	if e.Agent != "" {
		return "", nil
	}
	switch e.Type {
	case proto.TypeMessage, proto.TypeToolCall, proto.TypeArtifactPublished:
	default:
		return "", nil
	}
	p, err := e.Decode()
	if err != nil {
		return "", fmt.Errorf("extract: decode event %d: %w", e.Seq, err)
	}
	var label, text string
	switch v := p.(type) {
	case proto.Message:
		switch {
		case v.Role == proto.RoleUser && v.From == "":
			label = "user"
		case v.Role == proto.RoleAssistant:
			label = "assistant"
		default:
			return "", nil
		}
		text = v.Text
	case proto.ToolCall:
		if v.Status == proto.ToolRunning {
			return "", nil
		}
		label, text = "tool "+string(v.Status), v.Name+": "+v.Summary
	case proto.ArtifactPublished:
		label, text = "artifact", v.Name
	default:
		return "", nil
	}
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	return "[" + strconv.FormatUint(e.Seq, 10) + "] " + label + ": " + quote(text) + "\n", nil
}

func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil { // a string always encodes
		return strconv.Quote(s)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
