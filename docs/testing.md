# Testing

## Checks

`pnpm check` runs Biome, `tsc`, Vitest (core, shared, web and the scripts' tests), the import boundaries
(`scripts/boundaries.ts`, docs/architecture.md#boundaries), the version check, the design tokens check
(`design/check-tokens.py`), `gofmt`, `go vet` and `go test -race`. `.github/workflows/ci.yml` runs it on every pull request,
together with the end-to-end tests below and a check of the links between the repository's documents.

## End-to-end tests

Playwright (`web/e2e/`, Chromium) runs in two modes, both part of the image job:

- `pnpm e2e` starts the Vite dev server and opens every `/__states/<screen>/<state>` in both colour schemes.
  axe-core runs with its full rule set, colour contrast included, and any violation fails the test.
- `pnpm e2e:image` runs against the built image: the core in a container with the compose file's isolation, real
  agents built from the current source on the host, and generated fixture sessions. An agent's `claude_bin` is the
  scripted fake `agent/internal/runtime/fakeclaude`. `cockpit.spec.ts` is the cockpit flow: the token gate, the
  sidebar tree, the tail page and paging up, the elided block, the search palette, a prompt and its interrupt, a
  permission request, New session and Add project. The other specs start cores and agents of their own and cover
  the automatable items of `docs/spec.md#acceptance-checklist`: `sync.spec.ts` (the generated fixture of
  `E2E_SYNC_MB` MiB, default 256, syncs with `byte_size` equal to the file size; a restart of the agent or of the
  core during the sync loses and duplicates nothing; `not-owner` and `session.resync` on the agent socket),
  `runtime.spec.ts` (every runtime state the web UI shows, Deny and Interrupt leaving the session usable,
  `external`), `handoff.spec.ts` (online and offline handoff with uncommitted and untracked changes into a second
  agent's own clone, driven through the handoff panel, and a failure at each step injected from outside, with owner,
  cursor and both machines' files compared before and after) and `compose.spec.ts` (`docker compose up -d` with
  `image/compose.yaml`, on a port of its own, until the health check passes, then `down -v`). The time and
  memory budgets of the checklist are not measured here.

Locally both need a Chromium for Playwright (`pnpm exec playwright install chromium`); `pnpm e2e:image` also needs
Docker and Go.
`pnpm e2e:image` builds `hoplet:e2e` unless `E2E_IMAGE` names an image that already exists, as the CI job does with
`hoplet:ci`.

## Evidence for UI changes

Hoplet keeps no screen captures in the repository. A UI pull request carries `pnpm e2e` over every state in both
schemes with axe; the maintainer looks at the change in the running product before merging.

## Benchmarks

`pnpm bench` (`scripts/bench/`) measures the budgets of docs/spec.md#technical-baseline; recorded runs are in
`docs/benchmarks/`.
