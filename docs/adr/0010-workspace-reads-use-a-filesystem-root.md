# 0010 Workspace reads use a filesystem root

- Status: accepted
- Date: 2026-10-08
- Related: docs/spec.md#workspace, docs/threat-model.md#threats-and-mitigations

## Context

The web UI lists and opens files of the owner's working tree. The requested path comes from the network, and
the tree can contain symbolic links that are replaced while a read is in progress. Resolving a path and then
opening it separately leaves a window in which a link can point outside the tree.

## Decision

The agent opens the session's working tree with Go `os.Root` and resolves and opens every requested path
through it. Relative symbolic links that stay inside the root are followed; links that leave it and absolute
links are rejected. Only regular files are returned. A read returns at most the first 256 KiB of the file,
taken from the same file descriptor that supplied its metadata, with `truncated` set when more bytes exist. A
file with a NUL byte in that prefix is answered as binary with its size only; an image of at most 4 MiB is
answered whole. Paths inside `.git` are refused.

## Cost

Absolute symbolic links inside a project cannot be read through Hoplet. Hard links and bind mounts are not
confined by `os.Root`.

## Revisit triggers

Go changes `os.Root` semantics, or the agent needs to run on a platform without rooted opens.
