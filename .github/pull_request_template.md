## What

<!-- One or two sentences. Closes #n -->

## Why

## How to verify

- [ ] `pnpm check`
- [ ] `pnpm e2e` (UI changes: every state, both schemes, axe)

## Checklist

- [ ] Every commit is signed off (`git commit -s`, Developer Certificate of Origin)
- [ ] Docs updated where behaviour changed (`docs/`), if you can; otherwise the maintainer completes them
- [ ] A wire protocol change updates `shared/src/wire/` and `agent/internal/sync/protocol.go` together
- [ ] User-visible copy in `web/src/i18n/en.json` and `zh-Hans.json`

## Known limitations
