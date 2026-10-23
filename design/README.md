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
  - `settings` includes the resource warning thresholds (#175): the selected machine's tab ends with a Warnings group,
    Default (the values every machine uses, with Change default) or Custom for that machine (`settings-thresholds`,
    `settings-thresholds-default`, `settings-thresholds-custom`).
  - `settings` includes the machine capabilities (#70): a Capabilities group in the selected machine's tab, after the
    resources and before Warnings, with the toolchain versions, the GPU, the origin of each watched project and the
    custom labels. Label–value rows: toolchains as name-and-version chips with the missing ones named, the GPU line,
    repositories as project and origin, labels as removable chips with an inline Add label (`settings-capabilities`).
    Variants: the Apple chip on the MacBook Pro (`-mac`), adding, removing and an invalid label with its rule
    (`-label-add`, `-label-remove`, `-label-invalid`), few tools and no GPU (`-sparse`), an offline machine with its
    last report greyed and the labels still editable (`-offline`), and an agent that reports nothing, only labels
    (`-none`); all prefixed `settings-capabilities`.
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
- `screens/usage.html`: usage, estimated cost and budgets (#75), in light and dark, in English and Simplified Chinese
  (`-zh`); open one with `?state=<id>&scheme=light|dark`. Settings › Usage in the direction the maintainer chose, A
  (`usage`: totals, a chart per day stacked by model and a ranked table) over compact tiles with a dense table and
  over a table-first page; `usage-session`, `-machine` and `usage-unknown-model` group it by session, machine or
  model, with `usage-custom`, `usage-tokens-only`, `usage-empty` and `usage-backfill`. The session top bar
  (`session-usage`, `session-usage-open`, `-unknown`, `-near`). Budgets (`budgets` with hoplet-docs under its limit,
  `budgets-pause` with it over the limit in pause mode, `budgets-allowed`, `budgets-edit`, `budgets-empty`). The
  composer when a prompt is refused (`composer-over-budget`, `composer-over-budget-running`). Prices (`prices`,
  `prices-edit`, `prices-tokens-only`).
- `screens/templates.html`: session templates (#76), in light and dark, in English and Simplified Chinese; open one
  with `?state=<id>&scheme=light|dark&lang=zh-Hans`. The picker in the draft is a Templates popover in the composer
  footer (`picker`); a template without fields fills the draft, with the capability warning inline when the target
  does not offer its options (`warning`), and one with fields opens the small fill form with a preview of the prompt
  (`fill`). Saving: `save`, `save-invalid`, `save-menu`. One click: `sidebar-menu`, `palette`, `palette-fill`.
  Settings › Templates: `list` as rows, `list-edit`, `list-empty`, `list-import`.
- `screens/rules.html`: permission rules (#98), in light and dark, in English and Simplified Chinese (`-zh`); open
  one with `?state=<id>&scheme=light|dark`. Settings › Permission rules, direction A (chosen): one list grouped by
  scope (`a-list`, `a-builtin`, `a-builtin-confirm`, `a-edit`, `a-edit-invalid`, `a-empty`, `a-zh`), with the
  built-in rules behind an off switch and the second confirmation that names the rule, and the read-only rules set
  on a machine. Directions B (a tab per scope, `b-*`) and C (a list with the editor beside it, `c-*`) stay in the
  file as the alternatives considered. The permission card, direction 1 (chosen): Always allow… and Always deny…
  as quiet buttons (`card-a`, `card-a-rule` for a call a rule asked about, `card-a-nodraft` and `card-a-other` where
  no draft is offered, `card-a-zh`); the draft they open (`draft-session`, `draft-project`, `draft-deny`,
  `draft-session-zh`); the conversation lines for Allowed by rule, Denied by rule and Asked by rule (`conv-auto`,
  `conv-auto-zh`). The one Always menu (`card-b`, `card-b-menu`) is the alternative considered. Built in
  `screens/settings/` and `screens/cockpit/`, with the same state names under `/__states`.
  Two more are built (#308): which machines applied the latest rules revision on
  Settings › Permission rules (`a-revision`, `a-revision-zh`: applied, behind, offline; `/__states/settings/rules-revision`), and the quiet notes in a
  Codex session whose rules are prompts-only or whose hook did not fire (`conv-codex-prompts`,
  `conv-codex-nohook`, and their `-zh` states; the same names in the cockpit `/__states`).
- `hooks.html` (#97): Settings › Hooks, three directions; A is chosen and built in `screens/settings/` with the same state names under `/__states/settings/hooks*`, B and C stay as alternatives. A (`a-*`) is one page: the webhooks as rows, a row opens in place, adding and editing happen in a card above the list, and the machines' scripts are a read-only group below. B (`b-*`) is a list with the selected hook beside it, on tabs for Settings and Deliveries; the scripts sit in the same list with a lock. C (`c-*`) is a tab per kind (Webhooks, Scripts, Deliveries) with one delivery log for every hook, and editing in a dialog. States: A `a-list`, `a-deliveries`, `a-needs-secret` (a hook whose
  secret was lost), `a-add`, `a-edit`, `a-secret` (the one-time secret after create), `a-rotate-confirm`,
  `a-rotate-secret`, `a-test`, `a-test-bad`, `a-script-run`, `a-empty`; B `b-settings`, `b-deliveries`, `b-script`,
  `b-add`, `b-secret`, `b-empty`; C `c-webhooks`, `c-edit`, `c-secret`, `c-rotate`, `c-scripts`, `c-deliveries`,
  `c-empty`. Add `?lang=zh-Hans` for Simplified Chinese. New icons: `webhook`, `play`, `copy`, `ellipsis`. The secret
  shown is a placeholder, never a real one.
- `audit.html` (#80): Settings › Audit and the session inspector's Audit tab, three directions; A is chosen and built
  as Settings › Audit (the `audit-*` states in `/__states/settings`) and as the inspector's Audit tab (the
  `a-inspector`, `a-inspector-detail` and `a-inspector-empty` states in `/__states/workspace`), B and C stay as
  explored. A (`a-*`)
  is dense rows grouped by day with a filter bar of five selects and a search box; B (`b-*`) is a facet column with
  counts beside a two-line feed; C (`c-*`) is search first, with `key:value` tokens and a monospace log. They share one
  record grammar (time, type, summary, actor) and one detail. States: A `a-page`, `a-filtered`, `a-detail` (a
  scrubbed command), `a-verify-ok`, `a-verify-broken` (hash-mismatch), `a-export` (JSON Lines or CSV), `a-empty`,
  `a-inspector`, `a-inspector-empty`; B `b-page`, `b-detail`, `b-verify`, `b-export`, `b-empty`, `b-inspector`; C
  `c-page`, `c-detail`, `c-verify`, `c-export`, `c-empty`, `c-inspector`. Retention is shown read-only: it is set on
  the host. Add `?lang=zh-Hans` for Simplified Chinese. New icons: `list-checks`, `download`.
- `screens/tokens.html` (#71): Settings › API tokens, three directions; A is chosen and built in `screens/settings/` under `/__states/settings/tokens-*`. A (`a-*`) is one page: rows
  that open in place, the issue form in a card above the list, revoked and expired tokens in a group below; B (`b-*`)
  is a table with the form and the one-time reveal in dialogs; C (`c-*`) is two tabs (Active, Revoked and expired)
  with issuing on its own page beside a "This token can / cannot" summary. States: A `a-list`, `a-detail`,
  `a-issue`, `a-issue-answer` (the separate answer switch and its warning), `a-reveal`, `a-revoke`, `a-error`
  (100 tokens active), `a-empty`; B `b-table`, `b-issue`, `b-reveal`, `b-revoke`, `b-error` (the list failed to
  load), `b-empty`; C `c-active`, `c-inactive`, `c-issue`, `c-issue-answer`, `c-reveal`, `c-revoke`, `c-error` (the
  audit log could not record the token), `c-empty`; each direction also has `-zh` states. Add `?lang=zh-Hans` for
  Simplified Chinese. New icons: `key-round`. The token shown is a placeholder, never a real one.
- `screens/session-mcp.html` (#67): Settings › Other sessions and the per-session switches of ADR 0034, three
  directions. The maintainer chose a combination: the session's switches as A's inspector tab, Settings as C's
  table, and the hand-off request and the provenance label as A's card in the turn and line in the meta row; the
  rest stays as the alternatives considered. Each shows the
  per-session switches (send, start, hand-off; reading always on, answering shown as never), Reach (project,
  machine, all), the project and global defaults, a machine with `session_mcp` off, the hand-off request the user
  confirms, and the label on a prompt from another session. A (`a-*`) puts the switches in an inspector tab and
  has one Settings page (global default, project defaults, a list of machines); the hand-off request is a card in
  the turn and the provenance label a line in the prompt's meta row. B (`b-*`) puts a chip in the top bar that
  opens a popover; Settings has a tab per machine, so a machine with `session_mcp` off is a tab that explains
  itself; the hand-off request is a banner above the composer and the provenance label a pill above the prompt.
  C (`c-*`) opens a dialog from the session's actions menu with presets (Read only, Can direct, Can start and
  move) and Custom; Settings is a table with the global row on top and greyed rows on machines that have it off;
  the hand-off card offers a second action, Approve and always allow studio (a proposal); the provenance label is
  a quoted block. States: A `a-session`, `a-session-on`, `a-session-wide`, `a-session-off`, `a-settings`,
  `a-settings-edit`, `a-handoff`, `a-handoff-after`, `a-prov`; B `b-session`, `b-session-on`, `b-session-wide`,
  `b-session-off`, `b-settings`, `b-settings-machine`, `b-settings-off`, `b-handoff`, `b-handoff-after`, `b-prov`;
  C `c-session`, `c-session-adv`, `c-session-off`, `c-settings`, `c-settings-menu`, `c-settings-empty`,
  `c-handoff`, `c-handoff-after`, `c-prov`; `conv-refusals` shows `forbidden`, `loop-limit` and `rate-limited`
  as the sending session sees them. Most states also have `-zh` states in Simplified Chinese. Strings that are not
  in `web/src/i18n/zh-Hans.json` yet are proposals for it. Automatic approval of hand-offs (the switch and its list
  of machines, up to 3 an hour, each still waiting for the end of the turn) is kept; it needs a field of
  `SessionMcpSettings` in `shared/`, added with ADR 0034 step 5. New icons: `send`, `square-plus`, `arrow-right-left`, `eye`, `link`,
  `corner-down-right`, `external-link`, `webhook`.
- `screens/self-config.html` (#69): a session's change request for its own configuration (ADR 0036), three
  directions; the maintainer chose B, and A and C stay as the alternatives considered. Each shows the request in the turn (CLAUDE.md, a
  skill, a command and the effort setting, a diff per object, Approve and Decline for the whole request, "takes
  effect when the session next starts", the content-check warning), the state after approval with Undo and after
  Undo, a request that expired at a hand-off, where `read-config` and `write-config` are turned on, and the
  notification of a pending request. A (`a-*`) opens the diffs inside the card and puts the switches in the
  inspector tab, retitled Hoplet tools. B (`b-*`) keeps the card compact and opens a wide inspector with an object
  list and the diff; the switches are a chip and a popover. C (`c-*`) offers one action, Review and decide, and a
  dialog with steps that offers Approve on the last step; the switches are presets in a dialog. States: A
  `a-card`, `a-card-open`, `a-card-warn`, `a-applied`, `a-undone`, `a-expired`, `a-session`, `a-session-write`,
  `a-notify`; B `b-card`, `b-review`, `b-review-warn`, `b-applied`, `b-undone`, `b-expired`, `b-session`,
  `b-session-write`, `b-notify`; C `c-card`, `c-review`, `c-review-warn`, `c-review-last`, `c-applied`,
  `c-undone`, `c-expired`, `c-session`, `c-session-write`, `c-notify`; `conv-refused` (`stale`, `unsafe-content`,
  `unsupported`) and `conv-undo-stale` show what the session or the user sees when a request or an Undo fails.
  Most states also have `-zh` states. Strings that are not in `web/src/i18n/zh-Hans.json` yet are proposals for
  it. New icons: `undo-2`, `zap`, `sliders-horizontal`, `book-open`, `triangle-alert`.
- `screens/child-sessions.html` (#81): the UI of child sessions (ADR 0035, decision 6), three directions awaiting
  the maintainer's choice. Shared by all three: the session list as a tree (a parent with a count and the
  strongest state among its children, indented children with machine and worktree branch, a grandchild, a folded
  parent that keeps its attention dot, "Started by a deleted session") and the stop dialog with "Also stop N
  running children", checked by default. Every cancel says what stops and what stays; worktrees always stay. A
  child waiting for a permission shows in the list, in the parent and where the directions place it. A (`a-*`) puts
  the children in a Children tab of the inspector: one row each with state, elapsed time and tokens, a done child
  opened in place to its summary and changed files, cancel in a dialog. B (`b-*`) replaces the "Started" lines of
  the parent's turn with a card, one row per child, opened and cancelled in place, with the permission request as
  a strip under its row. C (`c-*`) is a split view: a strip of children above the parent and the selected child,
  read only, answered in its pane, cancel in a popover. States: shared `list-open`, `list-folded`, `stop-parent`;
  A `a-panel`, `a-open`, `a-blocked`, `a-cancel`, `a-subtree`; B `b-card`, `b-open`, `b-blocked`, `b-cancel`,
  `b-subtree`; C `c-split`, `c-blocked`, `c-cancel`, `c-subtree`. `list-open`, `stop-parent` and the main states of each direction
  also have `-zh` states in Simplified Chinese; strings not in `web/src/i18n/zh-Hans.json` yet are proposals for it. The token cap per tree is not
  shown because its value is not decided. New icons: `git-fork`, `git-branch`, `unlink`.
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
