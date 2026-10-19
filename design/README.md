# Design

The mockups, design tokens and app icon behind Hoplet's web UI. The design language is described in
[docs/design.md](../docs/design.md); what each screen does is in [docs/spec.md](../docs/spec.md).

## What is here

- `screens/<screen>.html`: one mockup per screen (`cockpit`, `workspace`, `settings`, `action`) with every
  state, in light and dark. The built screens live in `web/src/screens/<screen>/`.
  - `settings` includes the Notifications section: presets, the per-project and per-event overrides, quiet hours,
    This browser (Web Push on, off and blocked), webhooks (`settings-notify`, `settings-notify-overrides`,
    `settings-notify-off`, `settings-notify-blocked`, `notify-hook`).
  - `settings` includes the Model providers section (read-only, a tab per machine and a row per provider its agent
    announces): `settings-providers`, `settings-providers-newer`, `settings-providers-offline`,
    `settings-providers-none`, `settings-providers-empty`.
  - `cockpit` includes the machine icon, the system's mark with the presence dot at its bottom-right: the sidebar
    (`machines`), the draft's machine picker (`machines-draft`) and the Add project machine select (`machines-add`).
  - `cockpit` includes deleting a session: Delete… in the actions menu (`session-menu-delete`), Delete beside Restore
    on a hovered Archived row (`archived`), and the confirmation dialog with its states (`delete`,
    `delete-worktree`, `delete-dirty`, `delete-offline`, `delete-running`, `delete-progress`, `delete-partial`).
  - `cockpit` includes approving a plan into a fresh context: Clear context and approve… in the Approve menu
    (`plan-fresh-menu`, `plan-fresh-confirm`) and the states after it (`plan-fresh-starting`, `plan-fresh-failed`,
    `plan-fresh-done`, `plan-fresh-new`).
  - `action` is the page a signed action link opens and the notifications that lead to it (`os-open`, `confirm`,
    `confirm-answered`, `confirm-expired`, `confirm-used`, `confirm-webui`, `hint-blocked`).
- `screens/inspector-tools.html`: the inspector's Terminal and Browser tabs (#14, #15) with every state, in light and
  dark: Terminal (`terminal-off`, `terminal-blocked-turn`, `-external`, `-handoff`, `-offline`, `terminal-ready`,
  `terminal-open`, `terminal-closed-turn`, `-idle`, `-offline`, `terminal-busy`) and Browser (`browser-ports`,
  `browser-loopback`, `browser-empty`, `browser-offline`), each served under `/__states/workspace/<state>`.
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
