# Changelog

Each release's notes are made from the titles of the pull requests merged since the previous one (Conventional
Commits: `feat` under Added, `fix` under Fixed, `perf` under Performance; `GOVERNANCE.md`). Versions follow
Semantic Versioning.

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
