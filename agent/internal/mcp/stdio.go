package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"time"
)

const (
	// protocolVersion is the MCP revision the server answers when the client asks for one it does not know.
	protocolVersion = "2025-06-18"
	// maxLine bounds one JSON-RPC message from the vendor.
	maxLine = 1 << 20
	// callTimeout bounds one tool call to the daemon.
	callTimeout = 60 * time.Second
)

var supportedVersions = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Caller performs one tool call.
type Caller func(tool string, args json.RawMessage) Reply

// Serve speaks MCP (JSON-RPC 2.0, one message per line) on in and out until in ends: initialize, ping, tools/list
// and tools/call. A notification gets no answer.
func Serve(in io.Reader, out io.Writer, call Caller) error {
	r := bufio.NewReaderSize(in, 64<<10)
	enc := json.NewEncoder(out)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			if resp := handle(line, call); resp != nil {
				if werr := enc.Encode(resp); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// readLine reads one line; a line over maxLine is dropped whole and answered as a parse error.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return buf, err
		}
		if len(buf)+len(chunk) > maxLine {
			for isPrefix {
				_, isPrefix, err = r.ReadLine()
				if err != nil {
					return nil, err
				}
			}
			return []byte("{"), nil
		}
		buf = append(buf, chunk...)
		if !isPrefix {
			return buf, nil
		}
	}
}

func handle(line []byte, call Caller) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
		return fail(json.RawMessage("null"), -32700, "parse error")
	}
	if len(req.ID) == 0 {
		return nil // a notification (notifications/initialized, notifications/cancelled)
	}
	ok := func(result any) *rpcResponse { return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result} }
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := protocolVersion
		if supportedVersions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		return ok(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "hoplet", "version": "1"},
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": tools()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return fail(req.ID, -32602, "invalid params")
		}
		if !known(p.Name) {
			return fail(req.ID, -32602, "unknown tool")
		}
		rep := call(p.Name, p.Arguments)
		res := map[string]any{"content": []map[string]any{{"type": "text", "text": rep.Text}}}
		if rep.IsError {
			res["isError"] = true
		}
		return ok(res)
	}
	return fail(req.ID, -32601, "method not found")
}

func known(name string) bool {
	for _, t := range tools() {
		if t.Name == name {
			return true
		}
	}
	return false
}

func fail(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// RunStdio is `hoplet-agent mcp`: it serves MCP on in and out and forwards each tool call to the daemon at the
// socket named by getenv(SockEnv), presenting getenv(NonceEnv).
func RunStdio(in io.Reader, out io.Writer, getenv func(string) string) error {
	sock, nonce := getenv(SockEnv), getenv(NonceEnv)
	return Serve(in, out, func(tool string, args json.RawMessage) Reply {
		if sock == "" || nonce == "" {
			return errReply("Hoplet MCP is not set up for this session")
		}
		return dial(sock, nonce, tool, args)
	})
}

func dial(sock, nonce, tool string, args json.RawMessage) Reply {
	c, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return errReply("The Hoplet agent is not reachable")
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(callTimeout))
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if err := json.NewEncoder(c).Encode(request{Nonce: nonce, Tool: tool, Args: args}); err != nil {
		return errReply("The Hoplet agent is not reachable")
	}
	if uc, ok := c.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	var rep Reply
	if err := json.NewDecoder(io.LimitReader(c, MaxResultBytes*4)).Decode(&rep); err != nil {
		return errReply("The Hoplet agent gave no answer")
	}
	return rep
}
