# Changelog

Each release's notes are made from the titles of the pull requests merged since the previous one (Conventional
Commits: `feat` under Added, `fix` under Fixed, `perf` under Performance; `release-please-config.json`).
Versions follow Semantic Versioning.

## [0.1.1](https://github.com/yangflow/hoplet/compare/v0.1.0...v0.1.1) (2026-10-12)


### Fixed

* Accept only known states in the mockups ([18520f6](https://github.com/yangflow/hoplet/commit/18520f69c56c2bdf459bdb3642eec8bfc0353385))
* **deps:** Bump github.com/spf13/cobra from 1.10.1 to 1.10.2 in /agent ([#26](https://github.com/yangflow/hoplet/issues/26)) ([2362662](https://github.com/yangflow/hoplet/commit/2362662f53b0d95a55089b54e5d2d08a5838a899))
* **deps:** Bump lucide-react from 1.52.0 to 1.53.0 ([#33](https://github.com/yangflow/hoplet/issues/33)) ([d974065](https://github.com/yangflow/hoplet/commit/d9740653587e0c4ca71d2203d3be77db224025ca))
* **deps:** Bump ws from 8.18.3 to 8.22.0 ([#34](https://github.com/yangflow/hoplet/issues/34)) ([1abb3e3](https://github.com/yangflow/hoplet/commit/1abb3e3ea280a69773717423234fb73c1c4b6037))
* Escape design tokens in the mockups' token table ([#53](https://github.com/yangflow/hoplet/issues/53)) ([d28cb96](https://github.com/yangflow/hoplet/commit/d28cb962219d1ea51906d45cfe268d4731e4ee15))
* **web:** Keep the focus on the folder list in Add project ([#23](https://github.com/yangflow/hoplet/issues/23)) ([0ad6165](https://github.com/yangflow/hoplet/commit/0ad6165287add709042b490a39090c1aaab95f4a))
* **web:** Put the visible text in accessible names ([#45](https://github.com/yangflow/hoplet/issues/45)) ([dd010ce](https://github.com/yangflow/hoplet/commit/dd010cebad4be042f33798b29a59b105cd97bed2))

## [Before the first release]

### Added
- Hoplet is open source under the Apache License 2.0.
- Claude Code sessions sync incrementally from every development machine to a server you run.
- Sessions open from the latest messages; earlier history loads as you scroll.
- Search across all sessions or within one.
- Add a machine from the web UI with its own access token.
- Read-only view of the files in a session's working tree.
- Hand off a session to another machine from the web UI: it finds the clone to continue in, or asks for its
  path, and shows each step and the new worktree.
