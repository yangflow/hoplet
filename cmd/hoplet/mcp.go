package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

const mcpUsage = `usage: hoplet mcp --home DIR

Serves a session's Hoplet tools to its harness as an MCP server on stdio.
It connects to the daemon of DIR with the session token in
HOPLET_SESSION_TOKEN and forwards every tool call to it. The daemon writes
it into the harness's MCP configuration; it is not run by hand.
`

// errDaemonGone ends the server when the daemon connection is lost.
var errDaemonGone = errors.New("lost the connection to the daemon")

// mcpCmd runs `hoplet mcp`.
func (a app) mcpCmd(args []string, stdin io.ReadCloser, stdout io.WriteCloser) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(a.stderr, mcpUsage)
		fs.PrintDefaults()
	}
	dir := fs.String("home", "", "home directory of the daemon")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *dir == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	token := os.Getenv(tools.TokenEnv)
	if token == "" {
		fmt.Fprintf(a.stderr, "hoplet mcp: %s is not set\n", tools.TokenEnv)
		return 1
	}
	ctx := context.Background()
	c, err := client.Dial(ctx, client.Options{Socket: home.Socket(*dir), SessionToken: token})
	if err != nil {
		fmt.Fprintf(a.stderr, "hoplet mcp: %v\n", err)
		return 1
	}
	defer c.Close()
	if err := serveMCP(ctx, c, &mcp.IOTransport{Reader: stdin, Writer: stdout}); err != nil {
		fmt.Fprintf(a.stderr, "hoplet mcp: %v\n", err)
		return 1
	}
	return 0
}

// toolClient is the daemon connection serveMCP forwards to.
type toolClient interface {
	Call(ctx context.Context, method string, params, result any) error
	Done() <-chan struct{}
}

// serveMCP registers the session's tools, as the daemon lists them once,
// and serves them on t until the harness closes it or the daemon goes.
func serveMCP(ctx context.Context, c toolClient, t mcp.Transport) error {
	var list proto.ToolsListResult
	if err := c.Call(ctx, proto.MethodToolsList, nil, &list); err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "hoplet", Version: version}, nil)
	for _, tl := range list.Tools {
		s.AddTool(&mcp.Tool{Name: tl.Name, Description: tl.Description, InputSchema: tl.InputSchema}, forward(c, tl.Name))
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		select {
		case <-c.Done():
			cancel(errDaemonGone)
		case <-ctx.Done():
		}
	}()
	err := s.Run(ctx, t)
	if cause := context.Cause(ctx); errors.Is(cause, errDaemonGone) {
		return cause
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// forward turns an MCP tool call into a tools.call to the daemon. The
// daemon checks the arguments; its refusals come back as error results.
func forward(c toolClient, name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var r proto.ToolsCallResult
		p := proto.ToolsCallParams{Name: name, Arguments: req.Params.Arguments}
		if err := c.Call(ctx, proto.MethodToolsCall, p, &r); err != nil {
			return nil, err
		}
		content := []mcp.Content{&mcp.TextContent{Text: r.Text}}
		for _, img := range r.Images {
			content = append(content, &mcp.ImageContent{MIMEType: img.MIME, Data: img.Data})
		}
		return &mcp.CallToolResult{Content: content, IsError: r.IsError}, nil
	}
}
