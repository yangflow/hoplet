# Design

The mockups, design tokens and app icon behind Hoplet's web UI. The design language is described in
[docs/design.md](../docs/design.md); what each screen does is in [docs/spec.md](../docs/spec.md).

## What is here

- `screens/<screen>.html`: one mockup per screen (`cockpit`, `workspace`, `settings`, `action`) with every
  state, in light and dark. The built screens live in `web/src/screens/<screen>/`.
  - `settings` includes the Notifications section: presets, the per-project and per-event overrides, quiet hours,
    This browser (Web Push on, off and blocked), webhooks (`settings-notify`, `settings-notify-overrides`,
    `settings-notify-off`, `settings-notify-blocked`, `notify-hook`).
  - `cockpit` includes deleting a session: Delete… in the actions menu (`session-menu-delete`), Delete beside Restore
    on a hovered Archived row (`archived`), and the confirmation dialog with its states (`delete`,
    `delete-worktree`, `delete-dirty`, `delete-offline`, `delete-running`, `delete-progress`, `delete-partial`).
  - `action` is the page a signed action link opens and the notifications that lead to it (`os-open`, `confirm`,
    `confirm-answered`, `confirm-expired`, `confirm-used`, `confirm-webui`, `hint-blocked`).
- `tokens.html`: every design token, with the JSON block that `check-tokens.py` compares with
  `web/src/design/tokens.css`.
- `mark.html`: the app icon and the web UI's mark; the exported icon is `web/public/icon.svg`.
- `fonts/`: Inter and JetBrains Mono with their licenses, used by the mockups.

## Previewing

Open a mockup in a browser. It shows every state stacked; `?state=<id>&scheme=light|dark` shows one window at real
size. In the running app, every state of a built screen renders at `/__states/<screen>/<state>` (`pnpm dev:web`).

## Checking and regenerating

- `python3 design/check-tokens.py` checks that the tokens in `tokens.html` match `web/src/design/tokens.css`;
  `pnpm check` runs it.
- The icon is drawn in `mark.html`; when it changes, update `web/public/icon.svg` from it by hand.

A UI change starts from the mockup here; mockups, code and spec change together in the same pull request.
