<p align="center">
  <img src="web/public/icon.svg" width="96" height="96" alt="Hoplet">
</p>

<h1 align="center">Hoplet</h1>

<p align="center">
  Self-hosted remote control and cross-machine handoff for coding-agent sessions, Claude Code first.
</p>

<p align="center">
  <a href="https://github.com/yangflow/hoplet/actions/workflows/ci.yml"><img src="https://github.com/yangflow/hoplet/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/yangflow/hoplet" alt="License: Apache-2.0"></a>
  <a href="https://github.com/yangflow/hoplet/releases"><img src="https://img.shields.io/github/v/tag/yangflow/hoplet?label=version" alt="Version"></a>
</p>

<p align="center">
  <b>English</b> · <a href="README.zh-CN.md">简体中文</a>
</p>

## Why Hoplet

A Claude Code session lives on the machine where it started. You cannot follow it from another computer, a long
session of hundreds of megabytes is slow to open anywhere, and moving the work to another machine means copying
files by hand and losing the conversation.

Hoplet puts an agent on each of your development machines and a server on a host you own. From any desktop
browser you open a session at once, drive it while Claude Code works, and hand it off, with its uncommitted
changes, to another machine where it continues. Nothing leaves your machines and your server.

## Features

- **Opens at once.** A session loads from its latest messages; earlier history, large tool output and images load
  as you scroll or ask for them. Sessions of hundreds of megabytes open as fast as small ones.
- **Remote control.** Send prompts, watch the output stream in, answer permission requests and interrupt a turn,
  for a Claude Code process Hoplet runs on the session's machine.
- **Cross-machine handoff.** Move a session with its uncommitted and untracked changes into the target machine's
  own clone, where it continues as a git worktree; also when the source machine is offline, from the last
  snapshot.
- **Workspace view.** See a session's changed files, diffs and file tree from the browser.
- **Search** across all sessions or within one.
- **Self-hosted and private.** One container image and one volume on your host; no outside service, no telemetry,
  and the server keeps an exact copy of every session, synced incrementally.

## How it works

```
 development machines                        your host                     any desktop browser
┌───────────────────────┐                 ┌──────────────────┐
│ Claude Code           │                 │  Hoplet core     │   HTTPS / WSS   ┌──────────────┐
│   ▲ runs, reads       │   WebSocket     │  (one container) │◀───────────────▶│  web UI      │
│ hoplet-agent ─────────┼────────────────▶│  sessions,       │  via your TLS   └──────────────┘
└───────────────────────┘  sync, control, │  snapshots,      │  reverse proxy
┌───────────────────────┐  handoff        │  SQLite index    │
│ hoplet-agent ─────────┼────────────────▶│                  │
└───────────────────────┘                 └──────────────────┘
```

The **agent** (Go, one binary) watches the Claude Code sessions you choose, sends only their new bytes to the
core, runs Claude Code when you prompt from the browser, and installs a handed-off session into a worktree of its
own clone. The **core** (TypeScript, one container image with the web UI) stores the sessions, serves them in
pages and relays control between the browser and the agents. Details are in
[docs/architecture.md](docs/architecture.md).

## Status

**Pre-release (v0.1).** Nothing is published yet: you build the image and the agent from this repository, and
formats and APIs may change without notice before 1.0.

- Works today: sync, opening and searching sessions, remote control with permissions and interruption, online and
  offline handoff, the workspace view, Add project and New session from the web UI.
- The server runs on Linux or macOS with Docker (x86_64 or arm64). Agents run on macOS (tested) and Linux, with
  Claude Code 2.1.289 or newer.
- The web UI is for desktop browsers. A native client for phones and the Mac comes later.

What comes next is in [ROADMAP.md](ROADMAP.md).

## Quick start

You need Docker with Compose on the server, and on each development machine Go 1.27, `git` and Claude Code. Adding
a machine uses the host CLI, which needs Node.js 26 and pnpm 12.

**1. Start the server.**

```bash
git clone https://github.com/yangflow/hoplet.git && cd hoplet
echo "HOPLET_TOKEN=$(openssl rand -hex 32)" > image/.env
docker compose -f image/compose.yaml up -d --build
```

Open `http://127.0.0.1:7788` on the host and enter the token from `image/.env`. The port is bound to the
host's loopback only; to reach Hoplet from other machines, put a TLS reverse proxy in front of it
([docs/hosting.md](docs/hosting.md#reverse-proxy)).

**2. Add a machine.** In the same checkout:

```bash
pnpm install --frozen-lockfile
HOPLET_URL=http://127.0.0.1:7788 HOPLET_TOKEN=<the token> pnpm hoplet devices add my-laptop
```

This mints a device token for the machine and prints its agent configuration; the token is shown only once.
Change `server_url` in it to the address the machine can reach (your proxy).

**3. Run the agent on that machine.**

```bash
git clone https://github.com/yangflow/hoplet.git && cd hoplet/agent
go build -o hoplet-agent ./cmd/hoplet-agent
mkdir -p ~/.hoplet && $EDITOR ~/.hoplet/config.json   # paste the printed configuration
./hoplet-agent daemon
```

The machine shows up as online in the web UI. Choose **Add project** on it to pick a folder whose Claude Code
sessions Hoplet should sync, then open a session, or start a new one with **New session**.

Every setting, the data on the host, upgrades and backups are in [docs/hosting.md](docs/hosting.md).

## Security

Hoplet runs on your host and your machines and calls no outside service. The web UI and the CLI use the host
token; each agent has its own device token, stored hashed on the server. Claude Code runs only on the machine
that owns the session, and only processes Hoplet started are controlled. What is protected, from whom and how is
in [docs/threat-model.md](docs/threat-model.md). Please report vulnerabilities privately, as
[SECURITY.md](SECURITY.md) explains.

## Documentation

| Document | Contents |
|---|---|
| [docs/hosting.md](docs/hosting.md) | Running Hoplet: requirements, settings, data, upgrades, the reverse proxy |
| [docs/spec.md](docs/spec.md) | What Hoplet does, screen by screen and flow by flow |
| [docs/architecture.md](docs/architecture.md) | How the parts fit and the contracts between them |
| [docs/api.md](docs/api.md) | The HTTP API and browser WebSocket, for other clients |
| [docs/threat-model.md](docs/threat-model.md) | What is protected and how |
| [docs/testing.md](docs/testing.md) | Checks, end-to-end tests and benchmarks |
| [docs/adr/](docs/adr/README.md) | Architecture decisions and why |
| [CHANGELOG.md](CHANGELOG.md) | Changes by release |

## Contributing

Bug reports, ideas and pull requests are welcome. Please open an issue before working on a feature, sign off
your commits (`git commit -s`, the Developer Certificate of Origin) and say whether an AI tool helped.
[CONTRIBUTING.md](CONTRIBUTING.md) has the details and the development setup; [GOVERNANCE.md](GOVERNANCE.md)
explains how decisions, triage and releases work. Hoplet is mostly built by its maintainer working with AI coding
agents, which follow [AGENTS.md](AGENTS.md).

## Support

Questions go to [GitHub Discussions](https://github.com/yangflow/hoplet/discussions); bugs to
[issues](https://github.com/yangflow/hoplet/issues). See [SUPPORT.md](SUPPORT.md).

## License

Hoplet is licensed under the [Apache License 2.0](LICENSE) (see also [NOTICE](NOTICE)). Third-party software and
its licenses are listed in [THIRD_PARTY.md](THIRD_PARTY.md).
