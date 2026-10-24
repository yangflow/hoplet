# Changelog

Each release's notes are made from the titles of the pull requests merged since the previous one (Conventional
Commits: `feat` under Added, `fix` under Fixed, `perf` under Performance; `release-please-config.json`).
Versions follow Semantic Versioning.

## [0.10.0](https://github.com/yangflow/hoplet/compare/v0.9.0...v0.10.0) (2026-10-24)


### Added

* **agent:** Give a parent session tools to wait for, collect and cancel its children ([#343](https://github.com/yangflow/hoplet/issues/343)) ([6383575](https://github.com/yangflow/hoplet/commit/63835750df094237691efa7e96fdb50b87be01fd))
* **agent:** Let a session propose changes to its own configuration over MCP ([#347](https://github.com/yangflow/hoplet/issues/347)) ([8b37371](https://github.com/yangflow/hoplet/commit/8b37371d653c2ffa1f5024774f0627ca3746634b))
* **agent:** Let a session read its own configuration over MCP ([#342](https://github.com/yangflow/hoplet/issues/342)) ([55a8606](https://github.com/yangflow/hoplet/commit/55a8606317974939a3fee36d7c9e43fa4a0ae91a))
* **agent:** Let Codex sessions read and change their own configuration ([#350](https://github.com/yangflow/hoplet/issues/350)) ([2f08bbc](https://github.com/yangflow/hoplet/commit/2f08bbcdbb3832e66c018c0a0d6eeec3182143f8))
* **agent:** Page the changes of a child session's workspace ([#340](https://github.com/yangflow/hoplet/issues/340)) ([84fb20f](https://github.com/yangflow/hoplet/commit/84fb20f5d7b70317e495c93875d847622dae72a9))
* **agent:** Write a session's approved configuration changes ([#345](https://github.com/yangflow/hoplet/issues/345)) ([efda80a](https://github.com/yangflow/hoplet/commit/efda80a93f24b32f3adafe6d33c59ec150df57ef))
* **core:** Copy project rules to the target machine on hand-off ([#352](https://github.com/yangflow/hoplet/issues/352)) ([ca1e869](https://github.com/yangflow/hoplet/commit/ca1e8692a1d2e0d9121197602933302d24d1295c))
* **web:** Review a session's requests to change its configuration ([#349](https://github.com/yangflow/hoplet/issues/349)) ([46c4cd4](https://github.com/yangflow/hoplet/commit/46c4cd48acb9ab3f4af3208daee83b9a8e573493))
* **web:** Show a session's children in the inspector ([#346](https://github.com/yangflow/hoplet/issues/346)) ([f7352f9](https://github.com/yangflow/hoplet/commit/f7352f9766cb38f5da9d34dc9a16b92dd5e9bef3))


### Fixed

* **core:** Audit what a parent session does to its children as that session ([#348](https://github.com/yangflow/hoplet/issues/348)) ([37a6328](https://github.com/yangflow/hoplet/commit/37a6328d79fde171cc753cf56b577c09208f6e7a))

## [0.9.0](https://github.com/yangflow/hoplet/compare/v0.8.0...v0.9.0) (2026-10-23)


### Added

* **agent:** Apply permission rules to Codex through a local hook ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#299](https://github.com/yangflow/hoplet/issues/299)) ([76ffa9d](https://github.com/yangflow/hoplet/commit/76ffa9d8a68980eb31840684579d73a68faceca7))
* **agent:** Apply the permission rules the core sends ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#294](https://github.com/yangflow/hoplet/issues/294)) ([17c8fa8](https://github.com/yangflow/hoplet/commit/17c8fa80910119cddb9cc3268780a1e31b21f251))
* **agent:** Check Claude Code tool calls against permission rules ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#290](https://github.com/yangflow/hoplet/issues/290)) ([1173512](https://github.com/yangflow/hoplet/commit/1173512d798ce1a518d5fd7f7782d6be331e606c))
* **agent:** Decide tool calls against permission rules ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#288](https://github.com/yangflow/hoplet/issues/288)) ([d228df0](https://github.com/yangflow/hoplet/commit/d228df0b869dc66b553d4f50d8a93ec634eda9ed))
* **agent:** Give Codex sessions the Hoplet MCP server ([#326](https://github.com/yangflow/hoplet/issues/326)) ([f2402a2](https://github.com/yangflow/hoplet/commit/f2402a2e95a1335221e6b966bb47039201b55a69))
* **agent:** Let sessions send prompts to and interrupt other sessions over MCP ([#322](https://github.com/yangflow/hoplet/issues/322)) ([f7b3338](https://github.com/yangflow/hoplet/commit/f7b333814be10809780a5005a7e8d1e8397a14ef))
* **agent:** Let sessions start other sessions over MCP ([#323](https://github.com/yangflow/hoplet/issues/323)) ([cd64b00](https://github.com/yangflow/hoplet/commit/cd64b003dd429a7819762854fd55d00f4b2f217e))
* **agent:** Serve the session MCP read tools to Claude Code sessions ([#67](https://github.com/yangflow/hoplet/issues/67)) ([#319](https://github.com/yangflow/hoplet/issues/319)) ([a24dc43](https://github.com/yangflow/hoplet/commit/a24dc43a2dbed9351bd96c52b33efbd3cffb7ac0))
* **cli:** Add the @hoplet/cli command for sessions, attach, send and wait ([#71](https://github.com/yangflow/hoplet/issues/71)) ([#314](https://github.com/yangflow/hoplet/issues/314)) ([18ace63](https://github.com/yangflow/hoplet/commit/18ace633bccc9e172bffd535d8e30f5fd63010dd))
* **core:** Add session tokens, session capabilities and loop limits for session MCP ([#67](https://github.com/yangflow/hoplet/issues/67)) ([#318](https://github.com/yangflow/hoplet/issues/318)) ([622a789](https://github.com/yangflow/hoplet/commit/622a789e073c54b051feb7a0c95d0b1a4c78af1f))
* **core:** Issue scoped API tokens and check their scope on every route ([#71](https://github.com/yangflow/hoplet/issues/71)) ([#310](https://github.com/yangflow/hoplet/issues/310)) ([3d6f32f](https://github.com/yangflow/hoplet/commit/3d6f32f32efb769c05b97aff34ff7c0b5f942871))
* **core:** Let a session propose changes to its own configuration ([#338](https://github.com/yangflow/hoplet/issues/338)) ([343a305](https://github.com/yangflow/hoplet/commit/343a305ee879159b7857e3e63b74012fa37cd65f))
* **core:** Let a session request a hand-off over session MCP ([#325](https://github.com/yangflow/hoplet/issues/325)) ([41644a5](https://github.com/yangflow/hoplet/commit/41644a53b44385ad9174d99e57e52082d629c6f6))
* **core:** Make a child session obey its parent's deny and ask rules ([#337](https://github.com/yangflow/hoplet/issues/337)) ([decb317](https://github.com/yangflow/hoplet/commit/decb31746815f53b7072f00496a28721b14ae1d5))
* **core:** Record actions in a hash-chained audit log ([#80](https://github.com/yangflow/hoplet/issues/80)) ([#276](https://github.com/yangflow/hoplet/issues/276)) ([bde3535](https://github.com/yangflow/hoplet/commit/bde353594eabc719bc17871362ca3f606802afd1))
* **core:** Record rule decisions and rule changes in the audit log ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#295](https://github.com/yangflow/hoplet/issues/295)) ([c1fe63f](https://github.com/yangflow/hoplet/commit/c1fe63f5f24663e98c56a6a61d7db5125de72368))
* **core:** Record tool calls and session events in the audit log ([#80](https://github.com/yangflow/hoplet/issues/80)) ([#298](https://github.com/yangflow/hoplet/issues/298)) ([3852159](https://github.com/yangflow/hoplet/commit/3852159ce921060468ec504c3c8277f7adf10db6))
* **core:** Send session lifecycle events to webhooks ([#97](https://github.com/yangflow/hoplet/issues/97)) ([#300](https://github.com/yangflow/hoplet/issues/300)) ([f6df4cf](https://github.com/yangflow/hoplet/commit/f6df4cfb49048f315538ea447d4d477646f0ee9d))
* **core:** Show each machine's own deny rules ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#297](https://github.com/yangflow/hoplet/issues/297)) ([ad390ab](https://github.com/yangflow/hoplet/commit/ad390ab01c610b498ea1d03e6293bd31ac4868af))
* **core:** Start a session in a new git worktree from the CLI and SDK ([#306](https://github.com/yangflow/hoplet/issues/306)) ([#316](https://github.com/yangflow/hoplet/issues/316)) ([ab49761](https://github.com/yangflow/hoplet/commit/ab4976172c8b26cf30c6725461ed9b9fef06af85))
* **core:** Store permission rules and serve them over the API ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#293](https://github.com/yangflow/hoplet/issues/293)) ([fa89733](https://github.com/yangflow/hoplet/commit/fa8973366f029787a70c25cfa43b31a8584525ea))
* **core:** Store session templates and start a session from one ([#76](https://github.com/yangflow/hoplet/issues/76)) ([#278](https://github.com/yangflow/hoplet/issues/278)) ([1a3d614](https://github.com/yangflow/hoplet/commit/1a3d614de485222d71bb789576d1ea2cf6403d12))
* **core:** Track child sessions and limit a tree of work ([#336](https://github.com/yangflow/hoplet/issues/336)) ([666d85a](https://github.com/yangflow/hoplet/commit/666d85a385a67c270a847f8e0263a8ba5770f29d))
* Run local script hooks on session lifecycle events ([#97](https://github.com/yangflow/hoplet/issues/97)) ([#304](https://github.com/yangflow/hoplet/issues/304)) ([6ad8483](https://github.com/yangflow/hoplet/commit/6ad8483c916ef3ef6ba87d8f620aa876e859af11))
* **sdk:** Add the @hoplet/sdk client for the REST API and the browser WebSocket ([#71](https://github.com/yangflow/hoplet/issues/71)) ([#313](https://github.com/yangflow/hoplet/issues/313)) ([d2f7af4](https://github.com/yangflow/hoplet/commit/d2f7af48e83c933e56249b01a4dfc134f1d83b11))
* Show each machine's rule revision and the Codex rule notes ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#308](https://github.com/yangflow/hoplet/issues/308)) ([ad18e21](https://github.com/yangflow/hoplet/commit/ad18e2184bee99e65aec5138c4b71d74efb7cefd))
* Show which rule decided a tool call in the conversation ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#302](https://github.com/yangflow/hoplet/issues/302)) ([2fa52d3](https://github.com/yangflow/hoplet/commit/2fa52d3c53347484db7d98d45dc2083c39b6e89c))
* **web:** Edit permission rules in Settings and from the permission card ([#98](https://github.com/yangflow/hoplet/issues/98)) ([#301](https://github.com/yangflow/hoplet/issues/301)) ([9be7c94](https://github.com/yangflow/hoplet/commit/9be7c941dd77b3d31e5c0bebc4e3239f12418f4a))
* **web:** Issue, list and revoke API tokens in Settings → API tokens ([#71](https://github.com/yangflow/hoplet/issues/71)) ([#312](https://github.com/yangflow/hoplet/issues/312)) ([3eee123](https://github.com/yangflow/hoplet/commit/3eee1233c85389be662ba913df920f4772975783))
* **web:** Manage session templates in Settings ([#76](https://github.com/yangflow/hoplet/issues/76)) ([#287](https://github.com/yangflow/hoplet/issues/287)) ([a61114b](https://github.com/yangflow/hoplet/commit/a61114bfbc632dafa87281924fcd0c95783e40b6))
* **web:** Search, inspect, verify and export the audit log in Settings → Audit ([#80](https://github.com/yangflow/hoplet/issues/80)) ([#309](https://github.com/yangflow/hoplet/issues/309)) ([485578b](https://github.com/yangflow/hoplet/commit/485578b49a81ce7ed0e43a15e1056b42d1f6e154))
* **web:** Set what a session may do to other sessions ([#324](https://github.com/yangflow/hoplet/issues/324)) ([e0aa0cd](https://github.com/yangflow/hoplet/commit/e0aa0cd75aaf30c154f70c0d81851e7cd571e6f4))
* **web:** Show a session's audit records in the inspector's Audit tab ([#332](https://github.com/yangflow/hoplet/issues/332)) ([1462889](https://github.com/yangflow/hoplet/commit/1462889ec17ded476415851934e724ca171a59c2))
* **web:** Show webhooks and machine scripts in Settings → Hooks ([#97](https://github.com/yangflow/hoplet/issues/97)) ([#311](https://github.com/yangflow/hoplet/issues/311)) ([908a734](https://github.com/yangflow/hoplet/commit/908a734553a9d98a3d7c13f3115d34b7d8de4989))
* **web:** Start a session from a template in the composer ([#76](https://github.com/yangflow/hoplet/issues/76)) ([#289](https://github.com/yangflow/hoplet/issues/289)) ([56b319c](https://github.com/yangflow/hoplet/commit/56b319c3b2a8cbc8abeadf45be269aeb6f2dba95))
* **web:** Start from a template in the sidebar and the palette ([#76](https://github.com/yangflow/hoplet/issues/76)) ([#291](https://github.com/yangflow/hoplet/issues/291)) ([039320b](https://github.com/yangflow/hoplet/commit/039320bdb13c09cc5e816ea8f43a708d12d8df05))


### Fixed

* **web:** Keep the select style off opened rows in Settings ([#333](https://github.com/yangflow/hoplet/issues/333)) ([fdfae5b](https://github.com/yangflow/hoplet/commit/fdfae5bfaf50abc7ca233d48d7ac5fd9ef32e63e))

## [0.8.0](https://github.com/yangflow/hoplet/compare/v0.7.0...v0.8.0) (2026-10-21)


### Added

* **core:** Budgets that alert and can pause new prompts ([#75](https://github.com/yangflow/hoplet/issues/75)) ([#265](https://github.com/yangflow/hoplet/issues/265)) ([254ff58](https://github.com/yangflow/hoplet/commit/254ff58f0793d6d3727e2120e9e06b6c8463d94e))
* **core:** Let a budget's allowance be withdrawn ([#75](https://github.com/yangflow/hoplet/issues/75)) ([#267](https://github.com/yangflow/hoplet/issues/267)) ([5fb06cc](https://github.com/yangflow/hoplet/commit/5fb06cc5b6c2a6150aaf8132d6028e7226b25b83))
* **core:** Record token usage per message and report usage and estimated cost ([#75](https://github.com/yangflow/hoplet/issues/75)) ([#200](https://github.com/yangflow/hoplet/issues/200)) ([4056631](https://github.com/yangflow/hoplet/commit/4056631ef903997059be8f48243d9170ef2f560e))
* **core:** Store machine capabilities and labels reported by the agent ([#70](https://github.com/yangflow/hoplet/issues/70)) ([#195](https://github.com/yangflow/hoplet/issues/195)) ([c97f48c](https://github.com/yangflow/hoplet/commit/c97f48c896cb621b1196727f0f3a113e788ccdbd))
* **web:** Set budgets and act on a paused budget ([#75](https://github.com/yangflow/hoplet/issues/75)) ([#269](https://github.com/yangflow/hoplet/issues/269)) ([154f396](https://github.com/yangflow/hoplet/commit/154f396927d51bfc598d001b3105a434fb01f7ac))
* **web:** Show machine capabilities and labels in Settings › Machines ([#70](https://github.com/yangflow/hoplet/issues/70)) ([#198](https://github.com/yangflow/hoplet/issues/198)) ([185a5c7](https://github.com/yangflow/hoplet/commit/185a5c7fe57f371b8592e96aec77a80d7b8c3890))
* **web:** Show usage and estimated cost in Settings and the session top bar ([#75](https://github.com/yangflow/hoplet/issues/75)) ([#261](https://github.com/yangflow/hoplet/issues/261)) ([3b6d1a5](https://github.com/yangflow/hoplet/commit/3b6d1a54d1b876272c51ea1f256a64b49c9e7b6e))


### Fixed

* **deps:** Bump github.com/klauspost/compress from 1.18.0 to 1.18.7 in /agent ([#197](https://github.com/yangflow/hoplet/issues/197)) ([3b0ce4d](https://github.com/yangflow/hoplet/commit/3b0ce4dfceafe7e57eb45506d0e05039d0605218))
* **web:** Keep the action page's styles off the composer footer ([#266](https://github.com/yangflow/hoplet/issues/266)) ([#268](https://github.com/yangflow/hoplet/issues/268)) ([f931489](https://github.com/yangflow/hoplet/commit/f93148903926eb432b44b879eb9dba0dfa6fee07))

## [0.7.0](https://github.com/yangflow/hoplet/compare/v0.6.0...v0.7.0) (2026-10-20)


### Added

* **core:** Keep each session's provider configuration and let the web update configurations ([#170](https://github.com/yangflow/hoplet/issues/170)) ([#184](https://github.com/yangflow/hoplet/issues/184)) ([588de0b](https://github.com/yangflow/hoplet/commit/588de0b5ef5f0a28abd5b58ab33eaddd8b8a29e9))
* **core:** Keep resource warning thresholds and apply them to each machine ([#188](https://github.com/yangflow/hoplet/issues/188)) ([4d3405f](https://github.com/yangflow/hoplet/commit/4d3405f8fd833cda3223a1b5764e6afbf9cd7d2c))
* Define provider configurations on each machine ([#170](https://github.com/yangflow/hoplet/issues/170)) ([#183](https://github.com/yangflow/hoplet/issues/183)) ([5a762dd](https://github.com/yangflow/hoplet/commit/5a762dd2d1c51d0cc83b20990127699b28b0032c))
* Lead each machine with its system's mark and a presence dot ([#174](https://github.com/yangflow/hoplet/issues/174)) ([bdf3dd0](https://github.com/yangflow/hoplet/commit/bdf3dd01d7c77a73881d40589b6aab9e09ebac57))
* Show each machine's resources and its sessions' usage ([#88](https://github.com/yangflow/hoplet/issues/88)) ([#181](https://github.com/yangflow/hoplet/issues/181)) ([8b9ac07](https://github.com/yangflow/hoplet/commit/8b9ac0780a25406b0ce340f2272cddf7634b1cc2))
* **web:** Adjust resource warning thresholds in Settings › Machines ([#189](https://github.com/yangflow/hoplet/issues/189)) ([47738c8](https://github.com/yangflow/hoplet/commit/47738c8588a5dca2fac1e88efdf376e14e6982f9))
* **web:** Enable a target's configuration from the handoff card ([#185](https://github.com/yangflow/hoplet/issues/185)) ([#187](https://github.com/yangflow/hoplet/issues/187)) ([96adee4](https://github.com/yangflow/hoplet/commit/96adee4d0c12ff01032c0516f0a168a0f70485d1))
* **web:** Manage provider configurations and choose one for new sessions and handoffs ([#170](https://github.com/yangflow/hoplet/issues/170)) ([#186](https://github.com/yangflow/hoplet/issues/186)) ([cc1f173](https://github.com/yangflow/hoplet/commit/cc1f173dd162929ed99bf2b2490a9e61d37d9140))
* **web:** Show each machine's model providers in Settings ([#171](https://github.com/yangflow/hoplet/issues/171)) ([a184efb](https://github.com/yangflow/hoplet/commit/a184efba99a95c7618a184288902afab2cd711b0))


### Fixed

* **agent:** Report Claude Code's exit when it dies before reading the prompt ([#193](https://github.com/yangflow/hoplet/issues/193)) ([81a5430](https://github.com/yangflow/hoplet/commit/81a543085811012cf0fc003af8c55cd16870b195))
* **deps:** Bump lucide-react from 1.54.0 to 1.58.0 ([#178](https://github.com/yangflow/hoplet/issues/178)) ([96e1806](https://github.com/yangflow/hoplet/commit/96e18065b79adf95ba7e5fa07f62c8a41ff5e30d))
* **deps:** Bump lucide-react from 1.58.0 to 1.59.0 ([#192](https://github.com/yangflow/hoplet/issues/192)) ([f6601e7](https://github.com/yangflow/hoplet/commit/f6601e7afb2c6a5b4071b4de877e3d7a87a058a5))
* **deps:** Bump react and react-dom together to 19.3.1 ([#191](https://github.com/yangflow/hoplet/issues/191)) ([97659fd](https://github.com/yangflow/hoplet/commit/97659fdb4e3eafddf664cf84f007bdf0711075bf))

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
