import Foundation
import ObjectiveC
import SimulatorHelperCore
import Synchronization

/// Why a device could not be opened; the text goes on the first output line.
struct DeviceOpenError: Error {
  let message: String
}

/// Sends HID messages to a booted simulator through its HID client.
///
/// Every stored property is immutable after init. Sends are dispatched off the
/// caller and bounded by `sendTimeout`, so a send that never returns cannot
/// hold up the lift that follows it or the exit of the process.
final class SimulatorDeviceInput: DeviceInput, @unchecked Sendable {
  private typealias MouseMessage =
    @convention(c) (
      UnsafePointer<CGPoint>, UnsafePointer<CGPoint>?, UInt32, UInt32, CGSize, UInt32
    ) -> UnsafeMutableRawPointer?
  private typealias KeyboardMessage =
    @convention(c) (UInt32, UInt32) -> UnsafeMutableRawPointer?
  private typealias ArbitraryMessage =
    @convention(c) (UInt32, UInt32, UInt32, UInt32) -> UnsafeMutableRawPointer?
  private typealias ClientInit =
    @convention(c) (
      NSObject, Selector, NSObject, UnsafeMutablePointer<NSError?>?
    ) -> NSObject?
  private typealias SendCompletion = @convention(block) (NSError?) -> Void
  private typealias ClientSend =
    @convention(c) (
      NSObject, Selector, UnsafeMutableRawPointer, Bool, DispatchQueue, @escaping SendCompletion
    ) -> Void

  /// Longest a single send may take before it counts as failed.
  private static let sendTimeout = 2.0
  private static let pressEvent: UInt32 = 1
  private static let releaseEvent: UInt32 = 2
  private static let digitizerTarget: UInt32 = 0x32
  private static let buttonTarget: UInt32 = 0x33
  private static let bootedState = 3
  /// The Home button as a HID usage: consumer page, Menu usage.
  private static let consumerPage: UInt32 = 0x0C
  private static let menuUsage: UInt32 = 0x40

  private let client: NSObject
  private let sendFunction: ClientSend
  private let sendSelector: Selector
  private let mouseMessage: MouseMessage
  private let keyboardMessage: KeyboardMessage
  private let arbitraryMessage: ArbitraryMessage

  /// Finds the booted device with `udid` and connects a HID client to it.
  /// Call only after the probe has passed.
  init(udid: String, developerDirectory: String, frameworks: PrivateFrameworks) throws {
    guard let mouse = frameworks.function(PrivateInterface.mouseMessageFunction),
      let keyboard = frameworks.function(PrivateInterface.keyboardMessageFunction),
      let arbitrary = frameworks.function(PrivateInterface.arbitraryMessageFunction),
      let clientClass = NSClassFromString(PrivateInterface.hidClientClass)
    else { throw DeviceOpenError(message: "private interface is unavailable") }
    mouseMessage = unsafeBitCast(mouse, to: MouseMessage.self)
    keyboardMessage = unsafeBitCast(keyboard, to: KeyboardMessage.self)
    arbitraryMessage = unsafeBitCast(arbitrary, to: ArbitraryMessage.self)

    let device = try Self.findDevice(udid: udid, developerDirectory: developerDirectory)
    client = try Self.connect(device: device, clientClass: clientClass)

    sendSelector = NSSelectorFromString(PrivateInterface.hidClientSendSelector)
    sendFunction = unsafeBitCast(
      class_getMethodImplementation(clientClass, sendSelector), to: ClientSend.self)
  }

