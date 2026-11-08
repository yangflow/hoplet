import Foundation
import SimulatorHelperCore

// Device input is wired up in a later change. Until then the helper only
// parses its arguments and reports that input is unavailable.
let arguments = Array(CommandLine.arguments.dropFirst())
let isProbe = arguments == ["--probe"]
let hasDevice = arguments.count == 2 && arguments[0] == "--device"

guard isProbe || hasDevice else {
  FileHandle.standardError.write(
    Data("usage: hoplet-simulator-helper --probe | --device <udid>\n".utf8))
  exit(2)
}

print(#"{"ok":false,"xcode":"","missing":["device input is not implemented"]}"#)
exit(1)
