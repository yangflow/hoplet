# AGENTS.md

The brief for AI coding agents working on Hoplet for its maintainer. Contributors start at `CONTRIBUTING.md`; this
file adds what an agent working on the maintainer's behalf must also do.

## What it is

Hoplet is self-hosted remote control and cross-machine handoff for coding-agent sessions, Claude Code first
(vendors plug in behind provider interfaces, [ADR 0012](docs/adr/0012-vendors-plug-in-behind-provider-interfaces.md)):
a TypeScript core (one container image with the web UI) and a Go agent on each development machine
([ADR 0001](docs/adr/0001-monorepo-with-a-typescript-core-and-a-go-agent.md)). Phones and a native client come later
([ADR 0011](docs/adr/0011-the-web-ui-is-a-desktop-react-application.md)).

## Sources of truth

- What to build and what not: [`docs/spec.md`](docs/spec.md).
- How the parts fit, what each layer may not do, the contracts, where extensions plug in:
  [`docs/architecture.md`](docs/architecture.md).
- Every REST route and browser WebSocket message: [`docs/api.md`](docs/api.md).
- The contract with the user's host: [`docs/hosting.md`](docs/hosting.md).
- What is protected and how: [`docs/threat-model.md`](docs/threat-model.md).
- Design language: [`docs/design.md`](docs/design.md); screens and mockups: [`design/README.md`](design/README.md).
- Why: [`docs/adr/`](docs/adr/README.md). What next: [`ROADMAP.md`](ROADMAP.md) and the GitHub milestones.
- How decisions, triage and releases work: [`GOVERNANCE.md`](GOVERNANCE.md).

Documents win over code: if they disagree, fix the document first and say so in the pull request.

## Directory boundaries

```
core/      the server: one module per directory under core/src/ (config, log, migrations, store, ingest, control, api)
shared/    schemas, types and error codes of the REST API and both WebSockets: the only contract between the parts
web/       the web UI: screens/, components/, lib/, design tokens, i18n
agent/     the Go daemon: cmd/hoplet-agent + internal/{config,provider,projects,sessions,locate,daemon,sync,runtime,workspace}
image/     the Dockerfile and the compose file
scripts/   repository checks, fixtures, benchmarks, smoke test, host CLI `hoplet`, and their tests
docs/      spec, architecture, API, hosting, threat model, design language, testing, ADRs, benchmarks
design/    mockups, design tokens and the app icon
```

The web UI talks to the core only over HTTP/WS; the agent talks to the core only through the protocol in
`shared/`; core modules import each other only through their `index.ts`. Enforced by `scripts/boundaries.ts`.

## Commands

```bash
pnpm install --frozen-lockfile
pnpm check                          # Biome · tsc · Vitest · boundaries · version · tokens · go vet/test
pnpm --filter @hoplet/web build     # the web UI the core serves
pnpm e2e                            # Playwright over every /__states route, axe on each in both schemes
pnpm e2e:image                      # Playwright flows against the built image (builds hoplet:e2e)
pnpm hoplet --help                  # host CLI (HOPLET_URL + HOPLET_TOKEN)
pnpm schema                         # regenerate docs/schema/hoplet-api.schema.json; pnpm check fails when it is stale
docker build -f image/Dockerfile -t hoplet:local .
./scripts/smoke.sh                  # end to end: core + agent + generated fixture sessions
scripts/check-version.sh            # VERSION, the package.json files and the agent agree (releases: GOVERNANCE.md)
```

## Project rules

- The byte cursor is sacred: never filter or rewrite a line on the way into the store, and never let
  `byte_size` drift from the appended bytes ([ADR 0002](docs/adr/0002-the-byte-offset-is-the-sync-cursor.md)).
- No client receives a whole session: reads are pages and blocks
  ([ADR 0007](docs/adr/0007-reads-elide-large-content-blocks.md)).
- Control work is never queued behind bulk work (docs/architecture.md#principles).
- A wire protocol change updates `shared/src/wire/` and `agent/internal/sync/protocol.go` in the same pull
  request (docs/architecture.md#wire-protocol).
- A change to ports, data, resources or upgrades updates `docs/hosting.md`; a change that adds or moves a
  trust boundary updates `docs/threat-model.md`; both in the same pull request.
- Documents and code state what is true now. No notes about earlier behaviour, compatibility shims or
  "previously" wording; history is in Git.

## How an agent works here

- **Start from the documents** above, then the code. A feature starts from an accepted issue and its text in
  `docs/spec.md`; a new screen or a redesign starts as a mockup in `design/screens/` with two or three directions.
  No UI code before the maintainer signs the mockup off; never record a sign-off or an acceptance the maintainer
  did not state.
- **Complete what contributors may leave out**: in the maintainer's pull requests, documents, Simplified Chinese
  strings (`web/src/i18n/zh-Hans.json`) and tests change with the code. Before an outside pull request merges,
  add what it left out.
- **Small pull requests**, one concern each, titled as Conventional Commits (`CONTRIBUTING.md`); commits carry
  `Co-Authored-By` for the agent.
- **A decision that changes the architecture is an ADR**; a decision only the maintainer can make is an issue
  labelled `needs-decision` with the options, their cost and a recommendation.
- **Follow the language of the thread**: issues, pull requests and replies may be in English or Simplified
  Chinese (`CONTRIBUTING.md`); answer in the language the maintainer or contributor wrote in.
- **Say what was verified**: report the commands run and their results, and what was not run, as they are.
- **Do not fake what the environment forbids**: if a step cannot be done, stop and report.
- **Never change the maintainer's saved settings** while testing; use flags and environment variables.

## Red lines

- Credentials never appear in code, pull requests, issues, logs, reports or chat.
- Sending anything to an external service, publishing, releasing, signing and deleting anything that is not your
  own need the maintainer's confirmation each time.
- No blind staging (`git add -A`, `git commit -a`): check the file list.
