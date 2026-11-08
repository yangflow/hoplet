import Foundation
import Testing

@testable import HopletProto

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
  try ProtoCoder.decoder().decode(type, from: Data(json.utf8))
}

/// Replaces every timestamp by its instant, so that "…:00Z" equals "…:00.000Z".
private func normalized(_ value: JSONValue) -> JSONValue {
  switch value {
  case .string(let text):
    if let date = parseWireDate(text), text.contains("T") {
      return .double(date.timeIntervalSince1970)
    }
    return value
  case .array(let values): return .array(values.map(normalized))
  case .object(let values): return .object(values.mapValues(normalized))
  default: return value
  }
}

private func goldenLines() throws -> [String] {
  let root = URL(filePath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    .deletingLastPathComponent().deletingLastPathComponent()
  let text = try String(
    contentsOf: root.appending(path: "proto/testdata/events.jsonl"), encoding: .utf8)
  return text.split(separator: "\n").map(String.init)
}

@Suite struct GoldenEvents {
  @Test func everyLineRoundTrips() throws {
    let lines = try goldenLines()
    #expect(!lines.isEmpty)
    for line in lines {
      let original = try decode(JSONValue.self, line)
      let event = try decode(Event.self, line)
      let again = try ProtoCoder.encoder().encode(event)
      let decoded = try ProtoCoder.decoder().decode(JSONValue.self, from: again)
      #expect(normalized(decoded) == normalized(original), "\(line)")
    }
  }

  @Test func everyPayloadIsKnownAndRoundTrips() throws {
    for line in try goldenLines() {
      let event = try decode(Event.self, line)
      let payload = try event.decodedPayload()
      if case .unknown = payload { Issue.record("unknown payload: \(line)") }
      #expect(try encodedPayload(payload).map(normalized) == normalized(event.payload), "\(line)")
    }
  }
}

/// Encodes a payload through its concrete type.
private func encodedPayload(_ payload: EventPayload) throws -> JSONValue? {
  let mirror = Mirror(reflecting: payload)
  guard let value = mirror.children.first?.value as? any Encodable else { return nil }
  let data = try ProtoCoder.encoder().encode(value)
  return try ProtoCoder.decoder().decode(JSONValue.self, from: data)
}

@Suite struct Tolerance {
  @Test func unknownEnumValueIsKept() throws {
    let mode = try decode(Mode.self, #""from-the-future""#)
    #expect(mode == .unknown("from-the-future"))
    #expect(mode.rawValue == "from-the-future")
    let encoded = try ProtoCoder.encoder().encode(mode)
    #expect(String(decoding: encoded, as: UTF8.self) == #""from-the-future""#)
  }

  @Test func unknownEventDoesNotThrow() throws {
    let event = try decode(
      Event.self,
      #"{"session":"s","seq":1,"ts":"2026-10-31T08:00:00Z","type":"new.kind","v":1,"payload":{"a":1}}"#
    )
    #expect(try event.decodedPayload() == .unknown(type: event.type, v: 1))
  }

  @Test func newerVersionOfKnownEventIsUnknown() throws {
    let event = try decode(
      Event.self,
      #"{"session":"s","seq":1,"ts":"2026-10-31T08:00:00Z","type":"message","v":99,"payload":{}}"#)
    #expect(try event.decodedPayload() == .unknown(type: event.type, v: 99))
  }

  @Test func damagedKnownEventThrows() throws {
    let event = try decode(
      Event.self,
      #"{"session":"s","seq":1,"ts":"2026-10-31T08:00:00Z","type":"message","v":1,"payload":{"text":5}}"#
    )
    #expect(throws: (any Swift.Error).self) { try event.decodedPayload() }
  }

  @Test func missingFieldsTakeZeroValues() throws {
    let event = try decode(Event.self, "{}")
    #expect(event.seq == 0)
    #expect(event.session.rawValue.isEmpty)
    #expect(event.ts == Date.protoZero)
    #expect(event.raw == nil)
    #expect(event.payload == .null)
  }

  @Test func nullArrayDecodesEmpty() throws {
    let host = try decode(Host.self, #"{"routes":null}"#)
    #expect(host.routes.isEmpty)
  }

  @Test func omitemptyMatchesGo() throws {
    let data = try ProtoCoder.encoder().encode(BlobRef())
    #expect(String(decoding: data, as: UTF8.self) == #"{"sha256":"","size":0}"#)
    let event = try ProtoCoder.encoder().encode(Event())
    let text = String(decoding: event, as: UTF8.self)
    #expect(!text.contains("turn"))
    #expect(!text.contains("agent"))
    #expect(!text.contains("raw"))
  }
}

@Suite struct Dates {
  @Test(arguments: [
    ("2026-10-31T08:00:00Z", 1_793_433_600.0),
    ("2026-10-31T08:00:00.5Z", 1_793_433_600.5),
    ("2026-10-31T08:00:00.123456789Z", 1_793_433_600.123456789),
    ("2026-10-31T16:00:00+08:00", 1_793_433_600.0),
    ("0001-01-01T00:00:00Z", -62_135_596_800.0),
  ])
  func parsesGoTimes(text: String, expected: Double) throws {
    let date = try #require(parseWireDate(text))
    #expect(abs(date.timeIntervalSince1970 - expected) < 0.000_01)
  }

  @Test func goZeroTimeRoundTrips() throws {
    let data = try ProtoCoder.encoder().encode(Date.protoZero)
    #expect(String(decoding: data, as: UTF8.self) == #""0001-01-01T00:00:00Z""#)
  }

  @Test func rejectsGarbage() {
    #expect(parseWireDate("yesterday") == nil)
  }

  @Test func encodesMillisecondsUTC() throws {
    let data = try ProtoCoder.encoder().encode(Date(timeIntervalSince1970: 1_793_433_600.5))
    #expect(String(decoding: data, as: UTF8.self) == #""2026-10-31T08:00:00.500Z""#)
  }
}
