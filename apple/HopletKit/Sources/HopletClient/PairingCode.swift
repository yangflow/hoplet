import CryptoKit
import Foundation

/// Returns the host ID of a public key: the first 16 bytes of its SHA-256 in
/// lowercase unpadded base32; nil for a key of the wrong size.
public func hostID(of publicKey: Data) -> String? {
  guard publicKey.count == noiseKeySize else { return nil }
  let alphabet = Array("abcdefghijklmnopqrstuvwxyz234567")
  var out = ""
  var buffer = 0
  var bits = 0
  for byte in SHA256.hash(data: publicKey).prefix(16) {
    buffer = (buffer << 8) | Int(byte)
    bits += 8
    while bits >= 5 {
      bits -= 5
      out.append(alphabet[(buffer >> bits) & 31])
    }
    buffer &= (1 << bits) - 1
  }
  if bits > 0 { out.append(alphabet[(buffer << (5 - bits)) & 31]) }
  return out
}

/// Decodes unpadded URL-safe base64, rejecting any other alphabet.
private func decodeBase64URL(_ text: String) -> Data? {
  guard !text.contains(where: { "+/=".contains($0) }), text.count % 4 != 1 else { return nil }
  let padding = String(repeating: "=", count: (4 - text.count % 4) % 4)
  let standard = text.replacing("-", with: "+").replacing("_", with: "/") + padding
  return Data(base64Encoded: standard)
}

/// A host's one-time invitation to pair.
public struct PairingCode: Equatable, Sendable {
  public static let prefix = "hoplet1:"
  private static let maxNameSize = 64

  public var id: String
  public var key: Data
  public var name: String
  public var routes: [String]
  public var psk: Data
  /// The host refuses the secret after this; the caller compares it.
  public var expires: Date

  private struct Wire: Decodable {
    var id: String?
    var key: String?
    var name: String?
    var routes: [String]?
    var psk: String?
    var exp: Int64?
  }

  /// Decodes a code and checks its parts; nil if it cannot be read.
  public init?(_ text: String) {
    let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
    guard trimmed.hasPrefix(Self.prefix),
      let raw = decodeBase64URL(String(trimmed.dropFirst(Self.prefix.count))),
      let wire = try? JSONDecoder().decode(Wire.self, from: raw),
      let key = wire.key.flatMap(decodeBase64URL),
      let id = hostID(of: key), id == wire.id,
      let psk = wire.psk.flatMap(decodeBase64URL), psk.count == noiseKeySize,
      let name = wire.name, !name.isEmpty, name.utf8.count <= Self.maxNameSize,
      let routes = wire.routes, !routes.isEmpty,
      let exp = wire.exp, exp != 0
    else { return nil }
    self.id = id
    self.key = key
    self.name = name
    self.routes = routes
    self.psk = psk
    expires = Date(timeIntervalSince1970: TimeInterval(exp))
  }
}
