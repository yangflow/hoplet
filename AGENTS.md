# AGENTS.md

Instructions for AI coding agents working in this repository. Human contributors: see [CONTRIBUTING.md](CONTRIBUTING.md); everything there applies to agents too.

## Read first

1. [`docs/README.md`](docs/README.md) — what Hoplet is, the two modes, architecture, milestones.
2. The design doc for the area you are touching in [`docs/design/`](docs/design/). The docs are in Chinese; they are the source of truth for behavior, protocols and storage.

## Hard rules

1. **Follow the design.** If a change conflicts with a decision in `docs/design/`, stop and raise it instead of working around it. If the design changes, update the doc in the same change.
2. **No references to other projects.** Never name another project as the source or model of an idea in code, comments, docs, commit messages or PR descriptions. State Hoplet's own rule instead.
3. **Language.**

   | Content | Language |
   |---|---|
   | Code, identifiers, comments, commit messages | English |
   | Design docs in `docs/` | Chinese |
   | README, CONTRIBUTING, templates, this file and other repository docs | English (`README.zh-CN.md` mirrors the README in Chinese — keep both in sync) |

4. **Commits.** Conventional Commits (`type(scope): summary`, English, imperative, ≤72 characters) with a DCO sign-off (`git commit -s`). Commit or push only when asked; never commit directly to `main`.
5. **Run `make check` before you say you're done**, and report its real output. Don't claim success for checks you didn't run.
6. **No new language, runtime or third-party dependency** without explicit agreement. The stack is Go (daemon, shim, relay, CLI, desktop via MyGo) and Swift (iOS).
7. **Never read or modify harness credentials** (`~/.claude`, `CODEX_HOME`, Hermes config, API keys) and never pass credential variables into harness environments beyond what the design allows.
8. **Generated code is regenerated, not hand-edited.** Edit `proto/` and run `make gen`.

## Code conventions

**Go**

- `gofmt`/`goimports`; `golangci-lint` must pass.
- Errors wrapped with `%w`; `context.Context` first; no panics in library code; no mutable globals; `log/slog` for logging; doc comments on exported identifiers.
- Persisted events must always decode: version event types and keep old decoders.
- Tests: table-driven where it helps, `-race`, no sleeps for synchronization.

**Swift**

- Swift 6 strict concurrency, warnings as errors, `swift-format`.
- Standard SwiftUI components and Apple's Human Interface Guidelines; no workarounds that force a custom look over native behavior.
- Swift Testing for tests; protocol behavior is checked against the Go-generated vectors in `testdata/vectors/`.

**Desktop (MyGo)**

- Pinned MyGo version; `go tool mygo vet`; views stay thin and are tested with `ui.Tester`.

## Repository layout

```
cmd/hoplet/        single binary: daemon, shim, relay, CLI
internal/          implementation packages
proto/             protocol types (source of truth)
tools/swiftgen/    Go types → Swift Codable generator
desktop/           MyGo desktop app
apple/             iOS app and extensions
testdata/vectors/  cross-language conformance vectors
docs/              design docs (Chinese)
```

Directories appear as the milestones land; don't create empty ones ahead of time.
