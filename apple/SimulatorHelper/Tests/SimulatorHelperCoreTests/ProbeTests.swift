import Foundation
import Testing

@testable import SimulatorHelperCore

@Suite struct ProbeTests {
  @Test func allPresentIsOK() {
    let result = ProbeResult.evaluate(xcode: "27.2") { _ in true }
    #expect(result.ok)
    #expect(result.line() == #"{"missing":[],"ok":true,"xcode":"27.2"}"#)
  }

  @Test func reportsOnlyWhatIsMissing() {
    let result = ProbeResult.evaluate(xcode: "27.2") { requirement in
      switch requirement {
      case .function(PrivateInterface.arbitraryMessageFunction): return false
      case .objcClass(PrivateInterface.hidClientClass): return false
      default: return true
      }
    }
    #expect(!result.ok)
    #expect(result.missing == [PrivateInterface.hidClientClass, "IndigoHIDMessageForHIDArbitrary"])
  }

  @Test func requirementsCoverEveryNamedClassAndFunction() {
    let names = PrivateInterface.requirements.map(\.name)
    for expected in [
      "SimServiceContext",
      "_TtC12SimulatorKit24SimDeviceLegacyHIDClient",
      "IndigoHIDMessageForMouseNSEvent",
      "IndigoHIDMessageForKeyboardArbitrary",
      "IndigoHIDMessageForHIDArbitrary",
    ] {
      #expect(names.contains(expected))
    }
  }

  @Test func methodRequirementsNameTheirClass() {
    let classMethod = Requirement.classMethod(className: "A", selector: "b:")
    let instanceMethod = Requirement.instanceMethod(className: "A", selector: "b:")
    #expect(classMethod.name == "+[A b:]")
    #expect(instanceMethod.name == "-[A b:]")
  }

  @Test func deviceFailureKeepsTheLineShapeAndFails() {
    let result = ProbeResult(xcode: "27.2", missing: []).failing("device is not booted")
    #expect(!result.ok)
    #expect(
      result.line() == #"{"message":"device is not booted","missing":[],"ok":false,"xcode":"27.2"}"#
    )
  }

  @Test func developerDirectoryIsNotEscaped() {
    let result = ProbeResult(xcode: "/a/b", missing: [])
    #expect(result.line().contains(#""xcode":"/a/b""#))
  }

  @Test(arguments: [
    (["--probe"], HelperMode.probe as HelperMode?),
    (["--device", "abc"], HelperMode.device(udid: "abc")),
    ([], nil),
    (["--probe", "x"], nil),
    (["--device"], nil),
    (["--device", ""], nil),
    (["--other"], nil),
  ])
  func parsesArguments(arguments: [String], expected: HelperMode?) {
    #expect(HelperMode.parse(arguments) == expected)
  }

  @Test func lineWriterEndsEachLineWithANewline() throws {
    var fds: [Int32] = [0, 0]
    #expect(pipe(&fds) == 0)
    defer { close(fds[0]) }
    let writer = LineWriter(fileDescriptor: fds[1])
    writer.write("one")
    writer.write("two")
    close(fds[1])
    let data = FileHandle(fileDescriptor: fds[0]).readDataToEndOfFile()
    #expect(String(decoding: data, as: UTF8.self) == "one\ntwo\n")
  }

  @Test func lineWriterSurvivesAClosedReader() {
    signal(SIGPIPE, SIG_IGN)
    var fds: [Int32] = [0, 0]
    #expect(pipe(&fds) == 0)
    close(fds[0])
    defer { close(fds[1]) }
    LineWriter(fileDescriptor: fds[1]).write("gone")
  }
}
