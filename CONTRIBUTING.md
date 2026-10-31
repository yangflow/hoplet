# Contributing to Hoplet

Thanks for your interest. Hoplet is a personal project that welcomes outside contributions. This guide covers how to propose changes and what a pull request needs before it can be merged.

Issues and pull requests may be written in English or Chinese.

## Before you start

- Read [`docs/README.md`](docs/README.md) and the design doc for the area you want to change (`docs/design/`, in Chinese).
- **Changes that touch the design** — new behavior, protocol or storage changes, new dependencies — start as an issue. Agree on the approach there before writing code.
- Small fixes (typos, obvious bugs, tests) can go straight to a pull request.

## Development setup

| Tool | Version |
|---|---|
| Go | 1.27 or newer |
| golangci-lint | 2.x |
| Xcode | Latest stable (iOS app only) |

Common commands:

```sh
make check   # format check, lint, tests, generated-code check
make test    # tests with the race detector
make gen     # regenerate code (e.g. Swift types from proto/)
```

Run `make check` before opening a pull request; CI runs the same checks.

## Branches and commits

- `main` is protected. All changes land through pull requests.
- Branch names: `<type>/<short-description>`, for example `feat/relay-keepalive` or `fix/shim-reconnect`.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/), in English:

  ```
  feat(relay): add end-to-end echo probe
  fix(shim): resume from the last acknowledged offset
  docs(design): clarify approval relay
  ```

  Types: `feat`, `fix`, `perf`, `refactor`, `test`, `docs`, `build`, `ci`, `chore`. Scopes follow the module: `daemon`, `shim`, `store`, `sync`, `noise`, `relay`, `harness/<kind>`, `workspace`, `trigger`, `desktop`, `ios`, `docs`, `ci`.
- Pull requests are squash-merged, so the **pull request title** becomes the commit on `main` and must follow the same format. CI checks it.

## Developer Certificate of Origin

Every commit must be signed off, certifying that you wrote the change or have the right to submit it under the project's license ([DCO 1.1](https://developercertificate.org/)):

```sh
git commit -s
```

This adds a `Signed-off-by: Your Name <you@example.com>` trailer. CI rejects pull requests with unsigned commits; commits made by GitHub bot accounts (such as the release automation) are exempt. To fix a branch, run `git rebase --signoff main` and force-push.

## Language

| Content | Language |
|---|---|
| Code, identifiers, comments, commit messages | English |
| Design docs in `docs/` | Chinese |
| README, CONTRIBUTING, templates and other repository docs | English (README also has a Chinese version) |
| Issues and pull request discussions | English or Chinese |

## Code style

**Go**

- `gofmt` and `goimports`; `golangci-lint` must pass with the repository config.
- Wrap errors with `%w`; pass `context.Context` as the first parameter; no panics in library code; no mutable global state.
- Log with `log/slog`.
- Exported identifiers have doc comments.
- Tests are table-driven where it helps and run with `-race`.

**Swift**

- Swift 6 with strict concurrency; warnings are errors; `swift-format` with the repository config.
- Swift Testing for tests.
- Standard system components and the Human Interface Guidelines for UI.

**Desktop (MyGo)**

- Use the pinned MyGo version; run `go tool mygo vet`.
- Keep views thin — state belongs in the protocol client — and test views with `ui.Tester`.

**Generated code**

Generated files are committed. CI fails if `make gen` changes anything, so run it after editing `proto/`.

## Design changes

If a pull request changes a decision recorded in `docs/design/`, update that doc in the same pull request. Reviewers treat a design change without a doc change as incomplete.

## Pull requests

- Keep each pull request focused on one change.
- Fill in the template: what changed, why, how it was tested.
- New dependencies, a new language or a new runtime need agreement in an issue first.
- A maintainer reviews every pull request; CI must be green before merge.

## Reporting bugs and security issues

- Bugs and feature ideas: open an issue using the templates.
- Security vulnerabilities: **do not open a public issue** — see [SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
