# AGENTS.md

The brief for AI coding agents working on Hoplet for its maintainer: what it is, where things are, the commands,
the conventions and the red lines.

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
- Why: [`docs/adr/`](docs/adr/README.md). What next: [`docs/roadmap.md`](docs/roadmap.md) holds the themes and
  their order; the GitHub milestones and their `overview` issues hold the work.

Documents win over code: if they disagree, fix the document first and say so in the pull request.

## Directory boundaries

```
core/      the server: one module per directory under core/src/ (config, log, migrations, store, ingest, control, api)
shared/    schemas, types and error codes of the REST API and both WebSockets: the only contract between the parts
web/       the web UI: screens/, components/, lib/, design tokens, i18n
sdk/       `@hoplet/sdk`, the TypeScript client of the REST API and the browser WebSocket (imports `shared/` only)
cli/       `@hoplet/cli`, the `hoplet` command: sessions, attach, send, wait and administration (imports `sdk/` only)
agent/     the Go daemon: cmd/hoplet-agent + internal/{config,provider,projects,sessions,locate,daemon,sync,runtime,workspace,rules}
image/     the Dockerfile and the compose file
scripts/   repository checks, fixtures, benchmarks, smoke test and their tests
docs/      spec, architecture, API, hosting, threat model, design language, testing, roadmap, ADRs, benchmarks
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
pnpm hoplet --help                  # the CLI (HOPLET_URL + HOPLET_TOKEN or HOPLET_TOKEN_FILE)
pnpm schema                         # regenerate docs/schema/hoplet-api.schema.json; pnpm check fails when it is stale
docker build -f image/Dockerfile -t hoplet:local .
./scripts/smoke.sh                  # end to end: core + agent + generated fixture sessions
scripts/check-version.sh            # VERSION, the package.json files and the agent agree
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

## Conventions

- **Start from the documents** above, then the code. A feature starts from an issue the maintainer agreed to and
  its text in `docs/spec.md`.
- A UI change starts from the mockup in design/; the maintainer confirms the visual direction before UI code is
  written. A new screen or a redesign gets two or three directions in `design/screens/`; never claim a
  confirmation the maintainer did not give.
- **Complete what contributors may leave out**: in the maintainer's pull requests, documents, Simplified Chinese
  strings (`web/src/i18n/zh-Hans.json`) and tests change with the code. Before an outside pull request merges,
  add what it left out.
- **Small pull requests**, one concern each, on a branch named `<type>/<issue>-<slug>`. Commit messages and pull
  request titles are Conventional Commits: `feat|fix|docs|refactor|perf|test|build|ci|chore|revert(scope)!: subject`,
  with the scope `core`, `shared`, `web`, `agent`, `sdk`, `cli`, `image`, `scripts` or `deps` when one fits. The maintainer
  squash-merges, so the title becomes the commit and the changelog line. Commits carry `Co-Authored-By` for the
  agent; no sign-off.
- **Releases**: release-please keeps a release pull request open on `main` and computes the version from the
  merged titles (`feat` raises the minor version before 1.0, `fix` and `perf` the patch), writing `VERSION`, the
  `package.json` files, the agent and `CHANGELOG.md` (`release-please-config.json`). Do not edit `CHANGELOG.md` or
  the version by hand; only the maintainer merges the release pull request. Publishing that release starts
  `.github/workflows/npm.yml`, which publishes `@hoplet/sdk` and `@hoplet/cli` once the maintainer approves the
  `npm` environment; an agent never approves or runs it.
- **A decision that changes the architecture is an ADR** in `docs/adr/`; a decision only the maintainer can make
  goes into an issue with the options, their cost and a recommendation.
- **Follow the language of the thread**: issues, pull requests and replies may be in English or Simplified
  Chinese; answer in the language the maintainer or contributor wrote in.
- **Say what was verified**: report the commands run and their results, and what was not run, as they are.
- **Do not fake what the environment forbids**: if a step cannot be done, stop and report.
- **Never change the maintainer's saved settings** while testing; use flags and environment variables.

## Issues and pull requests

`gh issue create` and `gh pr create --body` skip the issue forms and the pull request template, so write their
structure yourself. Search open issues first; one issue is one problem.

- Issue title: one short sentence that says the problem or the outcome, in English or Simplified Chinese. No type
  prefix such as `[feat]`; the label carries the type.
- Bug, `--label bug`: `## What happened`, `## What you expected`, `## Steps to reproduce`,
  `## Version and environment`.
- Feature or improvement, `--label enhancement`: `## Problem`, `## Proposed solution`,
  `## Alternatives considered` (when there were any), `## Done when`.
- Documentation, `--label documentation`: `## Where`, `## What is missing, unclear or wrong`, `## Suggested change`.
- Milestone: every new issue gets one, a version, `Next` or `Later` (`Later` when it is not scheduled).
- Labels: add the `scope:*` label of each area touched (`scope:core`, `scope:shared`, `scope:web`, `scope:agent`,
  `scope:image`, `scope:scripts`); `security` marks trust-boundary work; `overview` is only for the roadmap issues.
- Pull request: the title is the Conventional Commits subject (it becomes the squash commit). The body has
  `## Summary` (what changes and why), `## Related issue` (`Closes #<n>`), `## How tested` (the commands run and
  their results, and what was not run) and `## Checklist`:
  `- [ ] PR title follows Conventional Commits`, `- [ ] Tests pass`,
  `- [ ] Documentation updated where behavior changed`, ticked only when true.

## Red lines

- Credentials never go into code, pull requests, issues, logs, reports or chat.
- Releases, signing, publishing, anything sent to an external service and deleting anything that is not your own
  need the maintainer's confirmation each time.
- No blind `git add -A` or `git commit -a` of unknown files: stage explicit paths and check the file list.
