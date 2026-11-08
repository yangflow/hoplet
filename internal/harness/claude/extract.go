package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// extractVersion is the one version whose isolation has been measured. A
// version that only passed the local self-check does not count.
const extractVersion = "2.1.319"

// extractWait is how long a stopped extraction may take to exit.
const extractWait = 5 * time.Second

var _ harness.Extractor = (*Harness)(nil)

// ExtractSupport says why info's version cannot extract, "" when it can.
func (h *Harness) ExtractSupport(info harness.Info) string {
	if info.Version == extractVersion {
		return ""
	}
	return fmt.Sprintf("Claude Code %s has not been verified for isolated extraction", info.Version)
}

func extractArgs(r harness.ExtractRequest) []string {
	return []string{
		"-p", "--output-format", "json",
		"--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence", "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`,
		"--disable-slash-commands", "--system-prompt", r.System, "--model", r.Model,
	}
}

func extractEnv(env []string) []string {
	return append(launchEnv(env), "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1")
}

// Extract runs one isolated, one-shot claude over r.
func (h *Harness) Extract(ctx context.Context, r harness.ExtractRequest) (harness.ExtractResult, error) {
	if r.Dir == "" || r.Model == "" {
		return harness.ExtractResult{}, errors.New("claude: extraction needs a directory and a model")
	}
	cmd := extractCommand(ctx, h.Binary, extractArgs(r))
	cmd.Dir, cmd.Env, cmd.WaitDelay = r.Dir, extractEnv(r.Env), extractWait
	cmd.Stdin = strings.NewReader(r.Input)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return harness.ExtractResult{}, fmt.Errorf("claude: extraction: %w: %s", err, lastLine(errOut.String()))
	}
	return parseExtract(out.Bytes())
}

func parseExtract(out []byte) (harness.ExtractResult, error) {
	var res struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Usage   struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return harness.ExtractResult{}, fmt.Errorf("claude: extraction reply: %w", err)
	}
	if res.IsError {
		return harness.ExtractResult{}, fmt.Errorf("claude: extraction failed: %s", lastLine(res.Result))
	}
	return harness.ExtractResult{
		Output: res.Result,
		Usage:  proto.Usage{InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens},
	}, nil
}

// lastLine is the end of an error report: one line, of bounded length.
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	const limit = 500
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "")
	}
	return s
}

