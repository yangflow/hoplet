import Foundation
import SimulatorHelperCore

/// Runs the helper and returns its exit status.
func runHelper(arguments: [String]) async -> Int32 {
  guard let mode = HelperMode.parse(arguments) else {
    FileHandle.standardError.write(
      Data("usage: hoplet-simulator-helper --probe | --device <udid>\n".utf8))
    return 2
  }

  // A response to a daemon that went away must not kill the lift.
  signal(SIGPIPE, SIG_IGN)
  let output = LineWriter(fileDescriptor: STDOUT_FILENO)

  guard let developerDirectory = DeveloperDirectory.current() else {
    output.write(ProbeResult(xcode: "", missing: [], message: "no developer directory").line())
    return 1
  }
  let frameworks = PrivateFrameworks(developerDirectory: developerDirectory)
  let probe = ProbeResult.evaluate(
    xcode: DeveloperDirectory.versionLabel(developerDirectory),
    isAvailable: frameworks.isAvailable)

  guard case .device(let udid) = mode, probe.ok else {
    output.write(probe.line())
    return probe.ok ? 0 : 1
  }

  let input: SimulatorDeviceInput
  do {
    input = try SimulatorDeviceInput(
      udid: udid, developerDirectory: developerDirectory, frameworks: frameworks)
  } catch {
    let reason = (error as? DeviceOpenError)?.message ?? "\(error)"
    output.write(probe.failing(reason).line())
    return 1
  }

  let loop = CommandLoop(input: input, write: output.write)
  // Interrupt and hangup are ignored so the end of standard input lifts any
  // touch and exits; only termination has a handler.
  signal(SIGINT, SIG_IGN)
  signal(SIGHUP, SIG_IGN)
  signal(SIGTERM, SIG_IGN)
  let termination = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .global())
  termination.setEventHandler { loop.terminate() }
  termination.resume()

  output.write(probe.line())
  await loop.run(lines: standardInputLines())
  termination.cancel()
  return 0
}

/// Stdin lines, read on a thread of their own because `readLine` blocks.
private func standardInputLines() -> AsyncStream<String> {
  let (lines, continuation) = AsyncStream<String>.makeStream()
  Thread.detachNewThread {
    while let line = readLine() { continuation.yield(line) }
    continuation.finish()
  }
  return lines
}
