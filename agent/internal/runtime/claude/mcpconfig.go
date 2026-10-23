package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/agent/internal/mcp"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// mcpConfigPath is the config file of one process: `<hoplet_home>/mcp/<nonce>.json`.
func mcpConfigPath(home string, spec *runtime.MCPSpec) string {
	return filepath.Join(home, "mcp", spec.Nonce+".json")
}

// writeMCPConfig writes the `--mcp-config` file for spec: the directory 0700, the file 0600, because the file
// holds the nonce that names the session on mcp.sock. The server entry is Hoplet's own, `hoplet`, so its tools are
// `mcp__hoplet__<tool>`.
func writeMCPConfig(home string, spec *runtime.MCPSpec) error {
	if home == "" || spec.Nonce == "" {
		return fmt.Errorf("the MCP config needs the agent's state directory and a nonce")
	}
	dir := filepath.Join(home, "mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("the MCP config directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("the MCP config directory: %w", err)
	}
	cfg := map[string]any{"mcpServers": map[string]any{mcp.ServerName: map[string]any{
		"type": "stdio", "command": spec.Command, "args": spec.Args, "env": spec.Env,
	}}}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(mcpConfigPath(home, spec), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("the MCP config file: %w", err)
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(mcpConfigPath(home, spec))
		return fmt.Errorf("the MCP config file: %w", werr)
	}
	return nil
}
