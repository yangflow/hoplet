import Foundation

/// Runs commands one at a time against a `DeviceInput` and keeps the lift
/// guarantee: a touch that went down is lifted before the loop ends, even when
/// a device call is stuck and ignores cancellation.
///
/// Commands run in unstructured tasks. The loop reacts to whichever comes
/// first: the command finishing, its timeout, a termination request, or stdin
/// closing. After an interruption it never waits for the abandoned task.
public actor CommandLoop {
  /// Interval between interpolated swipe events.
  public static let stepInterval = 0.016
  /// Longest a single command may run.
  public static let commandTimeout = 15.0
  /// How long a hardware button stays pressed.
  public static let buttonHold = 0.1

  private struct ActiveTouch {
    var x: Double
    var y: Double
    var edge: TouchEdge
  }

  private enum Event: Sendable {
    case line(String)
    case stdinClosed
    case terminate
    case finished(generation: Int, error: (any Error)?)
    case timeout(generation: Int)
  }

  private struct Running {
    let generation: Int
    let id: RequestID?
    let task: Task<Void, Never>
    let timer: Task<Void, Never>
  }

  private let input: any DeviceInput
  private let clock: any HelperClock
  private let write: @Sendable (String) -> Void
  private let events: AsyncStream<Event>
  private let sink: AsyncStream<Event>.Continuation
  private var pending: [String] = []
  private var running: Running?
  private var generation = 0
  private var activeTouch: ActiveTouch?
  private var heldButton: HardwareButton?
  private var heldModifiers: [UInt32] = []

  /// - Parameter write: receives each response as one JSON line.
  public init(
    input: any DeviceInput,
    clock: any HelperClock = SystemClock(),
    write: @escaping @Sendable (String) -> Void
  ) {
    self.input = input
    self.clock = clock
    self.write = write
    (events, sink) = AsyncStream<Event>.makeStream()
  }

  /// Handles request lines in arrival order until the stream finishes, a
  /// command times out, or `terminate()` is called. A command in flight when
  /// stdin closes is interrupted and buffered commands are dropped. A touch in
  /// progress is lifted before this returns.
  public func run(lines: AsyncStream<String>) async {
    let sink = self.sink
    let reader = Task {
      for await line in lines { sink.yield(.line(line)) }
      sink.yield(.stdinClosed)
    }
    for await event in events {
      if await !handle(event) { break }
    }
    reader.cancel()
    await releaseEverything()
  }

  /// Asks the loop to stop. Callable from any thread, including a signal
  /// handler's task; may be called before `run`.
  public nonisolated func terminate() {
    sink.yield(.terminate)
  }

  /// Returns false when the loop must end.
  private func handle(_ event: Event) async -> Bool {
    switch event {
    case .line(let line):
      pending.append(line)
      pump()
      return true
    case .finished(let finished, let error):
      guard let current = running, current.generation == finished else { return true }
      current.timer.cancel()
      running = nil
      if let error {
        let failure = (error as? HelperError) ?? HelperError(.inputFailed, "\(error)")
        write(Response.failure(current.id, failure).line())
        await releaseEverything()
      } else {
        write(Response.success(current.id).line())
      }
      pump()
      return true
    case .timeout(let timedOut):
      guard running?.generation == timedOut else { return true }
      let message = "command exceeded \(Int(Self.commandTimeout)) seconds"
      interrupt(HelperError(.timeout, message))
      return false
    case .terminate:
      interrupt(HelperError(.cancelled, "helper is stopping"))
      return false
    case .stdinClosed:
      interrupt(HelperError(.cancelled, "stdin closed"))
      return false
    }
  }

  /// Abandons the command in flight, if any, and answers it with `error`.
  private func interrupt(_ error: HelperError) {
    guard let current = running else { return }
    running = nil
    current.task.cancel()
    current.timer.cancel()
    write(Response.failure(current.id, error).line())
  }

  /// Starts the next valid pending command if none is running.
  private func pump() {
    while running == nil, !pending.isEmpty {
      let line = pending.removeFirst()
      switch Request.decode(line: line) {
      case .failure(let failure):
        write(Response.failure(failure.id, failure.error).line())
      case .success(let request):
        start(request)
      }
    }
  }

  private func start(_ request: Request) {
    generation += 1
    let current = generation
    let command = request.command
    let sink = self.sink
    let clock = self.clock
    let task = Task {
      var failure: (any Error)?
      do {
        try await self.execute(command)
      } catch {
        failure = error
      }
      sink.yield(.finished(generation: current, error: failure))
    }
    let timer = Task {
      do {
        try await clock.sleep(seconds: Self.commandTimeout)
        sink.yield(.timeout(generation: current))
      } catch {
        // Cancelled because the command ended first.
      }
    }
    running = Running(generation: current, id: request.id, task: task, timer: timer)
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
      try Task.checkCancellation()
      // Marked before sending: a send that is cut short still gets a release.
      heldButton = button
      try await input.button(button, isDown: true)
      try await clock.sleep(seconds: Self.buttonHold)
      try Task.checkCancellation()
      try await input.button(button, isDown: false)
      heldButton = nil
    }
  }

  /// Presses each usage in turn. A modifier stays held until the next
  /// non-modifier usage has been pressed and released; modifiers still held
  /// at the end, or after a failure, are released in reverse order.
  private func press(_ usages: [UInt32]) async throws {
    do {
      for usage in usages {
        try await key(usage, isDown: true)
        if HIDUsage.isModifier(usage) { continue }
        try await key(usage, isDown: false)
        for modifier in heldModifiers.reversed() { try await key(modifier, isDown: false) }
      }
      for modifier in heldModifiers.reversed() { try await key(modifier, isDown: false) }
    } catch {
      await releaseHeldModifiers()
      throw error
    }
  }

  private func key(_ usage: UInt32, isDown: Bool) async throws {
    try Task.checkCancellation()
    // Marked before sending: a send that is cut short still gets a release.
    if isDown && HIDUsage.isModifier(usage) { heldModifiers.append(usage) }
    try await input.key(usage: usage, isDown: isDown)
    if !isDown { heldModifiers.removeAll { $0 == usage } }
  }

  private func touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge) async throws {
    try Task.checkCancellation()
    // Marked before sending: a send that is cut short still gets a lift.
    if phase == .down { activeTouch = ActiveTouch(x: x, y: y, edge: edge) }
    try await input.touch(x: x, y: y, phase: phase, edge: edge)
    if phase == .up { activeTouch = nil }
  }

  private func releaseEverything() async {
    await liftIfNeeded()
    await releaseHeldButton()
    await releaseHeldModifiers()
  }

  private func releaseHeldButton() async {
    guard let button = heldButton else { return }
    heldButton = nil
    try? await input.button(button, isDown: false)
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

  private func releaseHeldModifiers() async {
    for modifier in heldModifiers.reversed() {
      try? await input.key(usage: modifier, isDown: false)
    }
    heldModifiers.removeAll()
  }
}
