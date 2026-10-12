# Contributing to Hoplet

Thank you for helping. Hoplet is built mostly by its maintainer working with AI coding agents; contributions from
anyone are welcome, and this page is all you need to make one.

## Languages

Issues, discussions, pull requests, commit messages and reviews may be in English or Simplified Chinese; write in
the one you are comfortable with, and replies follow the language of the thread.
欢迎使用中文或英文提交 issue、参与讨论、提交 PR 和撰写提交信息，回复会跟随所在讨论使用的语言。

## Before you write code

- **Bugs**: open an issue with the Bug form: what you did, what happened, what you expected, and the versions.
- **Features**: open an issue with the Feature idea form first and wait for it to be accepted (`GOVERNANCE.md`).
  Hoplet builds only what serves its two goals (docs/spec.md#decisions); a pull request for a feature nobody agreed
  on may be closed.
- **Questions**: GitHub Discussions (`SUPPORT.md`).
- **Vulnerabilities**: never in public (`SECURITY.md`).

## Making a change

1. Read `AGENTS.md` for the layout, the commands and the rules the code must keep (the byte cursor, pages and
   blocks, control before bulk, the wire protocol on both sides).
2. Branch from `main`; keep the pull request to one concern, ideally under 400 changed lines.
3. Sign off every commit with `git commit -s`: the `Signed-off-by` line certifies the
   [Developer Certificate of Origin](https://developercertificate.org/). The DCO check fails without it.
4. Run the checks (`docs/testing.md`):

   ```bash
   pnpm install --frozen-lockfile
   pnpm check      # Biome, tsc, Vitest, boundaries, version, tokens, go vet and test
   pnpm e2e        # every web UI state in both schemes, with axe
   ```

5. Title the pull request as a [Conventional Commit](https://www.conventionalcommits.org/): `type(scope): Subject`,
   with the type `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci` or `chore`, the scope one of
   `core`, `shared`, `web`, `agent`, `image` or `scripts` (`deps` for dependency updates), and a subject of at most 50 characters,
   in English (an imperative sentence starting with a capital letter) or in Chinese. Pull requests are squash-merged, so the title becomes the commit, and the changelog and the next
   version are made from those titles.
6. Fill in the template: what, why, how you verified it, what you did not run. Leave "Allow edits by maintainers"
   on.

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

By contributing you agree that your contribution is licensed under the Apache License 2.0 (`LICENSE`), as the
sign-off certifies.
