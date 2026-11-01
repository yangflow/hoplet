// Package workspace finds the git repositories under a session's working
// directory, records what they contain when the session begins, and works
// out what the session changed since.
//
// Hoplet writes nothing under the user's repositories: no refs, objects,
// index, locks or other files, and no modification time changes. It never
// reads the user's index; of the user's objects it reads only the HEAD commit
// and its tree (ls-tree, a submodule's rev-parse HEAD), and the configuration,
// read-only. For each repository it keeps its
// own object directory and, per worktree, its own index (a stat cache). A
// snapshot hashes the worktree into them: every file tracked at HEAD plus
// every untracked file that is not ignored, as raw bytes. No clean or smudge
// filter runs and no eol, ident or encoding conversion happens, so a file's
// status never depends on how the user's git would normalize it. Content is
// stored with git fast-import, as packs, inside a bare repository of
// Hoplet's own. Every git invocation is built by one helper (see Git) that
// removes GIT_* variables, disables hooks, replaces every configured filter
// driver by an empty command, and turns off gc, maintenance, fsmonitor and
// index caches.
//
// Layouts that would need special care are refused with an error row, not
// handled: a .git that is a symlink, core.worktree, partial clones, and a
// repository whose top is the user's home directory or above it (unless it
// merely encloses the session, which is then not inside that project).
//
// Known gaps: on a filesystem that ignores case, a case-only rename that is
// not committed yet is not seen (as in git status). A file that is ignored,
// force-added and not committed yet is not part of the file set. A change of
// line endings alone that keeps a file's size and modification time can go
// unnoticed under text=auto. A file swapped for a symlink in a parent directory while a
// snapshot runs can be read through. Invalid UTF-8 in a patch is replaced by
// U+FFFD, as JSON encoding would do.
package workspace
