package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/proto"
)

// modelsTTL is how long the model list is reused before Hermes is asked
// again.
const modelsTTL = 10 * time.Minute

// Models lists the models of the providers Hermes is signed in to, from
// the gateway's model.options. It reuses an answer for modelsTTL.
func (h *Harness) Models(ctx context.Context) ([]proto.ModelInfo, error) {
	h.mu.Lock()
	if h.models != nil && h.now().Sub(h.listed) < modelsTTL {
		m := slices.Clone(h.models)
		h.mu.Unlock()
		return m, nil
	}
	h.mu.Unlock()
	env := h.env()
	path, err := Find(env)
	if err != nil {
		return nil, err
	}
	ep, err := Ensure(ctx, path, env)
	if err != nil {
		return nil, err
	}
	c, err := Dial(ctx, ep)
	if err != nil {
		return nil, err
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":"hoplet-models","method":"model.options","params":{}}`)); err != nil {
		return nil, fmt.Errorf("hermes: model.options: %w", err)
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("hermes: model.options: %w", err)
		}
		var f struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if json.Unmarshal(data, &f) != nil || f.ID != "hoplet-models" {
			continue // events and other connections' traffic
		}
		if f.Error != nil {
			return nil, fmt.Errorf("hermes: model.options: %s", f.Error.Message)
		}
		m, err := parseModelOptions(f.Result)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.models, h.listed = m, h.now()
		h.mu.Unlock()
		return slices.Clone(m), nil
	}
}

// modelOptions is the part of model.options' answer Hoplet reads.
type modelOptions struct {
	Model     string          `json:"model"`
	Providers []modelProvider `json:"providers"`
}

type modelProvider struct {
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Current       bool     `json:"is_current"`
	Authenticated bool     `json:"authenticated"`
	Models        []string `json:"models"`
	Capabilities  map[string]struct {
		Fast bool `json:"fast"`
	} `json:"capabilities"`
}

// parseModelOptions lists the models of signed-in providers, the current
// provider first; the model Hermes runs by default is marked Default.
func parseModelOptions(raw json.RawMessage) ([]proto.ModelInfo, error) {
	var o modelOptions
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("hermes: model.options answer: %w", err)
	}
	providers := slices.Clone(o.Providers)
	slices.SortStableFunc(providers, func(a, b modelProvider) int {
		switch {
		case a.Current == b.Current:
			return 0
		case a.Current:
			return -1
		}
		return 1
	})
	out := []proto.ModelInfo{}
	for _, p := range providers {
		if !p.Authenticated {
			continue
		}
		for _, m := range p.Models {
			out = append(out, proto.ModelInfo{
				ID: m, Provider: p.Slug, Name: m, Description: p.Name,
				Default: p.Current && m == o.Model, Fast: p.Capabilities[m].Fast,
			})
		}
	}
	return out, nil
}
