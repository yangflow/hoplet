import CryptoKit
import Foundation
import Security

/// This device's long-term key, kept in the Keychain and never leaving it
/// except through the handshake.
enum DeviceIdentity {
  private static var query: [String: Any] {
    [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: "dev.hoplet.identity",
      kSecAttrAccount as String: "device",
    ]
  }

  enum Failure: Swift.Error { case keychain(OSStatus) }

  /// The saved key, created on first use.
  static func load() throws -> Curve25519.KeyAgreement.PrivateKey {
    var request = query
    request[kSecReturnData as String] = true
    var item: CFTypeRef?
    let status = SecItemCopyMatching(request as CFDictionary, &item)
    if status == errSecSuccess, let data = item as? Data {
      return try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: data)
    }
    guard status == errSecItemNotFound else { throw Failure.keychain(status) }
    let key = Curve25519.KeyAgreement.PrivateKey()
    var add = query
    add[kSecValueData as String] = key.rawRepresentation
    add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
    let added = SecItemAdd(add as CFDictionary, nil)
    guard added == errSecSuccess else { throw Failure.keychain(added) }
    return key
  }
}
