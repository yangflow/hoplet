// Package shim runs one session's harness process and outlives the daemon.
//
// The shim owns the child's stdio. Every line the child writes, every line
// written to it and its exit are appended to a spool file, so a daemon that
// restarts reconnects over the shim's Unix socket and resumes from the
// byte offset it last committed: nothing is lost and nothing is repeated,
// and the child never notices the daemon went away.
//
// Wire format: each frame is a 4-byte big-endian length followed by a JSON
// message. The client opens with Hello carrying ProtocolVersion; a shim
// speaking another version answers with an error message and closes.
package shim
