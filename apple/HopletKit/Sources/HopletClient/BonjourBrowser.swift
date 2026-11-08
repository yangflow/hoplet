import Foundation
import Network

/// Finds hosts on the local network by their host ID.
public final class BonjourBrowser: Sendable {
  public static let serviceType = "_hoplet._tcp"

  private let browser = NWBrowser(
    for: .bonjour(type: serviceType, domain: nil), using: .tcp)
  private let queue = DispatchQueue(label: "hoplet.bonjour")

  public init() {}

  /// Starts browsing; `onChange` gets every host found, by host ID.
  public func start(onChange: @escaping @Sendable ([String: NWEndpoint]) -> Void) {
    browser.browseResultsChangedHandler = { results, _ in
      var found: [String: NWEndpoint] = [:]
      for result in results {
        if case .service(let name, _, _, _) = result.endpoint { found[name] = result.endpoint }
      }
      onChange(found)
    }
    browser.start(queue: queue)
  }

  public func stop() { browser.cancel() }

  /// The direct route of a discovered host, nil if it does not answer in
  /// time. IPv4 only: a link-local IPv6 address needs a scope a URL cannot carry.
  public static func resolve(_ endpoint: NWEndpoint) async -> URL? {
    let parameters = NWParameters.tcp
    if let ip = parameters.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options {
      ip.version = .v4
    }
    let connection = NWConnection(to: endpoint, using: parameters)
    let queue = DispatchQueue(label: "hoplet.bonjour.resolve")
    defer { connection.cancel() }
    return await withCheckedContinuation { (done: CheckedContinuation<URL?, Never>) in
      let once = Once(done)
      connection.stateUpdateHandler = { state in
        switch state {
        case .ready:
          guard
            case .hostPort(.ipv4(let address), let port)? = connection.currentPath?.remoteEndpoint
          else { return once.finish(nil) }
          once.finish(URL(string: "ws://\(address):\(port.rawValue)/v1/noise"))
        case .failed, .cancelled: once.finish(nil)
        default: break
        }
      }
      connection.start(queue: queue)
      queue.asyncAfter(deadline: .now() + 3) { once.finish(nil) }
    }
  }
}

/// Resumes a continuation the first time only; used on one serial queue.
private final class Once: @unchecked Sendable {
  private var done: CheckedContinuation<URL?, Never>?

  init(_ done: CheckedContinuation<URL?, Never>) { self.done = done }

  func finish(_ url: URL?) {
    done?.resume(returning: url)
    done = nil
  }
}
