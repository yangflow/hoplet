package extract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yangflow/hoplet/internal/memory"
)

// Cite is one piece of evidence as a model states it.
type Cite struct {
	Kind  memory.EvidenceKind `json:"kind"`
	Event uint64              `json:"event"`
}

// Op is one proposed change as a model states it. Scope holds a scope kind;
// the value comes from the session the run read.
type Op struct {
	Action   memory.Action    `json:"action"`
	Category memory.Category  `json:"category"`
	Scope    memory.ScopeKind `json:"scope"`
	Target   string           `json:"target"`
	Version  int              `json:"version"`
	Body     string           `json:"body"`
	Evidence []Cite           `json:"evidence"`
	Reason   string           `json:"reason"`
}

// Parse reads a run's reply. Anything out of shape fails the whole reply:
// half-understood output is not committed.
func Parse(s string) ([]Op, error) {
	var out struct {
		Ops *[]Op `json:"ops"`
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("extract: bad reply: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("extract: bad reply: text after the object")
	}
	if out.Ops == nil {
		return nil, errors.New("extract: bad reply: no ops")
	}
	for i, op := range *out.Ops {
		if err := op.check(); err != nil {
			return nil, fmt.Errorf("extract: bad reply: op %d: %w", i, err)
		}
	}
	return *out.Ops, nil
}

func (op Op) check() error {
	if strings.TrimSpace(op.Reason) == "" {
		return errors.New("no reason")
	}
	if len(op.Evidence) == 0 {
		return errors.New("no evidence")
	}
	for _, c := range op.Evidence {
		if !c.Kind.Valid() || c.Event == 0 {
			return fmt.Errorf("bad evidence %q at event %d", c.Kind, c.Event)
		}
	}
	body := strings.TrimSpace(op.Body) != ""
	switch op.Action {
	case memory.ActionCreate:
		if !op.Category.Valid() || !op.Scope.Valid() || !body || op.Target != "" || op.Version != 0 {
			return errors.New("create needs a category, a scope and a body, and no target or version")
		}
	case memory.ActionUpdate:
		if op.Target == "" || op.Version < 1 || !body || op.Category != "" || (op.Scope != "" && !op.Scope.Valid()) {
			return errors.New("update needs a target, its version and a body, and no category")
		}
	case memory.ActionWithdraw:
		if op.Target == "" || op.Version < 1 || body || op.Category != "" || op.Scope != "" {
			return errors.New("withdraw needs a target and its version, and no body, category or scope")
		}
	default:
		return fmt.Errorf("unknown action %q", op.Action)
	}
	return nil
}
