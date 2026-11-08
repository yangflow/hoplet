import Foundation
import HopletClient
import Network
import SwiftUI

/// Paired hosts and the live connection kept to each.
@MainActor @Observable
final class HostConnections {
  private(set) var paired: [PairedHost] = []
  @ObservationIgnored var onChange: ((String, HostLink) -> Void)?

  @ObservationIgnored private let registry = HostRegistry(
    url: URL.applicationSupportDirectory.appending(path: "hosts.json"))
  @ObservationIgnored private var monitors: [String: HostMonitor] = [:]
  @ObservationIgnored private var nearby: [String: URL] = [:]
  @ObservationIgnored private var browser: BonjourBrowser?
  @ObservationIgnored private let path = NWPathMonitor()
  @ObservationIgnored private var dialer: HostDialer? = {
    (try? DeviceIdentity.load()).map { HostDialer(identity: $0) }
  }()

  /// Connects to every saved host and redials when the network changes.
  func start() {
    paired = registry.load()
    if !paired.isEmpty { startDiscovery() }
    for host in paired { monitor(host) }
    path.pathUpdateHandler = { [weak self] update in
      guard update.status == .satisfied else { return }
      Task { @MainActor in self?.reconnectAll() }
    }
    path.start(queue: .global(qos: .utility))
  }

  /// Browses the local network; the first call asks for permission.
  func startDiscovery() {
    guard browser == nil else { return }
    let browser = BonjourBrowser()
    self.browser = browser
    browser.start { [weak self] found in
      Task { @MainActor in await self?.update(found) }
    }
  }

  /// Joins a host, saves it and starts keeping it connected.
  func pair(_ code: PairingCode, deviceName: String) async throws {
    guard let dialer else { throw RouteError.unreachable }
    let first = nearby[code.id].map { [$0] } ?? []
    let connection = try await dialer.pair(code, name: deviceName, first: first)
    await connection.close()
    let host = PairedHost(id: code.id, key: code.key, name: code.name, routes: code.routes)
    await monitors.removeValue(forKey: host.id)?.stop()
    paired.removeAll { $0.id == host.id }
    paired.append(host)
    try registry.save(paired)
    monitor(host)
  }

  /// Redials one paired host; false if `name` is not one.
  func reconnect(name: String) -> Bool {
    guard let host = paired.first(where: { $0.name == name }), let monitor = monitors[host.id]
    else { return false }
    Task { await monitor.reconnect() }
    return true
  }

  func reconnectAll() {
    for monitor in monitors.values { Task { await monitor.reconnect() } }
  }

  private func monitor(_ host: PairedHost) {
    guard let dialer else { return }
    let monitor = HostMonitor(
      dial: { [weak self] limit in
        let first = await self?.nearbyRoute(host.id).map { [$0] } ?? []
        let dialed = try await dialer.connect(host, first: first, limit: limit)
        return (dialed.value, dialed.index)
      },
      onState: { [weak self] state in
        Task { @MainActor in self?.onChange?(host.name, HostLink(state)) }
      })
    monitors[host.id] = monitor
    onChange?(host.name, .reconnecting)
    Task { await monitor.start() }
  }

  private func nearbyRoute(_ id: String) -> URL? { nearby[id] }

  private func update(_ found: [String: NWEndpoint]) async {
    for id in nearby.keys where found[id] == nil { nearby[id] = nil }
    for (id, endpoint) in found where nearby[id] == nil {
      guard let url = await BonjourBrowser.resolve(endpoint) else { continue }
      nearby[id] = url
      if let monitor = monitors[id] { await monitor.routesChanged() }
    }
  }
}

extension HostLink {
  fileprivate init(_ state: HostLinkState) {
    switch state {
    case .online: self = .online
    case .reconnecting: self = .reconnecting
    case .offline: self = .offline
    case .revoked: self = .revoked
    }
  }
}
