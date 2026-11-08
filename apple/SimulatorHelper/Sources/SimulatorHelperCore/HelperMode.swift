/// How the helper was started.
public enum HelperMode: Sendable, Equatable {
  case probe
  case device(udid: String)

  /// Parses the arguments after the program name; nil when they are invalid.
  public static func parse(_ arguments: [String]) -> HelperMode? {
    switch arguments.count {
    case 1 where arguments[0] == "--probe":
      return .probe
    case 2 where arguments[0] == "--device" && !arguments[1].isEmpty:
      return .device(udid: arguments[1])
    default:
      return nil
    }
  }
}
