package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/yangflow/hoplet/internal/harness/hermes"
)

const bridgeUsage = `usage: hoplet hermes-bridge --hermes PATH

Connects to the Hermes gateway of the Hermes home the environment names and
relays it over stdio: each gateway frame is a line on stdout, each line on
stdin is sent to the gateway. The daemon runs it behind a shim for a Hermes
session; it stops on SIGTERM, interrupting a turn it saw start.
`

// runBridge runs `hoplet hermes-bridge`.
func runBridge(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hermes-bridge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, bridgeUsage)
		fs.PrintDefaults()
	}
	bin := fs.String("hermes", "", "the hermes executable")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *bin == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// After the first signal a second one must end a stuck shutdown: give
	// the default handling back.
	go func() { <-ctx.Done(); stop() }()
	err := hermes.Bridge(ctx, hermes.BridgeConfig{Hermes: *bin, Env: os.Environ(), In: stdin, Out: stdout})
	if err != nil {
		fmt.Fprintf(stderr, "hoplet hermes-bridge: %v\n", err)
		return 1
	}
	return 0
}
