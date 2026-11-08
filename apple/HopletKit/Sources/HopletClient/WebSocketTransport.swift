import Foundation

/// Carries Noise messages as WebSocket binary messages.
public final class WebSocketTransport: MessageTransport {
  private let task: URLSessionWebSocketTask

  public init(url: URL, session: URLSession = .shared) {
    task = session.webSocketTask(with: url)
    task.maximumMessageSize = 65536
    task.resume()
  }

  public func send(_ message: Data) async throws {
    try await task.send(.data(message))
  }

  public func receive() async throws -> Data {
    switch try await task.receive() {
    case .data(let data): return data
    case .string: throw ConnectionError.badFrame
    @unknown default: throw ConnectionError.badFrame
    }
  }

  public func close() async {
    task.cancel(with: .normalClosure, reason: nil)
  }
}
