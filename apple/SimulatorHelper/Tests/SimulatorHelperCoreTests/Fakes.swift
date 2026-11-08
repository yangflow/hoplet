import Synchronization

@testable import SimulatorHelperCore

enum Event: Equatable {
  case touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge)
  case key(UInt32, down: Bool)
  case button(HardwareButton, down: Bool)
  case sleep(Double)
  case response(String)
}

/// One ordered log shared by the fake input, the fake clock and the output.
final class EventLog: Sendable {
  private let events = Mutex<[Event]>([])

  func record(_ event: Event) { events.withLock { $0.append(event) } }
  var all: [Event] { events.withLock { $0 } }
  var touches: [Event] { all.filter { if case .touch = $0 { true } else { false } } }
  var responses: [String] {
    all.compactMap { if case .response(let line) = $0 { line } else { nil } }
  }
}

struct FakeFailure: Error {}

final class FakeInput: DeviceInput {
  let log: EventLog
  /// Number of upcoming touch-up events that fail.
  let failingUps = Mutex(0)

  /// When set, the first touch-up suspends, ignoring cancellation, after
  /// running the closure, until `release()`. It must not block a thread: the
  /// loop under test needs the concurrency pool, which may have only one.
  private let stuck = Mutex<(@Sendable () -> Void)?>(nil)
  private let gate = Mutex<Gate>(Gate())

  private struct Gate {
    var released = false
    var waiter: CheckedContinuation<Void, Never>?
  }

  init(log: EventLog) { self.log = log }

  func makeFirstUpStuck(onStuck: @escaping @Sendable () -> Void) {
    stuck.withLock { $0 = onStuck }
  }

  func release() {
    let waiter = gate.withLock { state in
      defer { state.waiter = nil }
      state.released = true
      return state.waiter
    }
    waiter?.resume()
  }

  private func waitUntilReleased() async {
    await withCheckedContinuation { continuation in
      let released = gate.withLock { state in
        if !state.released { state.waiter = continuation }
        return state.released
      }
      if released { continuation.resume() }
    }
  }

  func touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge) async throws {
    log.record(.touch(x: x, y: y, phase: phase, edge: edge))
    if phase == .up,
      let onStuck = stuck.withLock({ value in
        defer { value = nil }
        return value
      })
    {
      onStuck()
      await waitUntilReleased()
    }
    if phase == .up {
      let fails = failingUps.withLock { count -> Bool in
        defer { if count > 0 { count -= 1 } }
        return count > 0
      }
      if fails { throw FakeFailure() }
    }
  }

  /// A key-down with this usage fails.
  let failingKeyDown = Mutex<UInt32?>(nil)

  func key(usage: UInt32, isDown: Bool) async throws {
    log.record(.key(usage, down: isDown))
    if isDown && failingKeyDown.withLock({ $0 }) == usage { throw FakeFailure() }
  }

  func button(_ button: HardwareButton, isDown: Bool) async throws {
    log.record(.button(button, down: isDown))
  }
}

/// Sleeps return at once. A sleep of `hangOn` runs `hangAction`, then
/// suspends until cancelled. The command timeout sleep waits for `fireTimeout`.
final class FakeClock: HelperClock {
  let log: EventLog
  let hangOn: Double?
  private let hangAction = Mutex<(@Sendable () async -> Void)?>(nil)
  private let timeoutStream = AsyncStream<Void>.makeStream()

  init(log: EventLog, hangOn: Double? = nil) {
    self.log = log
    self.hangOn = hangOn
  }

  func setHangAction(_ action: @escaping @Sendable () async -> Void) {
    hangAction.withLock { $0 = action }
  }

  func fireTimeout() { timeoutStream.continuation.yield(()) }

  func sleep(seconds: Double) async throws {
    if seconds == CommandLoop.commandTimeout {
      for await _ in timeoutStream.stream { return }
      throw CancellationError()
    }
    log.record(.sleep(seconds))
    if let hangOn, seconds == hangOn {
      if let action = hangAction.withLock({ $0 }) { await action() }
      // Ends as soon as the task is cancelled.
      try await Task.sleep(for: .seconds(3600))
    }
    try Task.checkCancellation()
  }
}

/// What a test can reach while the loop runs.
struct Harness {
  let loop: CommandLoop
  let clock: FakeClock
  let input: FakeInput
  /// Closes the fake stdin.
  let closeInput: @Sendable () -> Void
}

/// Feeds `lines` to a fresh loop, closes stdin after `responses` responses
/// were written (unless the loop ended first), and returns once the loop has
/// ended.
func runLoop(
  _ lines: [String],
  responses expected: Int,
  prepare: (Harness) -> Void = { _ in },
  hangOn: Double? = nil
) async -> EventLog {
  let log = EventLog()
  let input = FakeInput(log: log)
  let clock = FakeClock(log: log, hangOn: hangOn)
  let (responses, responseSink) = AsyncStream<Void>.makeStream()
  let loop = CommandLoop(input: input, clock: clock) {
    log.record(.response($0))
    responseSink.yield(())
  }
  let (stream, continuation) = AsyncStream<String>.makeStream()
  prepare(
    Harness(loop: loop, clock: clock, input: input, closeInput: { continuation.finish() }))
  for line in lines { continuation.yield(line) }
  let closer = Task {
    var seen = 0
    if expected == 0 { continuation.finish() }
    for await _ in responses {
      seen += 1
      if seen >= expected {
        continuation.finish()
        return
      }
    }
  }
  await loop.run(lines: stream)
  closer.cancel()
  input.release()
  return log
}
