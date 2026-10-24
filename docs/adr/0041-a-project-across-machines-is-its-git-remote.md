# 0041 A project across machines is its git remote

- Status: accepted
- Date: 2026-10-24
- Related: ADR 0002, ADR 0009, ADR 0012, docs/api.md, docs/spec.md, design/screens/session-organize.html,
  issue #91

The maintainer chose direction A of `design/screens/session-organize.html` with its recommended options on
2026-10-24, including grouping the sidebar by project across machines.

## Context

The sidebar lists sessions by machine, then by folder. The same repository is usually cloned on several machines,
at different paths (`/Users/mark/github/hoplet` on one, `~/src/hoplet` on another), and a handoff moves a session
into a worktree next to the target's clone (ADR 0009), so its folder changes on every move. Grouping by project
needs a name for the project that is the same on every machine and survives a handoff. The folder's name is not:
two unrelated folders can share it, and a worktree's name carries a suffix. The agent already reports the `origin`
remote of each watched project in its inventory.

## Decision

**A session's project across machines is the normalized `origin` remote of its repository.** The core derives it
when it lists a session and sends it as `repository`; nothing is stored and no wire message changes.

- *Which repository.* The deepest watched project of the owning machine (its last inventory's `repositories`)
  whose path is the session's project path or holds it. When none has a remote, the clone recorded in
  `session_worktrees` for a worktree Hoplet made (a handoff or a new session in a worktree) is looked up the same
  way.
- *Normalization.* The scheme, the user and the port are removed and the host lowercased; the scp form
  (`git@host:owner/repo`) becomes `host/owner/repo`; a trailing `.git` and slashes are removed. So
  `git@github.com:a/b.git`, `https://GitHub.com/a/b` and `ssh://git@github.com:22/a/b/` are all `github.com/a/b`.
  A `file:` URL or a local path keeps its path. Credentials in a URL never reach the key.
- *Unknown.* A session whose folder has no remote, or whose machine has not reported an inventory, has no
  `repository`. The web UI then groups it by machine and folder, as its own project, marked "no remote".
- *Where it is used.* Only the web UI's Project grouping; the key is a display grouping, never an identity for
  permissions, rules or handoff.

## Cost

- Two different repositories that share a remote (a fork pushed to the same URL, or a mirror) are one project.
- A clone whose `origin` points elsewhere (a fork) is a different project from its upstream; there is no way to
  join them.
- A session of an offline machine is grouped by the inventory that machine last sent, which can be stale.
- Listing sessions looks up each session's repository; the inventories are read once per list.

## Revisit triggers

- Users need to join or split projects by hand, or to group by a remote other than `origin`.
- A vendor whose sessions are not in a git repository becomes common enough that "no remote" groups dominate.
