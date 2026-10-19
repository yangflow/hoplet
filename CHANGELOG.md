# Changelog

Each release's notes are made from the titles of the pull requests merged since the previous one (Conventional
Commits: `feat` under Added, `fix` under Fixed, `perf` under Performance; `release-please-config.json`).
Versions follow Semantic Versioning.

## [0.6.0](https://github.com/yangflow/hoplet/compare/v0.5.0...v0.6.0) (2026-10-19)


### Added

* **agent:** Discover and sync Codex sessions started in a terminal ([#164](https://github.com/yangflow/hoplet/issues/164)) ([2ca0a94](https://github.com/yangflow/hoplet/commit/2ca0a945d723de94f20b91cacb7fd689fceda025))
* **agent:** Hand off Codex sessions between machines ([#166](https://github.com/yangflow/hoplet/issues/166)) ([17db5b4](https://github.com/yangflow/hoplet/commit/17db5b4d51bb2c62291b9543706ef08874232ee6)), closes [#161](https://github.com/yangflow/hoplet/issues/161)
* **agent:** Let a driver's vendor assign a new session's ID ([#163](https://github.com/yangflow/hoplet/issues/163)) ([71b4119](https://github.com/yangflow/hoplet/commit/71b4119ac6dbf9333f73015b716ab14b87e1c64c))
* **agent:** Open a terminal on the owner machine ([#149](https://github.com/yangflow/hoplet/issues/149)) ([9282af8](https://github.com/yangflow/hoplet/commit/9282af821db497d11f8633125d299a0ee8290379))
* **agent:** Report a session's dev server ports ([#151](https://github.com/yangflow/hoplet/issues/151)) ([ec72912](https://github.com/yangflow/hoplet/commit/ec729121d9a5d79fdc9ab28cb10e102479def18d))
* **agent:** Start and control Codex sessions through its app server ([#167](https://github.com/yangflow/hoplet/issues/167)) ([2322897](https://github.com/yangflow/hoplet/commit/23228977815a24133753025860a0fca820103390)), closes [#162](https://github.com/yangflow/hoplet/issues/162)
* **core:** Read Codex sessions in the conversation model ([#165](https://github.com/yangflow/hoplet/issues/165)) ([e623feb](https://github.com/yangflow/hoplet/commit/e623feb7a14016df27ec48c2e513f488b2dccc0b)), closes [#160](https://github.com/yangflow/hoplet/issues/160)
* **core:** Relay the terminal and dev server ports to the browser ([#152](https://github.com/yangflow/hoplet/issues/152)) ([880f611](https://github.com/yangflow/hoplet/commit/880f611f861eabe7dc7a48fb94bd640fd1500605))
* Name the vendor in handoff.install so Codex sessions hand off ([#169](https://github.com/yangflow/hoplet/issues/169)) ([ab458d7](https://github.com/yangflow/hoplet/commit/ab458d7c19963b633ebc122d17fb6d18c45977ac))
* **web:** Add the inspector's Terminal and Browser tabs ([#153](https://github.com/yangflow/hoplet/issues/153)) ([d2fc762](https://github.com/yangflow/hoplet/commit/d2fc762515c7c853b2314f46a3c36599cb949582))


### Fixed

* **web:** Let the terminal fill the widened inspector ([#155](https://github.com/yangflow/hoplet/issues/155)) ([2772715](https://github.com/yangflow/hoplet/commit/2772715c741e8d631bee1e02a2b55c4437924d40))
* **web:** Show a shell's exit code once when its terminal closes ([#154](https://github.com/yangflow/hoplet/issues/154)) ([1371ff4](https://github.com/yangflow/hoplet/commit/1371ff41fcbc3e71842d950be75588deb7c42b83))

## [0.5.0](https://github.com/yangflow/hoplet/compare/v0.4.0...v0.5.0) (2026-10-18)


### Added

* **agent:** Declare that a plan can be approved into a new session ([#132](https://github.com/yangflow/hoplet/issues/132)) ([0b97331](https://github.com/yangflow/hoplet/commit/0b9733139e55224a9c1d5f3d35038a72e55a11b6))
* **core:** Approve a plan into a new session ([#135](https://github.com/yangflow/hoplet/issues/135)) ([97b703a](https://github.com/yangflow/hoplet/commit/97b703a7af1053bec1176bc5f6ac353cd5d45d0e))
* **core:** Link a session to the one started from its plan ([#133](https://github.com/yangflow/hoplet/issues/133)) ([6b6eca2](https://github.com/yangflow/hoplet/commit/6b6eca21f90f37e1f83ccf86edd13b0d7c34cfdc))
* **web:** Approve a plan into a new session from the plan card ([#138](https://github.com/yangflow/hoplet/issues/138)) ([aa21553](https://github.com/yangflow/hoplet/commit/aa215530009c5133b58b1029ed52fbdec3b2c5f6))


### Fixed

* **core:** Report a new session's first turn as running ([#129](https://github.com/yangflow/hoplet/issues/129)) ([38e7c3e](https://github.com/yangflow/hoplet/commit/38e7c3e74274468b25ba4e90968a254c6c0f7e93))
* **web:** Keep the Settings row buttons' style out of the conversation ([#137](https://github.com/yangflow/hoplet/issues/137)) ([fca7161](https://github.com/yangflow/hoplet/commit/fca71610f6fa89bd3e5d82c9450bb654f47f0490))

## [0.4.0](https://github.com/yangflow/hoplet/compare/v0.3.0...v0.4.0) (2026-10-17)


### Added

* Clone the repository on a handoff target that has none ([#121](https://github.com/yangflow/hoplet/issues/121)) ([1a61572](https://github.com/yangflow/hoplet/commit/1a61572deb76f09bb63750dbc6e1fa4c5228084b))
* Delete a session, its file and the worktree Hoplet created for it ([#120](https://github.com/yangflow/hoplet/issues/120)) ([e52d41c](https://github.com/yangflow/hoplet/commit/e52d41c4a84438c2dfe86556735463bb27462cfb))
* Notify on permissions, plans, finished turns and failures ([#117](https://github.com/yangflow/hoplet/issues/117)) ([12bfdd2](https://github.com/yangflow/hoplet/commit/12bfdd2177b6d94bdc1e0c791ed360ef8be7aeab))


### Fixed

* **agent:** Ask Claude Code for its session state events ([#128](https://github.com/yangflow/hoplet/issues/128)) ([b3a0a75](https://github.com/yangflow/hoplet/commit/b3a0a758622db0ea3d7caa867fc932fb00f21ee5))
* **agent:** Keep the lines Claude writes after a turn as the session's own ([#126](https://github.com/yangflow/hoplet/issues/126)) ([ebed9c4](https://github.com/yangflow/hoplet/commit/ebed9c466c67f7ed2b02a7271a75fdadcc54aa17))

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
