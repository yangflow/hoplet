import Testing

@testable import SimulatorHelperCore

@Suite("Protocol decoding")
struct ProtocolTests {
  @Test func tapDefaults() throws {
    let request = try Request.decode(line: #"{"id":1,"op":"tap","x":0.5,"y":0.25}"#).get()
    #expect(request.id == .number(1))
    #expect(request.command == .tap(x: 0.5, y: 0.25, duration: 0.08))
  }

  @Test func swipeDefaults() throws {
    let line = #"{"id":"a","op":"swipe","x":0,"y":1,"x2":1,"y2":0}"#
    let request = try Request.decode(line: line).get()
    #expect(request.id == .string("a"))
    #expect(
      request.command == .swipe(x: 0, y: 1, x2: 1, y2: 0, duration: 0.3, edge: .none))
  }

  @Test func swipeEdgeBottom() throws {
    let line = #"{"id":1,"op":"swipe","x":0.5,"y":1,"x2":0.5,"y2":0.5,"edge":"bottom"}"#
    let request = try Request.decode(line: line).get()
    #expect(
      request.command == .swipe(x: 0.5, y: 1, x2: 0.5, y2: 0.5, duration: 0.3, edge: .bottom))
  }

  @Test func otherCommands() throws {
    #expect(
      try Request.decode(line: #"{"id":1,"op":"keys","usages":[4,225]}"#).get().command
        == .keys([4, 225]))
    #expect(try Request.decode(line: #"{"id":1,"op":"paste"}"#).get().command == .paste)
    #expect(
      try Request.decode(line: #"{"id":1,"op":"button","name":"home"}"#).get().command
        == .button(.home))
  }

  @Test(arguments: [
    #"{"id":1,"op":"tap","x":1.01,"y":0.5}"#,
    #"{"id":1,"op":"tap","x":-0.01,"y":0.5}"#,
    #"{"id":1,"op":"tap","x":0.5}"#,
    #"{"id":1,"op":"tap","x":0.5,"y":0.5,"duration":10.5}"#,
    #"{"id":1,"op":"tap","x":0.5,"y":0.5,"duration":-1}"#,
    #"{"id":1,"op":"tap","x":"left","y":0.5}"#,
    #"{"id":1,"op":"swipe","x":0,"y":0,"x2":2,"y2":0}"#,
    #"{"id":1,"op":"swipe","x":0,"y":0,"x2":1,"y2":1,"edge":"left"}"#,
    #"{"id":1,"op":"keys","usages":[]}"#,
    #"{"id":1,"op":"keys","usages":[0]}"#,
    #"{"id":1,"op":"keys","usages":[232]}"#,
    #"{"id":1,"op":"keys"}"#,
    #"{"id":1,"op":"button","name":"lock"}"#,
    #"{"id":1,"op":"button"}"#,
  ])
  func invalidArguments(line: String) {
    guard case .failure(let failure) = Request.decode(line: line) else {
      Issue.record("expected failure for \(line)")
      return
    }
    #expect(failure.error.code == .invalidArgument)
    #expect(failure.id == .number(1))
  }

  @Test func boundariesAreAccepted() throws {
    let line = #"{"id":1,"op":"tap","x":0,"y":1,"duration":10}"#
    #expect(try Request.decode(line: line).get().command == .tap(x: 0, y: 1, duration: 10))
  }

  @Test func unknownOp() {
    guard case .failure(let failure) = Request.decode(line: #"{"id":7,"op":"zoom"}"#) else {
      Issue.record("expected failure")
      return
    }
    #expect(failure.error.code == .unknownOp)
    #expect(failure.id == .number(7))
  }

  @Test(arguments: ["not json", "", "[1,2]", #"{"id":1}"#])
  func invalidRequest(line: String) {
    guard case .failure(let failure) = Request.decode(line: line) else {
      Issue.record("expected failure")
      return
    }
    #expect(failure.error.code == .invalidRequest)
  }

  @Test func responseLines() {
    #expect(Response.success(.number(3)).line() == #"{"id":3,"ok":true}"#)
    #expect(
      Response.failure(nil, HelperError(.invalidRequest, "bad")).line()
        == #"{"code":"invalid_request","message":"bad","ok":false}"#)
  }
}
