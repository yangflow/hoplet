import Foundation
import ObjectiveC
import SimulatorHelperCore

/// The private frameworks, loaded at runtime. The helper never links them, so
/// a missing or changed Xcode shows up as a failed probe rather than a failed
/// launch.
struct PrivateFrameworks: @unchecked Sendable {
  private static let coreSimulatorPath =
    "/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator"

  /// Handles stay open for the life of the process.
  private let coreSimulator: UnsafeMutableRawPointer?
  private let simulatorKit: UnsafeMutableRawPointer?

  init(developerDirectory: String) {
    coreSimulator = dlopen(Self.coreSimulatorPath, RTLD_NOW)
    let sharedFrameworks = URL(fileURLWithPath: developerDirectory)
      .deletingLastPathComponent()
      .appendingPathComponent("SharedFrameworks/SimulatorKit.framework/SimulatorKit")
    simulatorKit = dlopen(sharedFrameworks.path, RTLD_NOW)
  }

  /// Looks up a C function in SimulatorKit.
  func function(_ name: String) -> UnsafeMutableRawPointer? {
    simulatorKit.flatMap { dlsym($0, name) }
  }

  func isAvailable(_ requirement: Requirement) -> Bool {
    switch requirement {
    case .framework(PrivateInterface.coreSimulator):
      return coreSimulator != nil
    case .framework(PrivateInterface.simulatorKit):
      return simulatorKit != nil
    case .framework:
      return false
    case .objcClass(let name):
      return NSClassFromString(name) != nil
    case .classMethod(let className, let selector):
      return NSClassFromString(className).flatMap {
        class_getClassMethod($0, NSSelectorFromString(selector))
      } != nil
    case .instanceMethod(let className, let selector):
      return NSClassFromString(className).flatMap {
        class_getInstanceMethod($0, NSSelectorFromString(selector))
      } != nil
    case .function(let name):
      return function(name) != nil
    }
  }
}

/// Where Xcode is on this machine, as `xcode-select` reports it.
enum DeveloperDirectory {
  /// Runs `xcode-select -p`; nil when it fails or prints nothing.
  static func current() -> String? {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/bin/xcode-select")
    process.arguments = ["-p"]
    let output = Pipe()
    process.standardOutput = output
    process.standardError = FileHandle.nullDevice
    do {
      try process.run()
    } catch {
      return nil
    }
    let data = output.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    guard process.terminationStatus == 0 else { return nil }
    let path = String(decoding: data, as: UTF8.self)
      .trimmingCharacters(in: .whitespacesAndNewlines)
    return path.isEmpty ? nil : path
  }

  /// The Xcode version next to `path`, or `path` itself when none is readable.
  static func versionLabel(_ path: String) -> String {
    let plist = URL(fileURLWithPath: path)
      .deletingLastPathComponent()
      .appendingPathComponent("version.plist")
    let info = NSDictionary(contentsOf: plist)
    return info?["CFBundleShortVersionString"] as? String ?? path
  }
}
