# Contributing to Hoplet

Thank you for helping. Hoplet is built mostly by its maintainer working with AI coding agents; contributions from
anyone are welcome, and this page is all you need to make one.

## Languages

Issues, pull requests, commit messages and reviews may be in English or Simplified Chinese; write in the one you
are comfortable with, and replies follow the language of the thread.
欢迎使用中文或英文提交 issue、PR 和撰写提交信息，回复会跟随所在讨论使用的语言。

## Before you write code

- Search the existing [issues](https://github.com/yangflow/hoplet/issues) and pull requests first.
- **Bugs**: open an issue: what you did, what happened, what you expected, and the versions of the core, the agent
  and Claude Code.
- **Features and large changes**: open an issue first and wait for the maintainer to agree before you build it.
  Hoplet builds only what serves its two goals ([docs/spec.md](docs/spec.md#decisions)); a pull request for a
  feature nobody agreed on may be closed. Small fixes can go straight to a pull request.
- **Questions**: open an issue.
- **Vulnerabilities**: never in public; report them privately as [SECURITY.md](SECURITY.md) explains.

## Development setup

You need Node.js 26 (`.node-version`) with pnpm 12, Go 1.27 (`agent/go.mod`), Python 3 for the design tokens check,
and Docker for the image and its end-to-end tests. `AGENTS.md` has the layout, every command and the rules the code
must keep (the byte cursor, pages and blocks, control before bulk, the wire protocol on both sides).

## Making a change

1. Fork the repository and branch from `main`, named `<type>/<issue>-<slug>`, for example
   `fix/42-sidebar-refresh`.
2. Keep the pull request to one concern, ideally under 400 changed lines.
3. Run the checks (`docs/testing.md`) and make sure they pass:

   ```bash
   pnpm install --frozen-lockfile
   pnpm check      # Biome, tsc, Vitest, boundaries, version, tokens, gofmt, go vet and go test
   pnpm e2e        # every web UI state in both schemes, with axe
   ```

4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages and the pull request title:
   `feat|fix|docs|refactor|perf|test|build|ci|chore|revert(scope)!: subject`. Scopes in use are `core`, `shared`,
   `web`, `agent`, `image` and `scripts` (`deps` for dependency updates); the scope is optional. Write the subject
   as a short imperative sentence, for example `fix(web): Keep the focus on the folder list`.
5. Open the pull request, link the issue with `Closes #<n>`, and say what changed, why, how you verified it and
   what you did not run. Leave "Allow edits by maintainers" on.

Pull requests are squash-merged by the maintainer, so the title becomes the commit, and the changelog and the next
version are made from those titles. No DCO or sign-off is required.

## What the maintainer finishes for you

You do not need to touch `CHANGELOG.md` (it is generated at release), write the Simplified Chinese strings (add
the English key to `web/src/i18n/en.json`; the app falls back to English until the translation is added), or
update the documents in `docs/` for your change: the maintainer completes them before merging. If you do update
them, even better.

## UI changes

Small changes to an existing screen are welcome with the end-to-end run as evidence. A new screen or a redesign
starts as a mockup in `design/screens/` that the maintainer reviews before any code; propose it in an issue first.

## AI-assisted contributions

Using AI tools is fine. Say so in the pull request (which tool, and what it did), and make sure you understand and
have run every line you submit. You are responsible for the change as its author.

## License

By contributing you agree that your contribution is licensed under the Apache License 2.0 ([LICENSE](LICENSE)).
