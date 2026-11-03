package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// listTimeout bounds asking a fresh claude for its models.
const listTimeout = 20 * time.Second

var _ harness.ModelLister = (*Harness)(nil)

// UseEnv sets where the environment claude starts with comes from, for
// listing its models outside a session.
func (h *Harness) UseEnv(env func() []string) {
	h.mu.Lock()
	h.envFunc = env
	h.mu.Unlock()
}

// Models lists the models claude offers, with what each takes (efforts,
// fast mode), without a session: a claude started for the purpose answers
// initialize and is stopped; no message is sent. The list is kept until
// the binary changes; a session's own initialize refreshes it.
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
	models, err = listModels(ctx, h.Binary, launchEnv(base))
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.modelsFor == st && len(h.models) == 0 {
		h.models = models
	}
	h.mu.Unlock()
	return slices.Clone(models), nil
}

// listModels starts bin with env, asks it to initialize and reads the
// models of its answer.
func listModels(ctx context.Context, bin string, env []string) ([]proto.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--verbose", "--no-session-persistence",
		"--input-format", "stream-json", "--output-format", "stream-json",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--settings", `{"disableAllHooks":true}`)
	cmd.Env, cmd.Dir = env, os.TempDir()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	req, err := json.Marshal(requestLine{Type: "control_request", RequestID: "models", Request: map[string]string{"subtype": "initialize"}})
	if err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	if _, err := in.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var ln line
		if json.Unmarshal(sc.Bytes(), &ln) != nil || ln.Type != "control_response" || ln.Response == nil || ln.Response.RequestID != "models" {
			continue
		}
		if ln.Response.Subtype == "error" {
			return nil, fmt.Errorf("claude: list models: %s", ln.Response.Error)
		}
		if m := modelsOf(ln.Response.Response); len(m) > 0 {
			return m, nil
		}
		return nil, errors.New("claude: list models: no models in the answer")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("claude: list models: %w", err)
	}
	return nil, errors.New("claude: list models: claude exited without answering")
}
