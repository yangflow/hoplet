import Foundation

/// The value a field takes when it is missing or null on the wire, matching
/// Go's zero values. `isProtoZero` matches what Go's `omitempty` leaves out,
/// so it is false for dates and structs.
public protocol ProtoZero {
  static var protoZero: Self { get }
  var isProtoZero: Bool { get }
}

extension String: ProtoZero {
  public static var protoZero: String { "" }
  public var isProtoZero: Bool { isEmpty }
}

extension Bool: ProtoZero {
  public static var protoZero: Bool { false }
  public var isProtoZero: Bool { !self }
}

extension Int: ProtoZero {
  public static var protoZero: Int { 0 }
  public var isProtoZero: Bool { self == 0 }
}

extension Int64: ProtoZero {
  public static var protoZero: Int64 { 0 }
  public var isProtoZero: Bool { self == 0 }
}

extension UInt16: ProtoZero {
  public static var protoZero: UInt16 { 0 }
  public var isProtoZero: Bool { self == 0 }
}

extension UInt32: ProtoZero {
  public static var protoZero: UInt32 { 0 }
  public var isProtoZero: Bool { self == 0 }
}

extension UInt64: ProtoZero {
  public static var protoZero: UInt64 { 0 }
  public var isProtoZero: Bool { self == 0 }
}

extension Data: ProtoZero {
  public static var protoZero: Data { Data() }
  public var isProtoZero: Bool { isEmpty }
}

extension Array: ProtoZero {
  public static var protoZero: [Element] { [] }
  public var isProtoZero: Bool { isEmpty }
}

extension Date: ProtoZero {
  /// Go's zero time, 0001-01-01T00:00:00Z.
  public static var protoZero: Date { Date(timeIntervalSince1970: -62_135_596_800) }
  public var isProtoZero: Bool { false }
}

extension KeyedDecodingContainer {
  func decodeOrZero<T: Decodable & ProtoZero>(_ type: T.Type, forKey key: Key) throws -> T {
    try decodeIfPresent(type, forKey: key) ?? T.protoZero
  }
}

extension KeyedEncodingContainer {
  mutating func encodeUnlessZero<T: Encodable & ProtoZero>(_ value: T, forKey key: Key) throws {
    if !value.isProtoZero { try encode(value, forKey: key) }
  }
}

/// JSON coders configured for the wire format.
public enum ProtoCoder {
  public static func decoder() -> JSONDecoder {
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .custom { decoder in
      let c = try decoder.singleValueContainer()
      let text = try c.decode(String.self)
      guard let date = parseWireDate(text) else {
        throw DecodingError.dataCorruptedError(in: c, debugDescription: "bad timestamp: \(text)")
      }
      return date
    }
    return decoder
  }

  public static func encoder() -> JSONEncoder {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
    encoder.dateEncodingStrategy = .custom { date, encoder in
      var c = encoder.singleValueContainer()
      try c.encode(date == .protoZero ? goZeroTime : date.formatted(wireFormat))
    }
    return encoder
  }

  private static let wireFormat = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
}

/// Go's zero time on the wire. It is matched exactly because the system date
/// parser and formatter treat dates this old differently from Go.
private let goZeroTime = "0001-01-01T00:00:00Z"

/// Parses RFC 3339 with zero to nine fractional digits, which is what Go's
/// `time.Time` produces.
func parseWireDate(_ text: String) -> Date? {
  if text == goZeroTime { return .protoZero }
  var whole = text
  var fraction = 0.0
  if let dot = text.firstIndex(of: ".") {
    let digitsEnd = text[text.index(after: dot)...].firstIndex { !$0.isNumber } ?? text.endIndex
    guard let value = Double("0" + text[dot..<digitsEnd]) else { return nil }
    fraction = value
    whole = String(text[..<dot] + text[digitsEnd...])
  }
  guard let base = try? Date(whole, strategy: .iso8601) else { return nil }
  return base.addingTimeInterval(fraction)
}

extension APIError: Swift.Error {}
