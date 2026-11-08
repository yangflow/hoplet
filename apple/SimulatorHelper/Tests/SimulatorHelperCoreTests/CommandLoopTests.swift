import Testing

@testable import SimulatorHelperCore

@Suite("Command loop")
struct CommandLoopTests {
  private func up(_ x: Double, _ y: Double, _ edge: TouchEdge = .none) -> Event {
    .touch(x: x, y: y, phase: .up, edge: edge)
  }
  private func down(_ x: Double, _ y: Double, _ edge: TouchEdge = .none) -> Event {
    .touch(x: x, y: y, phase: .down, edge: edge)
  }

  @Test func tapEventSequence() async {
    let log = await runLoop([#"{"id":1,"op":"tap","x":0.5,"y":0.25}"#])
    #expect(
      log.all == [down(0.5, 0.25), .sleep(0.08), up(0.5, 0.25), .response(#"{"id":1,"ok":true}"#)])
  }

  @Test func tapUsesGivenDuration() async {
    let log = await runLoop([#"{"id":1,"op":"tap","x":0.5,"y":0.5,"duration":1.5}"#])
    #expect(log.all.contains(.sleep(1.5)))
  }

  @Test func swipeStartsEndsAndStepsFromDuration() async {
    let log = await runLoop([#"{"id":1,"op":"swipe","x":0.1,"y":0.2,"x2":0.9,"y2":0.8}"#])
    let touches = log.touches
    // 0.3 s / 16 ms = 18.75, rounded to 19 interpolated downs.
    #expect(touches.count == 1 + 19 + 1)
    #expect(touches.first == down(0.1, 0.2))
    #expect(touches[touches.count - 2] == down(0.9, 0.8))
    #expect(touches.last == up(0.9, 0.8))
    #expect(log.all.filter { $0 == .sleep(0.016) }.count == 19)
    let xs = touches.compactMap { event -> Double? in
      if case .touch(let x, _, _, _) = event { x } else { nil }
    }
    #expect(xs == xs.sorted())
  }

  @Test func swipeWithZeroDurationStillMoves() async {
    let log = await runLoop([#"{"id":1,"op":"swipe","x":0,"y":0,"x2":1,"y2":1,"duration":0}"#])
    #expect(log.touches == [down(0, 0), down(1, 1), up(1, 1)])
  }

  @Test func swipeFromBottomEdgeCarriesEdgeOnEveryMessage() async {
    let line = #"{"id":1,"op":"swipe","x":0.5,"y":1,"x2":0.5,"y2":0.5,"edge":"bottom"}"#
    let log = await runLoop([line])
    #expect(!log.touches.isEmpty)
    for case .touch(_, _, _, let edge) in log.touches { #expect(edge == .bottom) }
  }

  @Test func plainKeys() async {
    let log = await runLoop([#"{"id":1,"op":"keys","usages":[4,5]}"#])
    #expect(
      log.all.dropLast() == [
        .key(4, down: true), .key(4, down: false), .key(5, down: true), .key(5, down: false),
      ])
  }

  @Test func shiftIsHeldUntilNextKeyIsReleased() async {
    let log = await runLoop([#"{"id":1,"op":"keys","usages":[225,4,5]}"#])
    #expect(
      log.all.dropLast() == [
        .key(0xE1, down: true), .key(4, down: true), .key(4, down: false),
        .key(0xE1, down: false), .key(5, down: true), .key(5, down: false),
      ])
  }

  @Test func trailingModifierIsReleased() async {
    let log = await runLoop([#"{"id":1,"op":"keys","usages":[4,225]}"#])
    #expect(
      log.all.dropLast() == [
        .key(4, down: true), .key(4, down: false), .key(0xE1, down: true),
        .key(0xE1, down: false),
      ])
  }

  @Test func pasteIsCommandV() async {
    let log = await runLoop([#"{"id":1,"op":"paste"}"#])
    #expect(
      log.all.dropLast() == [
        .key(0xE3, down: true), .key(0x19, down: true), .key(0x19, down: false),
        .key(0xE3, down: false),
      ])
  }

  @Test func homeButton() async {
    let log = await runLoop([#"{"id":1,"op":"button","name":"home"}"#])
    #expect(log.all.dropLast() == [.button(.home, down: true), .button(.home, down: false)])
  }

  @Test func badLinesGetErrorsAndLoopContinues() async {
    let log = await runLoop([
      #"{"id":1,"op":"zoom"}"#,
      "garbage",
      #"{"id":3,"op":"tap","x":2,"y":0}"#,
      #"{"id":4,"op":"button","name":"home"}"#,
    ])
    #expect(
      log.responses == [
        #"{"code":"unknown_op","id":1,"message":"unknown op zoom","ok":false}"#,
        #"{"code":"invalid_request","message":"not a valid request object","ok":false}"#,
        #"{"code":"invalid_argument","id":3,"message":"x must be in 0...1","ok":false}"#,
        #"{"id":4,"ok":true}"#,
      ])
    #expect(log.touches.isEmpty)
  }

  @Test func commandsRunSeriallyInArrivalOrder() async {
    let log = await runLoop([
      #"{"id":1,"op":"tap","x":0.1,"y":0.1}"#,
      #"{"id":2,"op":"tap","x":0.2,"y":0.2}"#,
    ])
    #expect(
      log.all == [
        down(0.1, 0.1), .sleep(0.08), up(0.1, 0.1), .response(#"{"id":1,"ok":true}"#),
        down(0.2, 0.2), .sleep(0.08), up(0.2, 0.2), .response(#"{"id":2,"ok":true}"#),
      ])
  }

  @Test func inputFailureReportsAndLiftsAtOnce() async {
    let result = await runLoop(
      [#"{"id":1,"op":"tap","x":0.5,"y":0.5}"#, #"{"id":2,"op":"button","name":"home"}"#],
      prepare: { _, _, input in input.failingUps.withLock { $0 = 1 } })
    // The failed up is followed by an immediate retry, then the next command runs.
    #expect(result.touches == [down(0.5, 0.5), up(0.5, 0.5), up(0.5, 0.5)])
    #expect(result.responses[0].contains(#""code":"input_failed""#))
    #expect(result.responses[1] == #"{"id":2,"ok":true}"#)
  }

  @Test func stdinCloseLiftsATouchStillDown() async {
    // The tap's up and the immediate lift both fail; closing stdin retries.
    let result = await runLoop(
      [#"{"id":1,"op":"tap","x":0.3,"y":0.4}"#, #"{"id":2,"op":"button","name":"home"}"#],
      prepare: { _, _, input in input.failingUps.withLock { $0 = 2 } })
    #expect(result.touches == [down(0.3, 0.4), up(0.3, 0.4), up(0.3, 0.4), up(0.3, 0.4)])
    #expect(result.all.last == up(0.3, 0.4))
    #expect(result.responses.count == 2)
  }

  @Test func noLiftWhenNoTouchInProgress() async {
    let log = await runLoop([#"{"id":1,"op":"button","name":"home"}"#])
    #expect(log.touches.isEmpty)
  }

  @Test func timeoutLiftsAtLastPositionAndEndsLoop() async {
    let log = EventLog()
    let result = await runLoop(
      [
        #"{"id":1,"op":"tap","x":0.3,"y":0.4,"duration":5}"#,
        #"{"id":2,"op":"button","name":"home"}"#,
      ],
      log: log,
      prepare: { _, clock, _ in clock.setHangAction { clock.fireTimeout() } },
      hangOn: 5)
    #expect(result.touches == [down(0.3, 0.4), up(0.3, 0.4)])
    #expect(result.responses.count == 1)
    #expect(result.responses[0].contains(#""code":"timeout""#))
    // The second command never ran.
    #expect(!result.all.contains(.button(.home, down: true)))
  }

  @Test func terminationMidGestureLiftsAndEndsLoop() async {
    let log = EventLog()
    let result = await runLoop(
      [#"{"id":1,"op":"tap","x":0.6,"y":0.7,"duration":5}"#],
      log: log,
      prepare: { loop, clock, _ in clock.setHangAction { await loop.terminate() } },
      hangOn: 5)
    #expect(result.touches == [down(0.6, 0.7), up(0.6, 0.7)])
    #expect(result.responses.count == 1)
    #expect(result.responses[0].contains(#""code":"cancelled""#))
  }

  @Test func terminateBeforeRunEndsImmediately() async {
    let log = EventLog()
    let input = FakeInput(log: log)
    let loop = CommandLoop(input: input, clock: FakeClock(log: log)) { log.record(.response($0)) }
    await loop.terminate()
    let (stream, continuation) = AsyncStream<String>.makeStream()
    continuation.yield(#"{"id":1,"op":"button","name":"home"}"#)
    continuation.finish()
    await loop.run(lines: stream)
    #expect(log.all.isEmpty)
  }
}
