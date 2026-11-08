package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/tools"
)

// mcpName is what a server name may be: it becomes a bare key in a config
// override.
var mcpName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// mcpOverrides turns the session's MCP servers into `-c` arguments for the
// codex command line and the token files they read. The token stays in a
// file in the session directory: an argument is visible to every process
// on the machine. Each launch writes the token issued for it.
func mcpOverrides(spec harness.Spec) ([]string, map[string][]byte, error) {
	if len(spec.MCP) == 0 {
		return nil, nil, nil
	}
	if spec.Dir == "" {
		return nil, nil, errors.New("codex: MCP servers need a session directory")
	}
	var args []string
	files := map[string][]byte{}
	for _, s := range spec.MCP {
		if !mcpName.MatchString(s.Name) || len(s.Command) == 0 {
			return nil, nil, fmt.Errorf("codex: cannot inject MCP server %q", s.Name)
		}
		token, err := serverToken(s)
		if err != nil {
			return nil, nil, err
		}
		file := "mcp-" + s.Name + ".token"
		files[file] = []byte(token)
		srv := append(append([]string(nil), s.Command[1:]...), "--token-file", filepath.Join(spec.Dir, file))
		prefix := "mcp_servers." + s.Name + "."
		for _, o := range []struct {
			key   string
			value any
		}{
			{"command", s.Command[0]},
			{"args", srv},
			// Hoplet decides who may use its tools; Codex would otherwise
			// ask the user about every call.
			{"default_tools_approval_mode", "approve"},
		} {
			// A JSON string or array of strings is also valid TOML.
			v, err := json.Marshal(o.value)
			if err != nil {
				return nil, nil, fmt.Errorf("codex: %w", err)
			}
			args = append(args, "-c", prefix+o.key+"="+string(v))
		}
	}
	return args, files, nil
}

// serverToken returns the session token in s's environment. The launch has
// no safe way to pass any other variable to the server.
func serverToken(s harness.MCPServer) (string, error) {
	token := ""
	for _, kv := range s.Env {
		v, ok := strings.CutPrefix(kv, tools.TokenEnv+"=")
		if !ok {
			return "", fmt.Errorf("codex: MCP server %q: only the session token can be passed", s.Name)
		}
		token = v
	}
	if token == "" {
		return "", fmt.Errorf("codex: MCP server %q has no session token", s.Name)
	}
	return token, nil
}
