// Package proto defines the types exchanged between Hoplet's daemon, shims
// and clients. It is the single source of truth for the wire protocol;
// Swift types are generated from it.
//
// # Versioning
//
// Every event carries the version v of its payload. Adding an optional field
// does not change the version. Renaming or removing a field, changing a
// field's type or meaning, or adding a required field bumps v, and the old
// version gets a defaultRegistry.addLegacy upgrader that converts it to the
// current payload type. Decode therefore always returns the current type.
// Old decoders are never deleted.
package proto
