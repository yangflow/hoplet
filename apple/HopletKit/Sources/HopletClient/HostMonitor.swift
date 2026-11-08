import Foundation

/// How this device stands with one host.
public enum HostLinkState: Equatable, Sendable {
  case online, reconnecting, offline, revoked
}

/// A live connection the monitor watches.
public protocol MonitoredLink: Sendable {
  func waitUntilLost() async
  func close() async
}

extension Connection: MonitoredLink {}

/// Keeps one host connected: redials a lost connection with backoff and moves
/// to an earlier route when one answers.
///
/// A loss repaired within `graceDelay` is not reported, and neither is a route
/// change. A device refusal is final.
public actor HostMonitor {
  /// Dials at most `limit` routes in order (all when nil).
  public typealias Dial =
    @Sendable (_ limit: Int?) async throws -> (value: any MonitoredLink, index: Int)
  public typealias Sleep = @Sendable (Duration) async throws -> Void

  private enum Event: Sendable {
    case attempt
    case retry(UInt64)
    case lost(UInt64)
    case probe(UInt64)
    case nudge, betterRoute
  }

  /// Failed redials before a host counts as offline.
  static let reconnectingAttempts = 5
  static let graceDelay: Duration = .seconds(2)
  static let retryMax: Duration = .seconds(30)
  static let probeEvery: Duration = .seconds(60)

  private let dial: Dial
  private let sleep: Sleep
  private let graceSleep: Sleep
  private let onState: @Sendable (HostLinkState) -> Void
  private let events: AsyncStream<Event>
  private let send: AsyncStream<Event>.Continuation
  private var loop: Task<Void, Never>?
  private var timer: Task<Void, Never>?
  private var grace: Task<Void, Never>?
  private var state = HostLinkState.reconnecting
  private var link: (any MonitoredLink)?
  private var index = 0
  private var generation: UInt64 = 0
  private var timerID: UInt64 = 0
  private var failures = 0

  public init(
    dial: @escaping Dial,
    sleep: @escaping Sleep = { try await Task.sleep(for: $0) },
    graceSleep: @escaping Sleep = { try await Task.sleep(for: $0) },
    onState: @escaping @Sendable (HostLinkState) -> Void
  ) {
    self.dial = dial
    self.sleep = sleep
    self.graceSleep = graceSleep
    self.onState = onState
    (events, send) = AsyncStream.makeStream()
  }

  /// Starts connecting. The state reported first is the first change from
  /// `reconnecting`.
  public func start() {
    guard loop == nil else { return }
    send.yield(.attempt)
    loop = Task {
      for await event in events { await handle(event) }
    }
  }

  /// Redials now if the host is not connected: the user asked, the app came
  /// back or the network changed.
  public func reconnect() { send.yield(.nudge) }

  /// Tries the routes again from the first: a nearer one has appeared.
  public func routesChanged() { send.yield(.betterRoute) }

  /// Ends monitoring and closes the connection.
  public func stop() async {
    loop?.cancel()
    timer?.cancel()
    grace?.cancel()
    send.finish()
    await link?.close()
    link = nil
  }

  private func handle(_ event: Event) async {
    guard state != .revoked else { return }
    switch event {
    case .attempt:
      await attempt()
    case .retry(let id):
      if id == timerID, link == nil { await attempt() }
    case .lost(let id):
      guard id == generation else { return }
      link = nil
      failures = 0
      startGrace()
      await attempt()
    case .probe(let id):
      if id == timerID, link != nil { await probe(limit: index) }
    case .betterRoute:
      if link != nil { await probe(limit: nil) }
    case .nudge:
      guard link == nil else { return }
      failures = 0
      set(.reconnecting)
      await attempt()
    }
  }

  private func attempt() async {
    do {
      adopt(try await dial(nil))
      grace?.cancel()
      failures = 0
      set(.online)
    } catch NoiseError.deviceRefused {
      timer?.cancel()
      set(.revoked)
    } catch {
      failures += 1
      set(failures > Self.reconnectingAttempts ? .offline : .reconnecting)
      schedule(after: backoff()) { .retry($0) }
    }
  }

  /// Connects on an earlier route, then lets the old connection go.
  private func probe(limit: Int?) async {
    if let better = try? await dial(limit) {
      let old = link
      adopt(better)
      await old?.close()
    } else {
      scheduleProbe()
    }
  }

  private func adopt(_ dialed: (value: any MonitoredLink, index: Int)) {
    generation += 1
    let id = generation
    link = dialed.value
    index = dialed.index
    let send = self.send
    let watched = dialed.value
    Task {
      await watched.waitUntilLost()
      send.yield(.lost(id))
    }
    scheduleProbe()
  }

  /// Reports the loss if the redial is still going when the grace runs out.
  private func startGrace() {
    grace?.cancel()
    let sleep = graceSleep
    grace = Task { [weak self] in
      guard (try? await sleep(Self.graceDelay)) != nil, !Task.isCancelled else { return }
      await self?.graceEnded()
    }
  }

  private func graceEnded() {
    if link == nil, state == .online { set(.reconnecting) }
  }

  private func scheduleProbe() {
    timer?.cancel()
    if index > 0 { schedule(after: Self.probeEvery) { .probe($0) } }
  }

  /// 1, 2, 4… seconds up to `retryMax`.
  private func backoff() -> Duration {
    min(.seconds(1 << min(failures - 1, 5)), Self.retryMax)
  }

  private func schedule(after delay: Duration, _ event: @escaping @Sendable (UInt64) -> Event) {
    timer?.cancel()
    timerID += 1
    let id = timerID
    let send = self.send
    let sleep = self.sleep
    timer = Task {
      guard (try? await sleep(delay)) != nil, !Task.isCancelled else { return }
      send.yield(event(id))
    }
  }

  private func set(_ new: HostLinkState) {
    guard new != state else { return }
    state = new
    onState(new)
  }
}
