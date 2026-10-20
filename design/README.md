# Design

The mockups, design tokens and app icon behind Hoplet's web UI. The design language is described in
[docs/design.md](../docs/design.md); what each screen does is in [docs/spec.md](../docs/spec.md).

## What is here

- `screens/<screen>.html`: one mockup per screen (`cockpit`, `workspace`, `settings`, `action`) with every
  state, in light and dark. The built screens live in `web/src/screens/<screen>/`.
  - `settings` includes the Notifications section: presets, the per-project and per-event overrides, quiet hours,
    This browser (Web Push on, off and blocked), webhooks (`settings-notify`, `settings-notify-overrides`,
    `settings-notify-off`, `settings-notify-blocked`, `notify-hook`).
  - `settings` includes the Model providers section (#170), a tab per machine with that machine's configurations, in
    rows grouped by vendor with an inline expand (`settings-providers`). It has `-edit` (name, enabled, new-session defaults), `-saving`, `-error` and `-add`
    (the command to copy; the web never runs it); `settings-providers-offline`, `settings-providers-none` and
    `settings-providers-empty` show the other cases. Command and variables are read-only, variables show set or not set, never
    a value; keys are entered on the machine.
  - `cockpit` includes the provider configurations (#170): the draft's configuration picker
    (`draft-configuration`), the configuration tag of a session that does not run the built-in
    (`session-configuration`) and the configuration select of Hand off (`handoff-configuration`,
    `handoff-configuration-pick`, `handoff-configuration-none`). When the target has the vendor's configurations
    but none enabled (#185), the card lists them with Enable (`handoff-configuration-enable`,
    `handoff-configuration-enabling`).
  - `cockpit` includes the machine icon, the system's mark with the presence dot at its bottom-right: the sidebar
    (`machines`), the draft's machine picker (`machines-draft`) and the Add project machine select (`machines-add`).
  - `cockpit` includes each machine's load and session processes (#88): the sidebar carries only a warning glyph on a
    machine over a threshold (`resources`), and the machine's ··· menu has a Resources item, hidden for a machine that
    reports nothing, that opens Settings › Machines on that machine's tab (`resources-menu`). `settings`
    includes the detail: Machines as a tab per machine like Model providers, with the selected machine's facts line
    and three stat tiles with the last hour, the warning line, memory and CPU as stacked bars by session, and the
    running sessions as the main list with their 1-hour memory line, CPU, memory and child processes
    (`settings-resources`); an offline machine greys its last values and says its sessions are unknown
    (`settings-resources-offline`).
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
