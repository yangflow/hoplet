import Foundation

/// Runs commands one at a time against a `DeviceInput` and keeps the lift
/// guarantee: a touch that went down is always lifted before the loop ends.
public actor CommandLoop {
  /// Interval between interpolated swipe events.
  public static let stepInterval = 0.016
  /// Longest a single command may run.
  public static let commandTimeout = 15.0

  private struct ActiveTouch {
    var x: Double
    var y: Double
    var edge: TouchEdge
  }

  private enum Outcome {
    case finished(Error?)
    case timedOut
    case sleepCancelled
  }

  private let input: any DeviceInput
  private let clock: any HelperClock
  private let write: @Sendable (String) -> Void
  private var activeTouch: ActiveTouch?
  private var terminated = false
  private var runTask: Task<Void, Never>?

  /// - Parameter write: receives each response as one JSON line.
  public init(
    input: any DeviceInput,
    clock: any HelperClock = SystemClock(),
    write: @escaping @Sendable (String) -> Void
  ) {
    self.input = input
    self.clock = clock
    self.write = write
  }

  /// Handles request lines in arrival order until the stream finishes, a
  /// command times out, or `terminate()` is called. A touch still in progress
  /// is lifted before this returns.
  public func run(lines: AsyncStream<String>) async {
    let task = Task { await self.loop(lines) }
    runTask = task
    if terminated { task.cancel() }
    await task.value
    runTask = nil
  }

  /// Asks the loop to stop. Safe to call from a signal handler's task.
  public func terminate() {
    terminated = true
    runTask?.cancel()
  }

  private func loop(_ lines: AsyncStream<String>) async {
    for await line in lines {
      if terminated { break }
      if await !handle(line: line) { break }
    }
    // Lift in a fresh task: this one may already be cancelled.
    await Task { await self.liftIfNeeded() }.value
  }

  /// Returns false when the loop must end.
  private func handle(line: String) async -> Bool {
    let request: Request
    switch Request.decode(line: line) {
    case .failure(let failure):
      write(Response.failure(failure.id, failure.error).line())
      return true
    case .success(let decoded):
      request = decoded
    }

    switch await executeWithTimeout(request.command) {
    case .finished(nil):
      write(Response.success(request.id).line())
      return true
    case .finished(let error?):
      if error is CancellationError {
        write(Response.failure(request.id, HelperError(.cancelled, "helper is stopping")).line())
        return false
      }
      let failure = (error as? HelperError) ?? HelperError(.inputFailed, "\(error)")
      write(Response.failure(request.id, failure).line())
      await liftIfNeeded()
      return true
    case .timedOut:
      let message = "command exceeded \(Int(Self.commandTimeout)) seconds"
      write(Response.failure(request.id, HelperError(.timeout, message)).line())
      return false
    case .sleepCancelled:
      return false
    }
  }

  private func executeWithTimeout(_ command: Command) async -> Outcome {
    let clock = self.clock
    return await withTaskGroup(of: Outcome.self) { group in
      group.addTask {
        do {
          try await self.execute(command)
          return .finished(nil)
        } catch {
          return .finished(error)
        }
      }
      group.addTask {
        do {
          try await clock.sleep(seconds: Self.commandTimeout)
          return .timedOut
        } catch {
          return .sleepCancelled
        }
      }
      var result = Outcome.sleepCancelled
      for await outcome in group {
        if case .sleepCancelled = outcome { continue }
        result = outcome
        break
      }
      group.cancelAll()
      return result
    }
  }

  private func execute(_ command: Command) async throws {
    switch command {
    case .tap(let x, let y, let duration):
      try await touch(x: x, y: y, phase: .down, edge: .none)
      try await clock.sleep(seconds: duration)
      try await touch(x: x, y: y, phase: .up, edge: .none)
    case .swipe(let x, let y, let x2, let y2, let duration, let edge):
      let steps = max(1, Int((duration / Self.stepInterval).rounded()))
      try await touch(x: x, y: y, phase: .down, edge: edge)
      for step in 1...steps {
        try await clock.sleep(seconds: Self.stepInterval)
        let t = Double(step) / Double(steps)
        // The last step lands exactly on the end point.
        let px = step == steps ? x2 : x + (x2 - x) * t
        let py = step == steps ? y2 : y + (y2 - y) * t
        try await touch(x: px, y: py, phase: .down, edge: edge)
      }
      try await touch(x: x2, y: y2, phase: .up, edge: edge)
    case .keys(let usages):
      try await press(usages)
    case .paste:
      try await press([HIDUsage.leftCommand, HIDUsage.v])
    case .button(let button):
      try await input.button(button, isDown: true)
      try await input.button(button, isDown: false)
    }
  }

  /// Presses each usage in turn. A modifier stays held until the next
  /// non-modifier usage has been pressed and released; modifiers still held
  /// at the end are released in reverse order.
  private func press(_ usages: [UInt32]) async throws {
    var held: [UInt32] = []
    for usage in usages {
      try await input.key(usage: usage, isDown: true)
      if HIDUsage.isModifier(usage) {
        held.append(usage)
        continue
      }
      try await input.key(usage: usage, isDown: false)
      for modifier in held.reversed() { try await input.key(usage: modifier, isDown: false) }
      held.removeAll()
    }
    for modifier in held.reversed() { try await input.key(usage: modifier, isDown: false) }
  }

  private func touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge) async throws {
    // Marked before sending: a send that is cut short still gets a lift.
    if phase == .down { activeTouch = ActiveTouch(x: x, y: y, edge: edge) }
    try await input.touch(x: x, y: y, phase: phase, edge: edge)
    if phase == .up { activeTouch = nil }
  }

  private func liftIfNeeded() async {
    guard let touch = activeTouch else { return }
    do {
      try await input.touch(x: touch.x, y: touch.y, phase: .up, edge: touch.edge)
      activeTouch = nil
    } catch {
      // Nothing more can be done; the caller is about to exit.
    }
  }
}
