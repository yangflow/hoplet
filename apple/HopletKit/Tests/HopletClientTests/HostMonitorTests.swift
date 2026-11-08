import Foundation
import Testing

@testable import HopletClient

/// A link the test loses by hand.
private final class FakeLink: MonitoredLink, @unchecked Sendable {
  private let (stream, continuation) = AsyncStream.makeStream(of: Void.self)
  private let lock = NSLock()
  private var closedFlag = false
  var closed: Bool { lock.withLock { closedFlag } }

  func lose() { continuation.finish() }
  func waitUntilLost() async { for await _ in stream {} }
  func close() async {
    lock.withLock { closedFlag = true }
    continuation.finish()
  }
}

private enum Answer: Sendable {
  case link(Int)
  case fail, refused, hang
}

/// Scripted dials, sleeps that only the test ends, and observed states.
private actor Harness {
  var answers: [Answer]
  var limits: [Int?] = []
  var sleeps: [Duration] = []
  var links: [FakeLink] = []
  private var waking: [CheckedContinuation<Void, any Swift.Error>] = []
  private var graces: [CheckedContinuation<Void, any Swift.Error>] = []
  private var hung: [CheckedContinuation<Void, any Swift.Error>] = []

  init(_ answers: [Answer]) { self.answers = answers }

  func add(_ more: Answer...) { answers += more }

  func dial(limit: Int?) async throws -> (value: any MonitoredLink, index: Int) {
    limits.append(limit)
    switch answers.isEmpty ? .fail : answers.removeFirst() {
    case .link(let index):
      let link = FakeLink()
      links.append(link)
      return (link, index)
    case .fail: throw RouteError.unreachable
    case .refused: throw NoiseError.deviceRefused
    case .hang:
      try await withCheckedThrowingContinuation { hung.append($0) }
      throw RouteError.unreachable
    }
  }

  func sleep(_ delay: Duration) async throws {
    sleeps.append(delay)
    try await withCheckedThrowingContinuation { waking.append($0) }
  }

  func graceSleep() async throws {
    try await withCheckedThrowingContinuation { graces.append($0) }
  }

  func wake() {
    if !waking.isEmpty { waking.removeFirst().resume() }
  }

  var sleeping: Int { waking.count }
  var graceRunning: Bool { !graces.isEmpty }

  func endGrace() { graces.removeFirst().resume() }

  /// Lets everything still suspended finish.
  func release() {
    for sleeper in waking + graces + hung { sleeper.resume(throwing: CancellationError()) }
    waking = []
    graces = []
    hung = []
  }
}

private func make(_ answers: [Answer]) -> (
  Harness, HostMonitor, AsyncStream<HostLinkState>.AsyncIterator
) {
  let harness = Harness(answers)
  let (states, sink) = AsyncStream.makeStream(of: HostLinkState.self)
  let monitor = HostMonitor(
    dial: { try await harness.dial(limit: $0) },
    sleep: { try await harness.sleep($0) },
    graceSleep: { _ in try await harness.graceSleep() },
    onState: { sink.yield($0) })
  return (harness, monitor, states.makeAsyncIterator())
}

/// Waits for the monitor to start its next sleep.
private func untilSleeping(_ harness: Harness) async {
  while await harness.sleeping == 0 { await Task.yield() }
}

@Suite struct HostMonitorBehavior {
  @Test func aLossTheFirstRedialRepairsIsNotReported() async {
    var (harness, monitor, states) = make([.link(0), .link(0), .refused])
    await monitor.start()
    #expect(await states.next() == .online)
    await harness.links[0].lose()
    while await harness.links.count < 2 { await Task.yield() }
    await harness.links[1].lose()
    #expect(await states.next() == .revoked)
    await monitor.stop()
    await harness.release()
  }

