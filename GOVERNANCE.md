# Governance

## Roles

- **Maintainer** ([@yangflow](https://github.com/yangflow)): decides what Hoplet builds, reviews and merges every
  pull request, signs off designs and cuts releases. Only the maintainer merges into `main`.
- **AI collaborators**: coding agents working for the maintainer (`AGENTS.md`). They draft specs, designs, code and
  pull requests; the maintainer is responsible for everything they produce, and their commits say so
  (`Co-Authored-By`).
- **Contributors**: anyone who opens an issue, a discussion or a pull request (`CONTRIBUTING.md`).

## Decisions

Hoplet builds what serves its two goals: a large session opens at once and is driven remotely, and a session
moves to another machine and continues there (docs/spec.md#decisions). Day-to-day decisions are made in issues
and pull requests. A decision that shapes the architecture is recorded as an ADR in `docs/adr/`; a reversed one
keeps its file and points to the one that replaced it.

## Issues and triage

- A new issue gets `needs-triage`. The maintainer answers it with one of: `accepted` (and a milestone), `Later`
  milestone (worth doing, not scheduled), `needs-decision` (waiting for the maintainer), or closing it as not
  planned.
- Feature work starts from an accepted issue. A pull request for a feature nobody agreed on may be closed.
- `good first issue` and `help wanted` mark accepted issues that suit contributors.

## Versions and milestones

- Each version in `ROADMAP.md` has one theme and one GitHub milestone. The milestone's issues are what the version
  ships; its overview issue holds the goal, scope and acceptance.
- Once work on a version starts, its scope is fixed: a new idea goes to a later version or `Later`. Something can
  still come in only as a fix that blocks the version's acceptance, or in exchange for something the maintainer
  moves out; the overview issue records the exchange.
- Versions follow Semantic Versioning; before 1.0 a minor version may change formats and APIs.

## Releases

release-please keeps a pull request titled `chore: Release <version>` open on `main`: from the Conventional Commit
titles merged since the last release it computes the next version (`feat` raises the minor version before 1.0,
`fix` and `perf` the patch), writes it to `VERSION`, the `package.json` files and the agent, and adds the release's
notes to `CHANGELOG.md` (`release-please-config.json`). The maintainer merges that pull request to release: the
merge tags `v<version>` and publishes the GitHub Release. Publishing an image needs the maintainer each time.

## Conduct

`CODE_OF_CONDUCT.md` applies everywhere in the project; the maintainer enforces it.
