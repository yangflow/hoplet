# Changelog

Each release's notes are made from the titles of the pull requests merged since the previous one (Conventional
Commits: `feat` under Added, `fix` under Fixed, `perf` under Performance; `release-please-config.json`).
Versions follow Semantic Versioning.

## [0.3.0](https://github.com/yangflow/hoplet/compare/v0.2.0...v0.3.0) (2026-10-16)


### Added

* Add the settings page ([#109](https://github.com/yangflow/hoplet/issues/109)) ([a3492b9](https://github.com/yangflow/hoplet/commit/a3492b9a43e72161048d20360e27f29312e5ca6d))
* Complete commands, skills and file mentions in the composer ([#108](https://github.com/yangflow/hoplet/issues/108)) ([f9aa4a9](https://github.com/yangflow/hoplet/commit/f9aa4a93dd3abf44cf02dc62119365f51647f88b))
* Queue or steer messages while a session is running ([#113](https://github.com/yangflow/hoplet/issues/113)) ([caf5c88](https://github.com/yangflow/hoplet/commit/caf5c888528475421e7b7fda22312e1d4673ed0f))
* Review and approve a plan when Claude leaves Plan mode ([#111](https://github.com/yangflow/hoplet/issues/111)) ([74cd14c](https://github.com/yangflow/hoplet/commit/74cd14c44c465befe55b6a6317e2cb7cd8bcf91a))

## [0.2.0](https://github.com/yangflow/hoplet/compare/v0.1.2...v0.2.0) (2026-10-16)


### ⚠ BREAKING CHANGES

* Declare a session's settings as config options ([#103](https://github.com/yangflow/hoplet/issues/103))

### Added

* Change a session's model, Thinking and Fast from the composer ([#102](https://github.com/yangflow/hoplet/issues/102)) ([47a403e](https://github.com/yangflow/hoplet/commit/47a403ebc58a160d4766071efd604721937a5e0b))
* Choose a session's permission mode from the composer ([#104](https://github.com/yangflow/hoplet/issues/104)) ([49e549e](https://github.com/yangflow/hoplet/commit/49e549e06a504e44ec0136e00589331a17479e1b)), closes [#9](https://github.com/yangflow/hoplet/issues/9)
* Rename, pin, copy and archive a session from its actions menu ([#107](https://github.com/yangflow/hoplet/issues/107)) ([947fa82](https://github.com/yangflow/hoplet/commit/947fa829bb2fc5ba387ea9c9094c2fd8f0baeae6))
* Send pasted and attached images with a prompt ([#101](https://github.com/yangflow/hoplet/issues/101)) ([438d61b](https://github.com/yangflow/hoplet/commit/438d61b9f04f12c5dfbf17dfc78abac8373ca155))
* Show the git branch in the composer's location row ([#100](https://github.com/yangflow/hoplet/issues/100)) ([51c11ad](https://github.com/yangflow/hoplet/commit/51c11ad9ca9654663fd54f94608e420666bd89f7))
* **web:** Start sessions from a draft in the composer ([#64](https://github.com/yangflow/hoplet/issues/64)) ([a745b33](https://github.com/yangflow/hoplet/commit/a745b333acb4f1edecd2028a4ecdb6929c071df5))


### Changed

* Declare a session's settings as config options ([#103](https://github.com/yangflow/hoplet/issues/103)) ([f849d79](https://github.com/yangflow/hoplet/commit/f849d7988089d0acc25ced00175f4741f86dd3d7)), closes [#9](https://github.com/yangflow/hoplet/issues/9)

## [0.1.2](https://github.com/yangflow/hoplet/compare/v0.1.1...v0.1.2) (2026-10-12)


### Fixed

* **deps:** Bump lucide-react from 1.53.0 to 1.54.0 ([#59](https://github.com/yangflow/hoplet/issues/59)) ([f4ebabf](https://github.com/yangflow/hoplet/commit/f4ebabf9d9ee6d923479dee2344e912ba7f21670))

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
