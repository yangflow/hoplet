// Package shim runs one session's harness process and outlives the daemon.
//
// The shim owns the child's stdio. Every line the child writes, every line
// written to it and its exit are appended to a spool file, so a daemon that
// restarts reconnects over the shim's Unix socket and resumes from the
// byte offset it last committed: nothing is lost, and nothing is repeated
// provided the daemon commits the offset in the same transaction as the
// events it produced from the records before it. The child never notices the
// daemon went away.
//
// Offsets are byte offsets into spool.jsonl. A record frame carries the
// offset just after its record; a hello may only name 0 or such an offset.
//
// # Wire format
//
// Each frame is a 4-byte big-endian length followed by a JSON message. The
// client opens with a hello carrying ProtocolVersion; the shim answers with
// a welcome, which means the shim is ready and records follow, or with an
// error frame (carrying a Code) and closes. A new client replaces the
// previous one only once its hello is accepted.
//
// # Running a shim
//
// The shim must survive its parent. The daemon starts it in its own session
// (setsid), with stdin on /dev/null, stdout and stderr on <dir>/shim.log, and
// a clean environment free of credentials and HOPLET_* internals; the child
// inherits Config.Env, so pass it only what the harness needs. The shim
// ignores SIGHUP, SIGINT and SIGPIPE (IgnoreParentSignals) and stops on
// SIGTERM. One shim owns a session directory at a time, enforced with a lock
// file.
//
// # Reserved for terminals
//
// A later extension adds PTY terminals. They will use new message types that
// carry a terminal id, and their bytes are not written to spool.jsonl. Frames
// whose payload's first byte is not '{' are reserved for binary frames.
// Start, pid and signal messages without a terminal id refer to the harness.
package shim
