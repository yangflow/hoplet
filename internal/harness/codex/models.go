package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// listTimeout bounds asking a fresh app-server for its models.
const listTimeout = 20 * time.Second

var _ harness.ModelLister = (*Harness)(nil)

// UseEnv sets where the environment Codex starts with comes from, for
// listing its models outside a session.
func (h *Harness) UseEnv(env func() []string) {
	h.mu.Lock()
	h.envFunc = env
	h.mu.Unlock()
}

// Models lists the models Codex offers, without a session: an app-server
// started for the purpose answers model/list and is stopped. The list is
// kept until the binary changes.
func (h *Harness) Models(ctx context.Context) ([]proto.ModelInfo, error) {
	st, err := binaryStamp(h.Binary)
	if err != nil {
		return nil, err
	}
	h.listing.Lock()
	defer h.listing.Unlock()
	h.mu.Lock()
	if st != h.modelsFor {
		h.models, h.modelsFor = nil, st
	}
	models, env := slices.Clone(h.models), h.envFunc
	h.mu.Unlock()
	if len(models) > 0 {
		return models, nil
	}
	var base []string
	if env != nil {
		base = env()
	}
	if models, err = listModels(ctx, h.Binary, base); err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.models = slices.Clone(models)
	h.mu.Unlock()
	return models, nil
}

func listModels(ctx context.Context, bin string, env []string) (_ []proto.ModelInfo, err error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := launchCommand(ctx, harness.Launch{Command: []string{bin, "app-server"}, Env: slices.Clone(env)})
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait() // killed by the cancel: its status says nothing
	}()
	send := func(line string, params any) error {
		var p json.RawMessage
		if params != nil {
			if p, err = json.Marshal(params); err != nil {
				return fmt.Errorf("codex: encode: %w", err)
			}
		}
		id := json.RawMessage(`"list"`)
		b, err := json.Marshal(message{ID: id, Method: line, Params: p})
		if err != nil {
			return fmt.Errorf("codex: encode: %w", err)
		}
		if _, err := stdin.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("codex: write %s: %w", line, err)
		}
		return nil
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(nil, harness.MaxLine)
	next := func() (json.RawMessage, error) {
		for sc.Scan() {
			var m message
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.Method != "" || string(m.ID) != `"list"` {
				continue
			}
			if m.Error != nil {
				return nil, fmt.Errorf("codex: list models: %s", m.Error.Message)
			}
			return m.Result, nil
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("codex: list models: %w", err)
		}
		return nil, fmt.Errorf("codex: list models: the app-server ended: %w", ctx.Err())
	}
	if err := send("initialize", initializeParams{
		ClientInfo:   clientInfo{Name: "hoplet", Title: "Hoplet", Version: "1"},
		Capabilities: clientCapabilities{ExperimentalAPI: true},
	}); err != nil {
		return nil, err
	}
	if _, err := next(); err != nil {
		return nil, err
	}
	if _, err := stdin.Write([]byte(`{"method":"initialized"}` + "\n")); err != nil {
		return nil, fmt.Errorf("codex: write initialized: %w", err)
	}
	var out []proto.ModelInfo
	cursor := ""
	for {
		if err := send("model/list", modelListParams{Cursor: cursor}); err != nil {
			return nil, err
		}
		raw, err := next()
		if err != nil {
			return nil, err
		}
		var res modelListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("codex: list models: %w", err)
		}
		out = append(out, modelInfos(res.Data)...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// modelInfos maps the models to show, keeping only the efforts Hoplet has.
func modelInfos(models []model) []proto.ModelInfo {
	known := caps()
	out := make([]proto.ModelInfo, 0, len(models))
	for _, m := range models {
		if m.Hidden {
			continue
		}
		mi := proto.ModelInfo{ID: m.ID, Model: m.Model, Name: m.DisplayName, Description: m.Description, Default: m.IsDefault}
		for _, e := range m.SupportedReasoningEfforts {
			if known.HasEffort(proto.Effort(e.ReasoningEffort)) {
				mi.Efforts = append(mi.Efforts, proto.Effort(e.ReasoningEffort))
			}
		}
		mi.NoEffort = len(mi.Efforts) == 0
		out = append(out, mi)
	}
	return out
}
