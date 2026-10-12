# Design

## Viewing

Open a mockup in a browser (`design/screens/<screen>.html`, or `<screen>.direction-<x>.html` while directions are
compared): it shows every state stacked, in light and dark; `?state=<id>&scheme=light|dark` shows one window at
real size. `design/tokens.html` shows the design tokens, which `design/check-tokens.py` keeps in step with
`web/src/design/tokens.css`; `design/mark.html` is the app icon and the web UI's mark.

## Screens

| Screen | Spec | Mockup | Code | State |
|---|---|---|---|---|
| cockpit | `docs/spec.md#hoplet` | `screens/cockpit.html` | `web/src/screens/cockpit/` | built |
| workspace | `docs/spec.md#workspace-inspector` | `screens/workspace.html` | `web/src/screens/workspace/` | built |
| settings | `docs/spec.md#settings` | `screens/settings.html` | | designed |

## How a screen is made

1. The screen and its states are written in `docs/spec.md`.
2. A new screen or a redesign gets two or three directions as mockups; a small change edits the mockup in place.
   The maintainer chooses one in a pull request that contains only the mockup.
3. The screen is built in its code directory, with every state renderable at `/__states/<screen>/<state>` and
   covered by `pnpm e2e` with axe in both schemes (`docs/testing.md`).
4. The maintainer checks it in the running product before the pull request merges. No captures are kept.

Mockups, code and spec change together: when one changes, the others follow in the same pull request.
