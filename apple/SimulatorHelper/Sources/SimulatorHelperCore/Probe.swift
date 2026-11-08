import Foundation

/// One thing the helper needs from the private frameworks.
public enum Requirement: Sendable, Equatable {
  case framework(String)
  case objcClass(String)
  case classMethod(className: String, selector: String)
  case instanceMethod(className: String, selector: String)
  case function(String)

  /// The name reported in `missing` when this requirement is not met.
  public var name: String {
    switch self {
    case .framework(let name), .objcClass(let name), .function(let name):
      return name
    case .classMethod(let className, let selector):
      return "+[\(className) \(selector)]"
    case .instanceMethod(let className, let selector):
      return "-[\(className) \(selector)]"
    }
  }
}

/// Names of the private interface the helper depends on.
public enum PrivateInterface {
  public static let coreSimulator = "CoreSimulator"
  public static let simulatorKit = "SimulatorKit"
  public static let serviceContextClass = "SimServiceContext"
  public static let hidClientClass = "_TtC12SimulatorKit24SimDeviceLegacyHIDClient"
  public static let sharedContextSelector = "sharedServiceContextForDeveloperDir:error:"
  public static let defaultDeviceSetSelector = "defaultDeviceSetWithError:"
  public static let hidClientInitSelector = "initWithDevice:error:"
  public static let hidClientSendSelector =
    "sendWithMessage:freeWhenDone:completionQueue:completion:"
  public static let mouseMessageFunction = "IndigoHIDMessageForMouseNSEvent"
  public static let keyboardMessageFunction = "IndigoHIDMessageForKeyboardArbitrary"
  public static let arbitraryMessageFunction = "IndigoHIDMessageForHIDArbitrary"

  /// Everything that must be present before the helper can inject input.
  public static let requirements: [Requirement] = [
    .framework(coreSimulator),
    .framework(simulatorKit),
    .objcClass(serviceContextClass),
    .objcClass(hidClientClass),
    .classMethod(className: serviceContextClass, selector: sharedContextSelector),
    .instanceMethod(className: serviceContextClass, selector: defaultDeviceSetSelector),
    .instanceMethod(className: hidClientClass, selector: hidClientInitSelector),
    .instanceMethod(className: hidClientClass, selector: hidClientSendSelector),
    .function(mouseMessageFunction),
    .function(keyboardMessageFunction),
    .function(arbitraryMessageFunction),
  ]
}

/// The probe report: one JSON line `{ok, xcode, missing, message?}`.
public struct ProbeResult: Sendable, Equatable, Encodable {
  public let ok: Bool
  /// The Xcode version, or the developer directory when no version is readable.
  public let xcode: String
  public let missing: [String]
  /// Why the probe failed, for failures that are not a missing symbol.
  public let message: String?

  public init(xcode: String, missing: [String], message: String? = nil) {
    self.ok = missing.isEmpty && message == nil
    self.xcode = xcode
    self.missing = missing
    self.message = message
  }

  /// Checks every requirement and reports the ones not met.
  public static func evaluate(
    xcode: String,
    requirements: [Requirement] = PrivateInterface.requirements,
    isAvailable: (Requirement) -> Bool
  ) -> ProbeResult {
    ProbeResult(
      xcode: xcode, missing: requirements.filter { !isAvailable($0) }.map(\.name))
  }

  /// The same report, failed for the reason given.
  public func failing(_ message: String) -> ProbeResult {
    ProbeResult(xcode: xcode, missing: missing, message: message)
  }

  /// The report as one JSON line without a trailing newline.
  public func line() -> String {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
    guard let data = try? encoder.encode(self), let text = String(data: data, encoding: .utf8)
    else { return #"{"missing":[],"message":"encode failed","ok":false,"xcode":""}"# }
    return text
  }
}
