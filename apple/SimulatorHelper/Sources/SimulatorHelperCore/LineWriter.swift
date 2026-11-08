import Foundation

/// Writes whole lines to a file descriptor. Each line goes out as one buffer
/// ending in a newline; a closed reader is not an error, the caller ignores
/// SIGPIPE and the line is dropped.
public struct LineWriter: Sendable {
  private let descriptor: Int32

  public init(fileDescriptor: Int32) {
    descriptor = fileDescriptor
  }

  /// Writes `line` followed by a newline.
  public func write(_ line: String) {
    let bytes = Array((line + "\n").utf8)
    bytes.withUnsafeBytes { buffer in
      var offset = 0
      while offset < buffer.count {
        let written = Darwin.write(descriptor, buffer.baseAddress! + offset, buffer.count - offset)
        if written > 0 {
          offset += written
        } else if errno != EINTR {
          return
        }
      }
    }
  }
}
