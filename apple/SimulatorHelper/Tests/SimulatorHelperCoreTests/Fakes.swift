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

  init(log: EventLog) { self.log = log }

  func touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge) async throws {
    log.record(.touch(x: x, y: y, phase: phase, edge: edge))
    if phase == .up {
      let fails = failingUps.withLock { count -> Bool in
        defer { if count > 0 { count -= 1 } }
        return count > 0
      }
      if fails { throw FakeFailure() }
    }
  }

  func key(usage: UInt32, isDown: Bool) async throws {
    log.record(.key(usage, down: isDown))
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

/// Feeds `lines` to a fresh loop and returns once the loop has ended.
func runLoop(
  _ lines: [String],
  log: EventLog = EventLog(),
  prepare: (CommandLoop, FakeClock, FakeInput) -> Void = { _, _, _ in },
  hangOn: Double? = nil
) async -> EventLog {
  let input = FakeInput(log: log)
  let clock = FakeClock(log: log, hangOn: hangOn)
  let loop = CommandLoop(input: input, clock: clock) { log.record(.response($0)) }
  prepare(loop, clock, input)
  let (stream, continuation) = AsyncStream<String>.makeStream()
  for line in lines { continuation.yield(line) }
  continuation.finish()
  await loop.run(lines: stream)
  return log
}
