import Foundation

/// Request identifier, echoed unchanged in the response.
public enum RequestID: Sendable, Equatable, Codable {
  case number(Int)
  case string(String)

  public init(from decoder: any Decoder) throws {
    let container = try decoder.singleValueContainer()
    if let number = try? container.decode(Int.self) {
      self = .number(number)
    } else {
      self = .string(try container.decode(String.self))
    }
  }

  public func encode(to encoder: any Encoder) throws {
    var container = encoder.singleValueContainer()
    switch self {
    case .number(let value): try container.encode(value)
    case .string(let value): try container.encode(value)
    }
  }
}

/// Error codes of a failed response.
public enum ErrorCode: String, Sendable {
  case invalidRequest = "invalid_request"
  case invalidArgument = "invalid_argument"
  case unknownOp = "unknown_op"
  case timeout
  case cancelled
  case inputFailed = "input_failed"
}

/// A command failure that maps to a failed response.
public struct HelperError: Error, Sendable, Equatable {
  public let code: ErrorCode
  public let message: String

  public init(_ code: ErrorCode, _ message: String) {
    self.code = code
    self.message = message
  }
}

/// A validated command.
public enum Command: Sendable, Equatable {
  case tap(x: Double, y: Double, duration: Double)
  case swipe(x: Double, y: Double, x2: Double, y2: Double, duration: Double, edge: TouchEdge)
  case keys([UInt32])
  case paste
  case button(HardwareButton)

  public static let defaultTapDuration = 0.08
  public static let defaultSwipeDuration = 0.3
  public static let maxDuration = 10.0
}

/// One decoded request line.
public struct Request: Sendable, Equatable {
  public let id: RequestID?
  public let command: Command
}

/// One response line: `{id, ok, code?, message?}`.
public struct Response: Sendable, Equatable, Encodable {
  public let id: RequestID?
  public let ok: Bool
  public let code: String?
  public let message: String?

  public static func success(_ id: RequestID?) -> Response {
    Response(id: id, ok: true, code: nil, message: nil)
  }

  public static func failure(_ id: RequestID?, _ error: HelperError) -> Response {
    Response(id: id, ok: false, code: error.code.rawValue, message: error.message)
  }

  /// The response as one JSON line without a trailing newline.
  public func line() -> String {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    guard let data = try? encoder.encode(self), let text = String(data: data, encoding: .utf8)
    else { return #"{"ok":false,"code":"input_failed","message":"encode failed"}"# }
    return text
  }
}

/// A failed decode, with whatever id could be read from the line.
public struct RequestFailure: Error, Sendable, Equatable {
  public let id: RequestID?
  public let error: HelperError
}

private struct IDField: Decodable {
  let id: RequestID?
}

private struct OpField: Decodable {
  let op: String?
}

private struct Fields: Decodable {
  let x: Double?
  let y: Double?
  let x2: Double?
  let y2: Double?
  let duration: Double?
  let edge: String?
  let usages: [Int]?
  let name: String?
}

extension Request {
  /// Decodes and validates one request line.
  public static func decode(line: String) -> Result<Request, RequestFailure> {
    let data = Data(line.utf8)
    let decoder = JSONDecoder()
    let id: RequestID?
    do {
      id = try decoder.decode(IDField.self, from: data).id
    } catch {
      // Not a JSON object, or the id has an unusable type.
      return .failure(
        RequestFailure(id: nil, error: HelperError(.invalidRequest, "not a valid request object")))
    }
    guard let op = (try? decoder.decode(OpField.self, from: data))?.op else {
      return .failure(
        RequestFailure(id: id, error: HelperError(.invalidRequest, "missing or invalid op")))
    }
    let fields: Fields
    do {
      fields = try decoder.decode(Fields.self, from: data)
    } catch {
      return .failure(
        RequestFailure(
          id: id, error: HelperError(.invalidArgument, "fields have the wrong type")))
    }
    do {
      let command = try Command.validated(op: op, fields: fields)
      return .success(Request(id: id, command: command))
    } catch let error as HelperError {
      return .failure(RequestFailure(id: id, error: error))
    } catch {
      return .failure(
        RequestFailure(id: id, error: HelperError(.invalidRequest, "\(error)")))
    }
  }
}

extension Command {
  fileprivate static func validated(op: String, fields: Fields) throws -> Command {
    switch op {
    case "tap":
      return .tap(
        x: try ratio(fields.x, "x"),
        y: try ratio(fields.y, "y"),
        duration: try duration(fields.duration, default: defaultTapDuration))
    case "swipe":
      return .swipe(
        x: try ratio(fields.x, "x"),
        y: try ratio(fields.y, "y"),
        x2: try ratio(fields.x2, "x2"),
        y2: try ratio(fields.y2, "y2"),
        duration: try duration(fields.duration, default: defaultSwipeDuration),
        edge: try edge(fields.edge))
    case "keys":
      guard let usages = fields.usages, !usages.isEmpty else {
        throw HelperError(.invalidArgument, "usages must be a non-empty list")
      }
      guard usages.allSatisfy({ (1...0xE7).contains($0) }) else {
        throw HelperError(.invalidArgument, "usages must be in 1...231")
      }
      return .keys(usages.map { UInt32($0) })
    case "paste":
      return .paste
    case "button":
      guard fields.name == "home" else {
        throw HelperError(.invalidArgument, "name must be home")
      }
      return .button(.home)
    default:
      throw HelperError(.unknownOp, "unknown op \(op)")
    }
  }

  private static func ratio(_ value: Double?, _ name: String) throws -> Double {
    guard let value else { throw HelperError(.invalidArgument, "\(name) is required") }
    guard (0...1).contains(value) else {
      throw HelperError(.invalidArgument, "\(name) must be in 0...1")
    }
    return value
  }

  private static func duration(_ value: Double?, default fallback: Double) throws -> Double {
    guard let value else { return fallback }
    guard (0...maxDuration).contains(value) else {
      throw HelperError(.invalidArgument, "duration must be in 0...10 seconds")
    }
    return value
  }

  private static func edge(_ value: String?) throws -> TouchEdge {
    switch value {
    case nil: return .none
    case "bottom": return .bottom
    default: throw HelperError(.invalidArgument, "edge must be bottom or omitted")
    }
  }
}
