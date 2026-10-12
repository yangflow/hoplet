# 0009 Handoff moves a workspace snapshot into the target clone

- Status: accepted
- Date: 2026-10-08
- Related: ADR 0002, ADR 0008, docs/spec.md#handoff

## Context

Moving a session to another machine means moving three things: ownership of the session, the Claude Code
session file, and the working tree the session was editing. A working tree in the middle of a session has
uncommitted and untracked changes. The target machine usually has its own clone of the same repository, at a
different path. The source machine may be online or already shut down.

## Decision

A workspace snapshot is the base commit, the repository's `origin` URL, a binary diff of tracked changes
against the base commit and the untracked files that are not ignored. The commits between the base commit and
the remote are included as a git bundle when the remote does not contain the base commit.

Handoff installs the snapshot into the target's own clone of the same `origin`, in a new git worktree next to
that clone on a new branch `hoplet/<first 8 characters of the session ID>`. It installs the session file byte
for byte into the Claude Code store for that worktree's realpath; Claude Code resumes it there with its full
history and writes new lines with the new working directory. Ownership moves only after the target has
verified, before any Claude Code process starts in the worktree, that the installed session file matches the
stored stream (byte size and SHA-256) and that the worktree matches the snapshot.

With the source online, handoff first stops the source's Claude Code process, waits for the final append and
takes a fresh snapshot. With the source offline, handoff uses the last snapshot the source uploaded; the
source uploads one after every completed turn. When an offline source reconnects, its appends are rejected and
it stops controlling the session.

## Cost

Handoff needs `git` and a clone of the same `origin` on the target, or a path chosen by the operator.
Sidecar files that Claude Code keeps beside a session file are not carried. Staged
changes arrive unstaged. Without a fetched `origin` the target must already have the base commit. An
offline handoff loses changes made after the last completed turn. Snapshots are stored by the core and count
against disk (docs/hosting.md#data).

## Revisit triggers

Sessions regularly work outside git repositories, or snapshot sizes exceed the limits in
docs/hosting.md#resources.
