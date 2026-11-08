/// Time source for the command loop. Injected so tests never really wait.
public protocol HelperClock: Sendable {
  /// Suspends for the given number of seconds. Throws on cancellation.
  func sleep(seconds: Double) async throws
}

/// The real clock.
public struct SystemClock: HelperClock {
  public init() {}

  public func sleep(seconds: Double) async throws {
    try await Task.sleep(for: .seconds(seconds))
  }
}
