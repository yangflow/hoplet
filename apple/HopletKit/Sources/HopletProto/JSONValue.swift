import Foundation

/// Arbitrary JSON, for fields whose shape depends on another field.
public enum JSONValue: Codable, Hashable, Sendable, ProtoZero {
  case null
  case bool(Bool)
  case int(Int64)
  case uint(UInt64)
  case double(Double)
  case string(String)
  case array([JSONValue])
  case object([String: JSONValue])

  public init(from decoder: any Decoder) throws {
    if var array = try? decoder.unkeyedContainer() {
      var values: [JSONValue] = []
      while !array.isAtEnd { values.append(try array.decode(JSONValue.self)) }
      self = .array(values)
      return
    }
    if let object = try? decoder.container(keyedBy: AnyKey.self) {
      var values: [String: JSONValue] = [:]
      for key in object.allKeys {
        values[key.stringValue] = try object.decode(JSONValue.self, forKey: key)
      }
      self = .object(values)
      return
    }
    let c = try decoder.singleValueContainer()
    if c.decodeNil() {
      self = .null
    } else if let value = try? c.decode(Bool.self) {
      self = .bool(value)
    } else if let value = try? c.decode(Int64.self) {
      self = .int(value)
    } else if let value = try? c.decode(UInt64.self) {
      self = .uint(value)
    } else if let value = try? c.decode(Double.self) {
      self = .double(value)
    } else {
      self = .string(try c.decode(String.self))
    }
  }

  public func encode(to encoder: any Encoder) throws {
    switch self {
    case .array(let values):
      var c = encoder.unkeyedContainer()
      for value in values { try c.encode(value) }
    case .object(let values):
      var c = encoder.container(keyedBy: AnyKey.self)
      for (key, value) in values { try c.encode(value, forKey: AnyKey(key)) }
    case .null:
      var c = encoder.singleValueContainer()
      try c.encodeNil()
    case .bool(let value):
      var c = encoder.singleValueContainer()
      try c.encode(value)
    case .int(let value):
      var c = encoder.singleValueContainer()
      try c.encode(value)
    case .uint(let value):
      var c = encoder.singleValueContainer()
      try c.encode(value)
    case .double(let value):
      var c = encoder.singleValueContainer()
      try c.encode(value)
    case .string(let value):
      var c = encoder.singleValueContainer()
      try c.encode(value)
    }
  }

  /// Decodes this value as `type`.
  public func decode<T: Decodable>(_ type: T.Type) throws -> T {
    try ProtoCoder.decoder().decode(type, from: try ProtoCoder.encoder().encode(self))
  }

  public static var protoZero: Self { .null }
  public var isProtoZero: Bool { self == .null }
}

private struct AnyKey: CodingKey {
  let stringValue: String
  var intValue: Int? { nil }

  init(_ value: String) { stringValue = value }
  init?(stringValue: String) { self.stringValue = stringValue }
  init?(intValue: Int) { nil }
}
