// Package noise is Hoplet's encrypted channel between hosts and devices.
//
// Each Hoplet home holds one long-term X25519 identity key in keys/identity
// (hex, mode 0600); a host or device is named by the HostID of its public
// key. A channel runs over any connection that carries whole messages
// (MsgConn) and opens with one of three Noise handshakes, all on the suite
// 25519_ChaChaPoly_SHA256:
//
//   - KindDaily, Noise IK: a paired device reaches a host whose key it knows.
//   - KindPair, Noise IKpsk1: a device pairs with a host using the one-time
//     secret from a pairing code.
//   - KindRelay, Noise XN: a host proves its key to a relay that has none.
//     The host's handshake ends with its own last message, so a relay that
//     refuses it shows only as the connection closing.
//
// The initiator's first message is prefixed with the kind byte, and the
// prologue is "hoplet/1" followed by the same byte, so the protocol version
// and the kind are both bound into the handshake.
//
// After the handshake every message is one Noise transport message whose
// plaintext is a flag byte and a piece of a frame: flagLast ends a frame,
// flagMore continues it, flagPing and flagPong carry an 8-byte nonce for
// end-to-end liveness. Noise's implicit nonces make a replayed, reordered,
// dropped or altered message fail to decrypt, and any failure ends the
// channel.
package noise

//go:generate go test -run ^TestVectors$ . -args -update
