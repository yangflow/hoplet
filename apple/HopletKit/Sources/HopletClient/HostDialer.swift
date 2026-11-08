import CryptoKit
import Foundation

/// A host this device has paired with.
public struct PairedHost: Codable, Equatable, Identifiable, Sendable {
  public var id: String
  public var key: Data
  public var name: String
  public var routes: [String]

  public init(id: String, key: Data, name: String, routes: [String]) {
    self.id = id
    self.key = key
    self.name = name
    self.routes = routes
  }
}

/// Why no route reached a host.
public enum RouteError: Swift.Error, Equatable, Sendable {
  case unreachable
}

/// Where a route is dialed for a host: a relay route asks the relay for the
/// host, a direct route is the host's own Noise path. Nil for a bad route.
public func routeURL(_ route: String, host: String) -> URL? {
  guard var parts = URLComponents(string: route) else { return nil }
  if parts.path == "/v1/connect" {
    parts.queryItems = [URLQueryItem(name: "host", value: host)]
    return parts.url
  }
  let base = route.hasSuffix("/") ? String(route.dropLast()) : route
  return URL(string: base + "/v1/noise")
}

/// Dials `routes` in order and returns the first that answers, with its
/// index. A device refusal is authenticated and holds on every route, so it
/// ends the search; a pairing refusal is reported once every route has failed.
public func dialInOrder<Value: Sendable>(
  _ routes: [URL], dial: @Sendable (URL) async throws -> Value
) async throws -> (value: Value, index: Int) {
  var pairingRefused = false
  for (index, route) in routes.enumerated() {
    try Task.checkCancellation()
    do {
      return (try await dial(route), index)
    } catch NoiseError.deviceRefused {
      throw NoiseError.deviceRefused
    } catch NoiseError.pairingRefused {
      pairingRefused = true
    } catch {
      continue
    }
  }
  throw pairingRefused ? NoiseError.pairingRefused : RouteError.unreachable
}

/// Connects this device to its hosts over WebSocket routes.
public struct HostDialer: Sendable {
  public var identity: Curve25519.KeyAgreement.PrivateKey
  /// Bounds one route's dial, handshake and hello.
  public var routeTimeout: Duration = .seconds(15)

  public init(identity: Curve25519.KeyAgreement.PrivateKey) {
    self.identity = identity
  }

  /// Joins the host a pairing code names; the connection is an ordinary one after.
  public func pair(_ code: PairingCode, name: String, first: [URL] = []) async throws -> Connection
  {
    let options = ConnectOptions(
      identity: identity, hostKey: code.key, pairing: .init(psk: code.psk, name: name))
    let routes = first + code.routes.compactMap { routeURL($0, host: code.id) }
    return try await dialInOrder(routes) { try await connect($0, options) }.value
  }

  /// Connects to a paired host, trying `first` and then its routes, at most
  /// `limit` of them.
  public func connect(_ host: PairedHost, first: [URL] = [], limit: Int? = nil) async throws
    -> (value: Connection, index: Int)
  {
    let options = ConnectOptions(identity: identity, hostKey: host.key)
    let routes = first + host.routes.compactMap { routeURL($0, host: host.id) }
    return try await dialInOrder(Array(routes.prefix(limit ?? routes.count))) {
      try await connect($0, options)
    }
  }

  private func connect(_ url: URL, _ options: ConnectOptions) async throws -> Connection {
    let transport = WebSocketTransport(url: url)
    let timeout = routeTimeout
    let timer = Task {
      try await Task.sleep(for: timeout)
      await transport.close()
    }
    defer { timer.cancel() }
    return try await Connection.connect(transport: transport, options: options)
  }
}
