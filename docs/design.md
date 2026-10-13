# Hoplet design language

## Principles

- **The turn is the unit.** A session is a sequence of turns: a prompt, the work it caused and the answer.
  Consequence: the conversation is built from turn blocks, and a finished turn summarizes itself (files,
  tools, duration) so a reader scans turns, not lines.
- **State lives next to what it describes.** Consequence: turn states sit on the live turn's header, session
  states under the composer, a session's state as a glyph in place of its time on the sidebar row, a device's presence on
  its heading, this window's connection at the sidebar foot, handoff progress in the inspector. There is no global
  status strip or status bar (docs/spec.md#hoplet).
- **Needing the user is the loudest signal.** Consequence: a permission request is the only amber on screen,
  with a ring, in the turn and on the session's sidebar row alike; nothing decorative competes with it.
- **One accent means live.** Consequence: orange marks the running turn, its edge and the primary action
  (Send, Allow, Hand off) and nothing else.
- **The shell maps to native.** Sidebar, content, inspector: the macOS three-pane layout. Consequence: the web
  UI invents no frame a native client could not adopt.
- **Prose reads, output scans.** Consequence: assistant text is comfortable sans at a bounded measure;
  anything from a terminal is monospace in aligned columns.
- **Say it once, briefly.** Consequence: no label repeats what a glyph, colour or position already says;
  idle shows nothing; actions whose icon carries the intent are icon buttons with a tooltip and an
  accessible name, and only decisions (Allow, Deny, the handoff confirmation) stay worded.
- **Honest state.** Consequence: the runtime state is always shown and named to assistive technology, a
  pane that cannot be read says why, and elided content says how large it is.

## Core symbol

The h: a lowercase h whose arch drops onto a dot, the hop of a session to another machine. The app icon
(`web/public/icon.svg`, drawn in `design/mark.html`) renders it as an extruded cream h and a glowing orb on an amber
to ember gradient field lit from the top left, on the macOS icon grid. In the web UI the same h is a stroke glyph in
the on-accent colour on the accent square of the sidebar brand, and alone in the top bar. States: static; none
animated.

## Color

Tokens live in `web/src/design/tokens.css`, the only place raw values are allowed in product code; the full
set, shadows included, is the JSON block of `design/screens/cockpit.html`. Palette: warm near-black and warm
paper, one hoplet orange. Dark is authored first; light is derived. Contrast follows WCAG AA for text.

| Token | Dark | Light | Use |
|---|---|---|---|
| `--color-bg` | `#131210` | `#f8f5f0` | canvas, finished turns |
| `--color-sidebar` | `#191715` | `#efebe4` | sidebar |
| `--color-surface` | `#1c1a17` | `#f0ebe4` | code panels, inspector |
| `--color-raised` | `#211e1b` | `#fffdfa` | live turn, composer, popovers |
| `--color-hover` | `#28241f` | `#e8e2d9` | row hover |
| `--color-selected` | `#2c2823` | `#e2dbd1` | selected session |
| `--color-line` | `rgba(255, 238, 220, 0.075)` | `rgba(60, 40, 20, 0.10)` | hairlines |
| `--color-text` | `#efe9e2` | `#1c1915` | primary text |
| `--color-text-2` | `#b8b0a6` | `#534c44` | secondary text |
| `--color-text-3` | `#988f85` | `#675f55` | meta, placeholders |
| `--color-accent` | `#ff7a1a` | `#c24e00` | live, primary action |
| `--color-accent-text` | `#ff9142` | `#a64100` | accent as text |
| `--color-on-accent` | `#1c0d02` | `#ffffff` | text on accent fills |
| `--color-attention` | `#e8b31a` | `#c08a00` | needs the user: permission requests |
| `--color-attention-text` | `#f0c142` | `#8f6200` | attention as text |
| `--color-success` | `#4cc38a` | `#1d8550` | online presence, done |
| `--color-danger` | `#ff7366` | `#c3352a` | failed, disconnected |
| `--color-add-text` | `#86dcae` | `#156b3d` | added diff lines |
| `--color-del-text` | `#ffa49b` | `#a82a1f` | deleted diff lines |
| `--color-git-modified` | `#d9b77e` | `#86621c` | workspace tree: modified |
| `--color-git-untracked` | `#6fcf97` | `#1a7a47` | workspace tree: untracked, added |
| `--color-git-deleted` | `#ff8a7d` | `#b42f24` | workspace tree: deleted |
| `--color-syntax-keyword` | `#d3a6ee` | `#8433b5` | code: keywords |
| `--color-syntax-string` | `#b0d58c` | `#3f7514` | code: strings |
| `--color-syntax-comment` | `#8f877d` | `#736b61` | code: comments |
| `--color-syntax-number` | `#efb57c` | `#9c4f00` | code: numbers |
| `--color-syntax-type` | `#7ec3e3` | `#17698f` | code: types |
| `--color-syntax-function` | `#e8d7a4` | `#6b5000` | code: functions |

Each hue has one meaning: orange live, amber needs you, red failed or deleted, green online or added. Git
colours appear only in the workspace tree, as in VS Code: a letter (M, A, D, U) and a tinted name; a folder
holding changes carries a dot.

## Typography

Two families, bundled with the web UI and served by the core, never fetched from elsewhere: Inter (`--font-ui`)
for the interface and, through `--font-prose`, for assistant prose; JetBrains Mono (`--font-mono`) for machine
content. Chinese text falls back to PingFang SC in both, and the system fonts follow when a face is missing.
`--font-prose` follows `--font-ui` and is its own token so that prose can change alone. Four sizes, three
weights, every line height a multiple of 4 px: `--text-meta` 11/16 for meta, keycaps and line numbers;
`--text-ui` 13/20 for the sidebar, buttons and tool rows; `--text-body` 15/24 for assistant prose within
`--measure-prose` 68ch; `--text-title` 15/20 at weight 600 for turn prompts and page titles; `--text-code`
12.5/20 mono for paths, commands, code and terminal output.
Weights: 400, 500, 600.
Every number that changes (times, sizes, counts, timers, line numbers) uses tabular numerals.

## Layout and breakpoints

Spacing scale `--space-1…7` (4, 8, 12, 16, 24, 32, 48 px); radii `--radius-sm` 6 px rows, `--radius-md`
10 px panels, `--radius-lg` 14 px turn blocks, `--radius-xl` 18 px composer. Declared viewport: desktop, at
least 1024 px wide (docs/spec.md#information-architecture); no other viewport is designed or built
([ADR 0011](adr/0011-the-web-ui-is-a-desktop-react-application.md)). Panes: sidebar `--layout-sidebar`
248 px, which folds away, with device rows of 32 px and 16 px above each device (the presence dot, the name and a chevron that folds the
device's directories; hovering or focusing the row shows a right-aligned Add project plus), directory rows of 28 px
that fold (no chevron and no indent: the row starts at the machine row's left edge, its folder glyph on the machine's
glyph column and its name on the machine's name; the whole row is the button, the folder glyph is open while the
directory is; the choice is remembered; a folded directory shows its session count and its most urgent state glyph)
and session rows of 28 px under it, the provider mark on the folder's glyph column and the title aligned with the
directory name, all on the 4 px grid the other panes use. A machine row also shows, on hover or focus, an
ellipsis button left of the plus that opens that machine's view options, a popover under the row titled with the
machine's name and holding two radio groups, Organize (By folder, Flat) and Sort (Recent activity, Created, Title),
a check marking each choice; Flat rows are 36 px with the directory name in meta type on a second line, and the
machine's directories are not shown. The Add project folder frame draws its own 1 px border and a 2 px accent focus
ring outside it (offset 2 px); its header row is 32 px with the segment plates 4 px inside every edge, and only the
list below clips, to the frame's radius. Conversation flex with a `--layout-column` 760 px column under a
`--layout-topbar` 52 px top bar holding only the title and the inspector toggle; inspector
`--layout-inspector` 296 px, opened from the top bar, widening to `--layout-inspector-wide` 720 px while a
diff or a file is open, split into the viewer and a `--layout-inspector-list` 220 px list or tree on its right, at the window's edge. Rows of the Files tree and the Changes list are `--layout-row-tree` 24 px, a tree level indents 12 px. Below
1408 px of window width the sidebar folds while the inspector is wide, so the conversation keeps at least
440 px. The inspector's tab bar holds its tools as closable tabs, Changes and Files to start, and a + button whose tools menu reopens a closed tab and takes Terminal and Browser (#14, #15) when they are built. Settings replaces the conversation and the inspector with a page whose
`--layout-settings-nav` 208 px section list grows with later sections.

The conversation is a column of turn blocks. A finished turn sits flat on the canvas with a hairline above
it; its header (prompt, time, summary; the line range in a tooltip) pins while the reader scrolls through it. The live turn
is the only raised block, with an accent edge, or attention when it waits for permission; a permission
request opens inside it. Every tool call is one 28 px row in four columns: icon, past-tense verb (Read, Edited, Wrote, Ran, Searched,
Listed, Fetched, Called, Thought, Viewed), object, and meta right-aligned in tabular meta type (`L1–120 ·
6.1 KiB`, `exit 1 · 42 s`, `12 matches`); a running row shows a spinner and a live timer there, a failed row
keeps its place with a danger rule at its left. Every row takes the hover background (`--color-hover`, `--radius-sm`)
on a plate that reaches 8 px past the row's content on each side. Verb and object are one step dimmer (`--color-text-2`)
than expanded output (`--color-text`), so the result stands out; the verb column is 64 px and grows to a longer verb
such as the folded row's "3 次工具调用". Expanded output always sits in one output well (mono 12.5/20,
optional line numbers, a fade past `--layout-well-max`, a copy button): Read previews, command output
under its `$ command` line, search hits as `path:line  excerpt`, web and MCP results, errors and the elided
block's preview with an open button showing its size. The diff shares the well's frame and gutter, and its
path opens the file's full diff in the inspector. Three or more consecutive rows without an open well fold
into one row naming their verbs; expanded thinking is dim italic UI type. The composer floats at the foot of the column: a
first row ruled off from the field that says where the session runs (machine / directory / git branch as quiet
chips, pickers with a chevron only in a draft), the field, and a toolbar with attach, a model button (the
model's name and the Thinking glyph, opening model, Thinking and Fast), voice and send, and a status line only for
session states that block or change what the user can do. A grip on its top edge sets its height; pasted
images sit above the text as 64 px thumbnails on the checkerboard. Workspace paths carry a file-type glyph; code in diffs and files is highlighted with the
syntax tokens; images are previewed on a checkerboard. The development fixtures are built for the states (one turn with a prompt, thinking, prose, a read, an
edit and a run of tools, then the live turn), not copied from a session.

## Components

Shared components live in `web/src/components/` and screens compose them; a screen never styles its own
variant of a shared component. Set: `TokenGate`, `Button`, `IconButton` (icon only, its accessible name is its
tooltip), `TextField`, `Row`, `Fold`, `Badge` (state labels and keycaps), `Panel`, `Popover`, `Select`, `Switch`,
`Dialog` (a centred modal over a scrim with a focus trap: the command palette, Add project). Icons are
`components/icons.tsx`; provider marks are `components/provider-marks.tsx`. Screen-owned pieces
(sidebar rows, turn blocks, activity rows, composer, inspector, handoff steps)
live in the Hoplet screen directory.

## Icons

One family, Lucide (ISC), bundled and never fetched: every glyph on a 24-unit grid at `--icon-size` 16 px with
`--icon-stroke` 1.5, round caps and joins, centred on the line of the text beside it. The set is closed:
search, panel-left, panel-right, square-pen, plus, chevron-right, arrow-up, arrow-up-right,
arrow-right-left, file, folder, folder-open, book-open, pencil, file-plus, terminal, plug, globe, lightbulb, image,
layers, eye, square, git-branch, zap, lock, paperclip, mic, hand, clock, x, check, circle-alert, info, rotate-cw, copy,
settings, laptop, contrast, ellipsis, text-wrap, loader-circle; turned glyphs (a chevron, an arrow) cover the other directions.
File-type badges (TypeScript, Markdown, JSON, image, text, other) use the same page and stroke with a tint.
Provider marks (the Claude mark for Claude Code) are drawn monochrome in the dim text colour, never in brand
colour, and lead each session row. Icons sit in labels, tool rows and empty states and do not decorate. The
app mark remains `web/public/icon.svg`.

## Motion

Only live things move: the live turn's state dot pulses, the caret of streamed text blinks, a running tool
shows a spinner. New rows and streamed text appear without animation; the inspector and sidebar change width
only; the jump pill rises with a shadow. When this window loses the core, every live motion stops and greys.
`prefers-reduced-motion` turns all animation off. Durations are tokens.

## Signature moments

The live turn: the turn block rises with an orange edge, its header reads `Running` with a pulsing dot and a
timer, tools tick in their columns and text streams with a caret; when the turn is stored, the block settles
flat onto the canvas and its header shows the turn summary, without a jump. A permission request turns the
edge amber and opens inside the turn, and the session's sidebar row turns amber with it.

## Copy tone

Terse, imperative, no exclamation marks. Labels are as short as their meaning allows ("MacBook Pro offline", "Read
only"); a disabled input says why in a few words ("Turn running", "Waiting for permission"); errors say what
happened and what to do. English is the source; `zh-Hans` is the first translation (`web/src/i18n/`).

## Accessibility

Semantic elements first (`button` for actions, `details`/`summary` for folds); every interactive element has
an accessible name and `data-testid="<screen>.<element>"`; keyboard order follows reading order; colour is
never the only carrier of meaning: state glyphs differ in shape and carry their state as an accessible name
and tooltip, git status has letters, and icon buttons have accessible names. Runtime state changes and
permission requests are announced through a polite live region. The end-to-end suite runs axe on every state
of every screen, including color contrast.

## Native shell

None in this repository. A native client, when there is one, maps its sidebar, content and inspector to this
layout ([ADR 0011](adr/0011-the-web-ui-is-a-desktop-react-application.md)).

## Design acceptance checklist

- [ ] Every state of the touched screen renders at `/__states/<screen>/<state>` in a development build
- [ ] Raw values appear only in `web/src/design/tokens.css`
- [ ] New user-visible copy exists in `en.json` (source) and `zh-Hans.json`
- [ ] Interactive elements carry `data-testid` and work by keyboard
- [ ] Light and dark both correct
