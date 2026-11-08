import CryptoKit
import Foundation

/// Why a Noise handshake or channel failed.
public enum NoiseError: Swift.Error, Equatable, Sendable {
  case handshake
  /// The host's authenticated answer that this device is not paired or was revoked.
  case deviceRefused
  /// The pairing code is no longer valid; unauthenticated.
  case pairingRefused
  case decrypt
  case frame
  case tooLarge
}

/// A handshake's kind; its raw value is the byte before the first message.
public enum NoiseKind: UInt8, Sendable {
  case daily = 1
  case pair = 2

  var protocolName: String {
    switch self {
    case .daily: return "Noise_IK_25519_ChaChaPoly_SHA256"
    case .pair: return "Noise_IKpsk1_25519_ChaChaPoly_SHA256"
    }
  }
}

let noiseKeySize = 32
let noiseTagSize = 16
/// The refusal byte: a daily response's payload, or a pairing response by itself.
let noiseRefusal: UInt8 = 1

struct CipherState: Sendable {
  private var key: SymmetricKey?
  private var counter: UInt64 = 0

  init(key: SymmetricKey? = nil) { self.key = key }

  private func nonce() throws -> ChaChaPoly.Nonce {
    var bytes = Data(count: 4)
    withUnsafeBytes(of: counter.littleEndian) { bytes.append(contentsOf: $0) }
    return try ChaChaPoly.Nonce(data: bytes)
  }

  mutating func encrypt(_ plain: Data, ad: Data = Data()) throws -> Data {
    guard let key else { return plain }
    let box = try ChaChaPoly.seal(plain, using: key, nonce: nonce(), authenticating: ad)
    counter += 1
    return box.ciphertext + box.tag
  }

  mutating func decrypt(_ sealed: Data, ad: Data = Data()) throws -> Data {
    guard let key else { return sealed }
    guard sealed.count >= noiseTagSize else { throw NoiseError.decrypt }
    let split = sealed.endIndex - noiseTagSize
    let box = try ChaChaPoly.SealedBox(
      nonce: nonce(), ciphertext: sealed[sealed.startIndex..<split], tag: sealed[split...])
    let plain = try ChaChaPoly.open(box, using: key, authenticating: ad)
    counter += 1
    return plain
  }
}

private func hmac(_ key: Data, _ parts: Data...) -> Data {
  var mac = HMAC<SHA256>(key: SymmetricKey(data: key))
  for part in parts { mac.update(data: part) }
  return Data(mac.finalize())
}

/// The Noise HKDF: up to three 32-byte outputs.
private func hkdf(_ chainingKey: Data, _ input: Data) -> (Data, Data, Data) {
  let temp = hmac(chainingKey, input)
  let one = hmac(temp, Data([1]))
  let two = hmac(temp, one, Data([2]))
  let three = hmac(temp, two, Data([3]))
  return (one, two, three)
}

private func sha256(_ data: Data...) -> Data {
  var hash = SHA256()
  for part in data { hash.update(data: part) }
  return Data(hash.finalize())
}

private struct SymmetricState {
  var chainingKey: Data
  var hash: Data
  var cipher = CipherState()

  init(name: String) {
    // A name that fits the hash is used directly, zero-padded.
    let raw = Data(name.utf8)
    hash = raw.count <= noiseKeySize ? raw + Data(count: noiseKeySize - raw.count) : sha256(raw)
    chainingKey = hash
  }

  mutating func mixHash(_ data: Data) { hash = sha256(hash, data) }

  mutating func mixKey(_ input: Data) {
    let (ck, key, _) = hkdf(chainingKey, input)
    chainingKey = ck
    cipher = CipherState(key: SymmetricKey(data: key))
  }

  mutating func mixKeyAndHash(_ input: Data) {
    let (ck, temp, key) = hkdf(chainingKey, input)
    chainingKey = ck
    mixHash(temp)
    cipher = CipherState(key: SymmetricKey(data: key))
  }

  mutating func encryptAndHash(_ plain: Data) throws -> Data {
    let sealed = try cipher.encrypt(plain, ad: hash)
    mixHash(sealed)
    return sealed
  }

  mutating func decryptAndHash(_ sealed: Data) throws -> Data {
    let plain = try cipher.decrypt(sealed, ad: hash)
    mixHash(sealed)
    return plain
  }

  func split() -> (CipherState, CipherState) {
    let (one, two, _) = hkdf(chainingKey, Data())
    return (
      CipherState(key: SymmetricKey(data: one)), CipherState(key: SymmetricKey(data: two))
    )
  }
}

typealias X25519Key = Curve25519.KeyAgreement.PrivateKey

private func dh(_ key: X25519Key, _ peer: Data) throws -> Data {
  let shared = try key.sharedSecretFromKeyAgreement(
    with: Curve25519.KeyAgreement.PublicKey(rawRepresentation: peer))
  return shared.withUnsafeBytes { Data($0) }
}

/// The initiator's side of a daily or pairing handshake.
struct NoiseHandshake {
  private var state: SymmetricState
  private let kind: NoiseKind
  private let identity: X25519Key
  private let ephemeral: X25519Key
  private let peerStatic: Data
  private let psk: Data?
  private let payload: Data

  /// - Parameter ephemeral: only tests fix this.
  init(
    kind: NoiseKind, identity: X25519Key, peerStatic: Data, psk: Data? = nil,
    payload: Data = Data(), ephemeral: X25519Key = X25519Key()
  ) throws {
    guard peerStatic.count == noiseKeySize, kind == .pair ? psk?.count == noiseKeySize : psk == nil
    else {
      throw NoiseError.handshake
    }
    self.kind = kind
    self.identity = identity
    self.ephemeral = ephemeral
    self.peerStatic = peerStatic
    self.psk = psk
    self.payload = payload
    state = SymmetricState(name: kind.protocolName)
    state.mixHash(Data("hoplet/1".utf8) + Data([kind.rawValue]))
    state.mixHash(peerStatic)
  }

  /// The first message, led by the kind byte: e, es, s, ss, and psk when pairing.
  mutating func writeFirst() throws -> Data {
    do {
      let e = ephemeral.publicKey.rawRepresentation
      state.mixHash(e)
      if psk != nil { state.mixKey(e) }
      state.mixKey(try dh(ephemeral, peerStatic))
      let s = try state.encryptAndHash(identity.publicKey.rawRepresentation)
      state.mixKey(try dh(identity, peerStatic))
      if let psk { state.mixKeyAndHash(psk) }
      let body = try state.encryptAndHash(payload)
      return Data([kind.rawValue]) + e + s + body
    } catch {
      throw NoiseError.handshake
    }
  }

  /// Reads the response (e, ee, se) and returns the send and receive ciphers.
  mutating func readResponse(_ message: Data) throws -> (send: CipherState, recv: CipherState) {
    if kind == .pair, message == Data([noiseRefusal]) { throw NoiseError.pairingRefused }
    guard message.count >= noiseKeySize + noiseTagSize else { throw NoiseError.handshake }
    let body: Data
    do {
      let split = message.startIndex + noiseKeySize
      let re = Data(message[..<split])
      state.mixHash(re)
      if psk != nil { state.mixKey(re) }
      state.mixKey(try dh(ephemeral, re))
      state.mixKey(try dh(identity, re))
      body = try state.decryptAndHash(Data(message[split...]))
    } catch {
      throw NoiseError.handshake
    }
    if kind == .daily, !body.isEmpty {
      throw body == Data([noiseRefusal]) ? NoiseError.deviceRefused : NoiseError.handshake
    }
    return state.split()
  }
}
