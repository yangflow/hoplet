import Foundation

/// The hosts this device has paired with, in one JSON file. Nothing here is
/// secret: a host key is public and its routes are addresses.
public struct HostRegistry: Sendable {
  private let url: URL

  public init(url: URL) { self.url = url }

  /// The saved hosts; none if nothing was saved or the file cannot be read.
  public func load() -> [PairedHost] {
    guard let data = try? Data(contentsOf: url) else { return [] }
    return (try? JSONDecoder().decode([PairedHost].self, from: data)) ?? []
  }

  /// Replaces the saved hosts.
  public func save(_ hosts: [PairedHost]) throws {
    try FileManager.default.createDirectory(
      at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    try JSONEncoder().encode(hosts).write(to: url, options: .atomic)
  }
}
