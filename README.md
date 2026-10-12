# Hoplet

Hoplet is self-hosted remote control and cross-machine handoff for Claude Code sessions. An agent on every
development machine syncs sessions to a server you own. From any desktop browser you open even a session of
hundreds of megabytes at once, drive it while Claude Code works, and move it with its uncommitted work to
another machine.

## Status

v0.1.0, pre-release: nothing is released yet, and the formats change without notice until 1.0. The current
iteration builds remote control and handoff (ROADMAP.md). Runs on any host with Docker Compose (x86_64 and
arm64), with agents on macOS; remote access goes through your own TLS proxy. A native client for phones and the
Mac comes later (ROADMAP.md).

## Product

- Open at once: the conversation loads from the tail, large tool output and images load on request.
- Remote control: streamed output, permission answers and interruption for a Claude Code process Hoplet runs
  on the session's machine.
- Handoff: a session moves with its uncommitted and untracked changes into the target machine's own clone and
  continues there, also when the source machine is offline.
- Incremental sync: only new bytes cross the network; the server keeps an exact copy.
- Self-hosted: one container image, one volume, no outside service, no telemetry.

## Install

On the server:

```bash
HOPLET_TOKEN=choose-a-long-token docker compose -f image/compose.yaml up -d
```

The web UI answers on `http://127.0.0.1:7788`; put a TLS proxy in front for other machines
(docs/hosting.md#reverse-proxy). `hoplet devices add <name>` (`pnpm hoplet`) mints a device token and prints the agent's
`config.json`; on that machine, build the agent (`agent/`, Go 1.27), save the configuration and run
`hoplet-agent daemon`.

## Development

Requirements: Node.js 26 (`.node-version`), pnpm 12, Go 1.27.

```bash
pnpm install --frozen-lockfile
pnpm check                        # Biome · tsc · Vitest · boundaries · version · tokens · go vet/test
./scripts/smoke.sh                # end to end with generated fixture sessions
```

The version lives in `VERSION`; releases are made by release-please (`GOVERNANCE.md`, Releases). How to contribute is in
[CONTRIBUTING.md](CONTRIBUTING.md); how decisions, triage and releases work, in [GOVERNANCE.md](GOVERNANCE.md).

## Documentation

| Document | Contents |
|---|---|
| [docs/hosting.md](docs/hosting.md) | Running Hoplet: what the host runs and stores, upgrades, the reverse proxy |
| [docs/api.md](docs/api.md) | The HTTP API and browser WebSocket for native clients |
| [docs/spec.md](docs/spec.md) | What Hoplet does, screen by screen and flow by flow |
| [docs/architecture.md](docs/architecture.md) | How the parts fit, the contracts between them |
| [docs/threat-model.md](docs/threat-model.md) | What is protected and how |
| [docs/design.md](docs/design.md) | Design language |
| [docs/testing.md](docs/testing.md) | Checks, end-to-end tests and benchmarks |
| [docs/adr/](docs/adr/README.md) | Decisions and why |
| [ROADMAP.md](ROADMAP.md) | What comes next |
| [CONTRIBUTING.md](CONTRIBUTING.md), [GOVERNANCE.md](GOVERNANCE.md) | How to contribute; how decisions, triage and releases work |
| [CHANGELOG.md](CHANGELOG.md) | Changes |
| [AGENTS.md](AGENTS.md) | Brief for AI coding agents |

## License

Apache License 2.0 (`LICENSE`, `NOTICE`). Third-party software is listed in `THIRD_PARTY.md`. How to report a
vulnerability is in `SECURITY.md`; questions go to [SUPPORT.md](SUPPORT.md).