  @Test func aHangingRedialIsReportedWhenTheGraceEnds() async {
    var (harness, monitor, states) = make([.link(0), .hang])
    await monitor.start()
    #expect(await states.next() == .online)
    await harness.links[0].lose()
    while await !harness.graceRunning { await Task.yield() }
    await harness.endGrace()
    #expect(await states.next() == .reconnecting)
    await monitor.stop()
    await harness.release()
  }

  @Test func failuresGoReconnectingThenOfflineThenBack() async {
    var (harness, monitor, states) = make([.link(0), .fail, .fail, .fail, .fail, .fail, .fail])
    await monitor.start()
    #expect(await states.next() == .online)
    await harness.links[0].lose()
    #expect(await states.next() == .reconnecting)
    for _ in 0..<5 {
      await untilSleeping(harness)
      await harness.wake()
    }
    #expect(await states.next() == .offline)
    await untilSleeping(harness)
    #expect(await harness.sleeps == [1, 2, 4, 8, 16, 30].map { .seconds($0) })
    await harness.add(.link(0))
    await monitor.reconnect()
    #expect(await states.next() == .reconnecting)
    #expect(await states.next() == .online)
    await monitor.stop()
    await harness.release()
  }

  @Test func aRefusalIsFinal() async {
    var (harness, monitor, states) = make([.refused])
    await monitor.start()
    #expect(await states.next() == .revoked)
    await monitor.reconnect()
    await monitor.stop()
    await harness.release()
    #expect(await harness.limits.count == 1)
  }

  @Test func aLaterRouteMovesToAnEarlierOneWithoutAReport() async {
    var (harness, monitor, states) = make([.link(1), .fail, .link(0), .refused])
    await monitor.start()
    #expect(await states.next() == .online)
    await untilSleeping(harness)
    await harness.wake()
    while await harness.limits.count < 2 { await Task.yield() }
    await untilSleeping(harness)
    await harness.wake()
    while await harness.links.count < 2 { await Task.yield() }
    #expect(await harness.limits == [nil, 1, 1])
    while await !(harness.links[0].closed) { await Task.yield() }
    await harness.links[1].lose()
    #expect(await states.next() == .revoked)
    await monitor.stop()
    await harness.release()
  }
}

@Suite struct Routes {
  @Test func directAndRelayURLs() {
    #expect(routeURL("ws://h:1", host: "x")?.absoluteString == "ws://h:1/v1/noise")
    #expect(routeURL("ws://h:1/", host: "x")?.absoluteString == "ws://h:1/v1/noise")
    #expect(
      routeURL("wss://r/v1/connect", host: "x")?.absoluteString == "wss://r/v1/connect?host=x")
  }

  @Test func searchOrder() async throws {
    let a = URL(string: "ws://a")!
    let b = URL(string: "ws://b")!
    let found = try await dialInOrder([a, b]) { url -> String in
      if url == a { throw RouteError.unreachable }
      return url.absoluteString
    }
    #expect(found.value == "ws://b" && found.index == 1)
    await #expect(throws: NoiseError.deviceRefused) {
      _ = try await dialInOrder([a, b]) { url -> Int in
        if url == b { Issue.record("tried a route after a refusal") }
        throw NoiseError.deviceRefused
      }
    }
    await #expect(throws: NoiseError.pairingRefused) {
      _ = try await dialInOrder([a, b]) { url -> Int in
        throw url == a ? NoiseError.pairingRefused : RouteError.unreachable
      }
    }
    await #expect(throws: RouteError.unreachable) {
      _ = try await dialInOrder([a]) { _ -> Int in throw RouteError.unreachable }
    }
  }
}

@Suite struct Registry {
  @Test func savedHostsComeBack() throws {
    let url = FileManager.default.temporaryDirectory
      .appending(path: UUID().uuidString).appending(path: "hosts.json")
    defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
    let registry = HostRegistry(url: url)
    #expect(registry.load().isEmpty)
    let host = PairedHost(id: "a", key: Data([1]), name: "Mac", routes: ["ws://h:1"])
    try registry.save([host])
    #expect(registry.load() == [host])
  }
}
