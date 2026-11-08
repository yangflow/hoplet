// Command swiftgen generates Swift Codable types from the proto package.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	protoDir := flag.String("proto", ".", "directory of the proto package")
	out := flag.String("out", "", "Swift file to write")
	flag.Parse()
	if err := run(*protoDir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "swiftgen:", err)
		os.Exit(1)
	}
}

func run(protoDir, out string) error {
	if out == "" {
		return fmt.Errorf("-out is required")
	}
	sources, err := readSources(protoDir)
	if err != nil {
		return err
	}
	m, err := parseSources(sources)
	if err != nil {
		return err
	}
	text, err := emit(m)
	if err != nil {
		return err
	}
	// Generated source is committed, so it has ordinary source permissions.
	if err := os.WriteFile(out, text, 0o644); err != nil { //nolint:gosec // see above
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}
