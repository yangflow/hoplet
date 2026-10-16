# Third-party software

What Hoplet ships or builds with, and under which license. Versions are in `package.json`, `pnpm-lock.yaml`
and `agent/go.mod`; Dependabot keeps them current.

| Name | License | Part | Shipped | Notice |
|---|---|---|---|---|
| ws | MIT | core | yes | not required |
| valibot | MIT | shared (core, web) | yes | not required |
| react | MIT | web | yes | not required |
| react-dom | MIT | web | yes | not required |
| lucide-react | ISC | web | yes | © Lucide Icons and Contributors, ISC licence (`LICENSE` of the package) |
| @fontsource-variable/inter | OFL-1.1 | web, design | yes | © The Inter Project Authors, SIL Open Font License 1.1 (`LICENSE` of the package) |
| @fontsource-variable/jetbrains-mono | OFL-1.1 | web, design | yes | © The JetBrains Mono Project Authors, SIL Open Font License 1.1 (`LICENSE` of the package) |
| @fontsource-variable/geist | OFL-1.1 | web | yes | © 2024 The Geist Project Authors, SIL Open Font License 1.1 (`LICENSE` of the package) |
| @fontsource-variable/geist-mono | OFL-1.1 | web | yes | © 2024 The Geist Project Authors, SIL Open Font License 1.1 (`LICENSE` of the package) |
| Node.js | MIT | image | yes | not required |
| Debian (bookworm-slim) | Debian licenses | image | yes | `/usr/share/doc/*/copyright` in the image |
| gorilla/websocket | BSD-2-Clause | agent | yes | not required |
| Go | BSD-3-Clause | agent | no (build only) | not required |
| typescript | Apache-2.0 | core, shared, web | no (development only) | not required |
| vite | MIT | web | no (development only) | not required |
| @vitejs/plugin-react | MIT | web | no (development only) | not required |
| @types/node | MIT | core | no (development only) | not required |
| @types/ws | MIT | core | no (development only) | not required |
| @types/react | MIT | web | no (development only) | not required |
| @types/react-dom | MIT | web | no (development only) | not required |
| @biomejs/biome | MIT OR Apache-2.0 | repository | no (development only) | not required |
| vitest | MIT | repository | no (development only) | not required |
| jsdom | MIT | repository | no (development only) | not required |
| axe-core | MPL-2.0 | repository | no (development only) | not required |
| @playwright/test | Apache-2.0 | repository | no (development only) | not required |
| @types/jsdom | MIT | repository | no (development only) | not required |
| @valibot/to-json-schema | MIT | repository | no (development only) | not required |
| pnpm | MIT | repository | no (development only) | not required |

## Notes

- The image runs the Node.js runtime and the built web UI; only `ws`, `valibot` and the workspace packages are runtime
  npm dependencies.
- react/react-dom and lucide-react (the icon glyphs, bundled at build time and never fetched) are in the built web bundle served by the core; they ship inside the image.
- The agent is a static Go binary; Go itself is build-time only.
