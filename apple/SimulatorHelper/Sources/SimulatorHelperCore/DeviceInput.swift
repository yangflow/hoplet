/// Whether a touch event presses or lifts the finger. Moves are repeated
/// `down` events at a new position.
public enum TouchPhase: Sendable, Equatable {
  case down
  case up
}

/// Screen edge a gesture starts from. The raw value is the edge parameter
/// carried by every message of the gesture.
public enum TouchEdge: Int, Sendable, Equatable {
  case none = 0
  case bottom = 3
}

/// Hardware buttons the helper can press.
public enum HardwareButton: Sendable, Equatable {
  case home
}

/// HID keyboard usages (usage page 0x07) the helper itself needs.
public enum HIDUsage {
  public static let v: UInt32 = 0x19
  public static let leftShift: UInt32 = 0xE1
  public static let leftCommand: UInt32 = 0xE3

  /// Modifier keys occupy 0xE0...0xE7.
  public static func isModifier(_ usage: UInt32) -> Bool {
    (0xE0...0xE7).contains(usage)
  }
}

/// The device-facing side of the helper. The production implementation talks
/// to private frameworks; tests use a recording fake.
///
/// Implementations must be safe to call concurrently: when a call is stuck,
/// the loop abandons it and sends the lift from another task while the stuck
/// call may still be running.
public protocol DeviceInput: Sendable {
  /// Sends one touch event at a ratio position (0...1, origin top-left).
  func touch(x: Double, y: Double, phase: TouchPhase, edge: TouchEdge) async throws

  /// Sends one keyboard event for a HID usage.
  func key(usage: UInt32, isDown: Bool) async throws

  /// Sends one hardware button event.
  func button(_ button: HardwareButton, isDown: Bool) async throws
}
