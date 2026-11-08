import Foundation

private enum Flag {
  static let last: UInt8 = 0
  static let more: UInt8 = 1
  static let ping: UInt8 = 2
  static let pong: UInt8 = 3
}

let channelNonceSize = 8

/// One decrypted message's meaning.
enum ChannelInput: Equatable {
  case frame(Data)
  case ping(Data)
  case pong(Data)
}

/// The encrypted, framed stream after a handshake. Failures are final: the
/// caller must drop the channel.
struct NoiseChannel {
  /// The most frame bytes one transport message carries.
  static let maxPiece = 65535 - noiseTagSize - 1

  private var send: CipherState
  private var recv: CipherState
  private let maxFrame: Int
  private var partial: Data?

  init(send: CipherState, recv: CipherState, maxFrame: Int) {
    self.send = send
    self.recv = recv
    self.maxFrame = maxFrame
  }

  /// Encrypts a frame into its transport messages.
  mutating func seal(frame: Data) throws -> [Data] {
    var out: [Data] = []
    var rest = frame[...]
    while true {
      let piece = rest.prefix(Self.maxPiece)
      rest = rest.dropFirst(piece.count)
      let flag = rest.isEmpty ? Flag.last : Flag.more
      out.append(try send.encrypt(Data([flag]) + piece))
      if flag == Flag.last { return out }
    }
  }

  mutating func sealPing(_ nonce: Data) throws -> Data {
    try send.encrypt(Data([Flag.ping]) + nonce)
  }

  mutating func sealPong(_ nonce: Data) throws -> Data {
    try send.encrypt(Data([Flag.pong]) + nonce)
  }

  /// Decrypts one message; nil while a frame is still incomplete.
  mutating func open(_ message: Data) throws -> ChannelInput? {
    let plain: Data
    do {
      plain = try recv.decrypt(message)
    } catch {
      throw NoiseError.decrypt
    }
    guard let flag = plain.first else { throw NoiseError.frame }
    let body = plain.dropFirst()
    switch flag {
    case Flag.last, Flag.more:
      var frame = partial ?? Data()
      guard frame.count + body.count <= maxFrame else { throw NoiseError.tooLarge }
      frame.append(body)
      if flag == Flag.more {
        partial = frame
        return nil
      }
      partial = nil
      return .frame(frame)
    case Flag.ping, Flag.pong:
      guard body.count == channelNonceSize else { throw NoiseError.frame }
      return flag == Flag.ping ? .ping(Data(body)) : .pong(Data(body))
    default:
      throw NoiseError.frame
    }
  }
}