  func touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge) async throws {
    var point = CGPoint(x: x, y: y)
    let event = phase == .down ? Self.pressEvent : Self.releaseEvent
    let unit = CGSize(width: 1, height: 1)
    try await send(
      mouseMessage(&point, nil, Self.digitizerTarget, event, unit, UInt32(edge.rawValue)))
  }

  func key(usage: UInt32, isDown: Bool) async throws {
    try await send(keyboardMessage(usage, isDown ? Self.pressEvent : Self.releaseEvent))
  }

  func button(_ button: HardwareButton, isDown: Bool) async throws {
    let usage: UInt32
    switch button {
    case .home: usage = Self.menuUsage
    }
    let event = isDown ? Self.pressEvent : Self.releaseEvent
    try await send(arbitraryMessage(Self.buttonTarget, Self.consumerPage, usage, event))
  }

  /// A built message on its way to the client, which owns and frees it.
  private struct Message: @unchecked Sendable {
    let pointer: UnsafeMutableRawPointer
  }

  private func send(_ pointer: UnsafeMutableRawPointer?) async throws {
    guard let pointer else { throw HelperError(.inputFailed, "could not build a HID message") }
    let message = Message(pointer: pointer)
    try await withCheckedThrowingContinuation { (continuation: SendWait) in
      let wait = SendOnce(continuation)
      let queue = DispatchQueue.global()
      queue.asyncAfter(deadline: .now() + Self.sendTimeout) {
        wait.finish(HelperError(.inputFailed, "HID send timed out"))
      }
      // Off the caller: a send that never returns must not block the caller.
      queue.async {
        self.sendFunction(self.client, self.sendSelector, message.pointer, true, queue) { error in
          wait.finish(error.map { HelperError(.inputFailed, $0.localizedDescription) })
        }
      }
    }
  }

  private typealias SendWait = CheckedContinuation<Void, any Error>

  /// Resumes the send's caller once, on completion or timeout, whichever is first.
  private final class SendOnce: Sendable {
    private let continuation: Mutex<SendWait?>

    init(_ continuation: SendWait) {
      self.continuation = Mutex(continuation)
    }

    func finish(_ error: HelperError?) {
      guard let continuation = self.continuation.withLock({ $0.take() }) else { return }
      if let error {
        continuation.resume(throwing: error)
      } else {
        continuation.resume()
      }
    }
  }

  private static func findDevice(udid: String, developerDirectory: String) throws -> NSObject {
    let contextSelector = NSSelectorFromString(PrivateInterface.sharedContextSelector)
    let setSelector = NSSelectorFromString(PrivateInterface.defaultDeviceSetSelector)
    guard let contextClass = NSClassFromString(PrivateInterface.serviceContextClass),
      let context = (contextClass as AnyObject)
        .perform(contextSelector, with: developerDirectory, with: nil)?
        .takeUnretainedValue(),
      let deviceSet = context.perform(setSelector, with: nil)?.takeUnretainedValue(),
      let devices = deviceSet.value(forKey: "devices") as? [NSObject]
    else { throw DeviceOpenError(message: "could not read the simulator device set") }

    let wanted = UUID(uuidString: udid)
    let device = devices.first { candidate in
      (candidate.value(forKey: "UDID") as? NSUUID).map { $0 as UUID == wanted } ?? false
    }
    guard let device else { throw DeviceOpenError(message: "no simulator with UDID \(udid)") }
    guard (device.value(forKey: "state") as? Int) == bootedState else {
      throw DeviceOpenError(message: "simulator \(udid) is not booted")
    }
    return device
  }

  private static func connect(device: NSObject, clientClass: AnyClass) throws -> NSObject {
    let selector = NSSelectorFromString(PrivateInterface.hidClientInitSelector)
    let initialize = unsafeBitCast(
      class_getMethodImplementation(clientClass, selector), to: ClientInit.self)
    guard
      let allocated = (clientClass as AnyObject).perform(NSSelectorFromString("alloc"))?
        .takeUnretainedValue() as? NSObject
    else { throw DeviceOpenError(message: "could not allocate the HID client") }
    var error: NSError?
    guard let client = initialize(allocated, selector, device, &error) else {
      let reason = error?.localizedDescription ?? "unknown error"
      throw DeviceOpenError(message: "could not connect to the simulator: \(reason)")
    }
    return client
  }
}
