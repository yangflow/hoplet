import CryptoKit
import Foundation
import HopletProto
import Testing

@testable import HopletClient

private func vector<T: Decodable>(_ name: String, as type: T.Type = T.self) throws -> T {
  let root = URL(filePath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    .deletingLastPathComponent().deletingLastPathComponent()
  let data = try Data(contentsOf: root.appending(path: "testdata/vectors/\(name)"))
  return try ProtoCoder.decoder().decode(type, from: data)
}

func hex(_ text: String) -> Data {
  var out = Data()
  var index = text.startIndex
  while index < text.endIndex {
    let next = text.index(index, offsetBy: 2)
    out.append(UInt8(text[index..<next], radix: 16)!)
    index = next
  }
  return out
}

private func key(_ text: String) throws -> X25519Key {
  try X25519Key(rawRepresentation: hex(text))
}

/// A vector writes "no secret" as an empty or missing field.
private func secret(_ text: String?) -> Data? {
  guard let text, !text.isEmpty else { return nil }
  return hex(text)
}

private func framePattern(_ size: Int) -> Data {
  Data((0..<size).map { UInt8($0 % 251) })
}

private struct Party: Decodable {
  var staticPrivate: String
  var staticPublic: String
  var ephemeralPrivate: String

  enum CodingKeys: String, CodingKey {
    case staticPrivate = "static_private"
    case staticPublic = "static_public"
    case ephemeralPrivate = "ephemeral_private"
  }
}

private struct Step: Decodable {
  var from: String
  var kind: String
  var size: Int?
  var nonce: String?
  var messages: [String]
}

private struct SessionVector: Decodable {
  var kind: UInt8
  var initiator: Party
  var responder: Party
  var psk: String?
  var payload: String?
  var messages: [String]
  var steps: [Step]
}

/// Runs a vector's handshake as the initiator, checking every byte.
private func handshake(_ v: SessionVector, maxFrame: Int = 64 << 20) throws -> NoiseChannel {
  #expect(
    try key(v.initiator.staticPrivate).publicKey.rawRepresentation
      == hex(v.initiator.staticPublic))
  var hs = try NoiseHandshake(
    kind: NoiseKind(rawValue: v.kind)!, identity: key(v.initiator.staticPrivate),
    peerStatic: hex(v.responder.staticPublic), psk: secret(v.psk),
    payload: hex(v.payload ?? ""), ephemeral: key(v.initiator.ephemeralPrivate))
  #expect(try hs.writeFirst() == hex(v.messages[0]))
  let keys = try hs.readResponse(hex(v.messages[1]))
  return NoiseChannel(send: keys.send, recv: keys.recv, maxFrame: maxFrame)
}

/// Replays steps: the initiator's are re-encrypted, the responder's opened.
private func replay(_ v: SessionVector) throws {
  var channel = try handshake(v)
  for step in v.steps {
    let messages = step.messages.map(hex)
    if step.from == "initiator" {
      switch step.kind {
      case "frame": #expect(try channel.seal(frame: framePattern(step.size!)) == messages)
      case "ping": #expect(try channel.sealPing(hex(step.nonce!)) == messages[0])
      default: #expect(try channel.sealPong(hex(step.nonce!)) == messages[0])
      }
    } else {
      var last: ChannelInput?
      for message in messages { last = try channel.open(message) }
      switch step.kind {
      case "frame": #expect(last == .frame(framePattern(step.size!)))
      case "ping": #expect(last == .ping(hex(step.nonce!)))
      default: #expect(last == .pong(hex(step.nonce!)))
      }
    }
  }
}

private struct HandshakeNegative: Decodable {
  var name: String
  var kind: UInt8
  var initiatorStaticPrivate: String
  var initiatorEphemeralPrivate: String
  var responderStaticPublic: String
  var psk: String?
  var firstMessage: String
  var response: String

  enum CodingKeys: String, CodingKey {
    case name
    case kind
    case initiatorStaticPrivate = "initiator_static_private"
    case initiatorEphemeralPrivate = "initiator_ephemeral_private"
    case responderStaticPublic = "responder_static_public"
    case psk
    case firstMessage = "first_message"
    case response
  }
}

private struct ChannelNegative: Decodable {
  var name: String
  var maxFrame: Int?
  var messages: [String]
  var framesBeforeError: Int
  var error: String

  enum CodingKeys: String, CodingKey {
    case name
    case maxFrame = "max_frame"
    case messages
    case framesBeforeError = "frames_before_error"
    case error
  }
}

private struct Negatives: Decodable {
  var handshake: [HandshakeNegative]
  var channel: [ChannelNegative]
}

@Suite struct NoiseVectors {
  @Test(arguments: ["noise-ik.json", "noise-ikpsk1.json", "channel.json"])
  func sessionReplays(file: String) throws {
    try replay(try vector(file))
  }

  @Test func badResponsesAreRejected() throws {
    let negatives: Negatives = try vector("noise-negative.json")
    #expect(negatives.handshake.count == 6)
    for c in negatives.handshake {
      let pairing = c.kind == NoiseKind.pair.rawValue
      var hs = try NoiseHandshake(
        kind: NoiseKind(rawValue: c.kind)!, identity: key(c.initiatorStaticPrivate),
        peerStatic: hex(c.responderStaticPublic), psk: secret(c.psk),
        payload: pairing ? Data("测试 iPhone".utf8) : Data(),
        ephemeral: key(c.initiatorEphemeralPrivate))
      #expect(try hs.writeFirst() == hex(c.firstMessage), "\(c.name)")
      #expect(throws: NoiseError.handshake, "\(c.name)") {
        _ = try hs.readResponse(hex(c.response))
      }
    }
  }

  @Test func badChannelMessagesAreRejected() throws {
    let negatives: Negatives = try vector("noise-negative.json")
    let session: SessionVector = try vector("channel.json")
    let expected: [String: NoiseError] = [
      "decrypt": .decrypt, "frame": .frame, "too_large": .tooLarge,
    ]
    #expect(negatives.channel.count == 7)
    for c in negatives.channel {
      var channel = try handshake(session, maxFrame: c.maxFrame ?? 64 << 20)
      var frames = 0
      var failure: NoiseError?
      for message in c.messages {
        do {
          if case .frame = try channel.open(hex(message)) { frames += 1 }
        } catch {
          failure = error as? NoiseError
          break
        }
      }
      #expect(failure == expected[c.error], "\(c.name)")
      #expect(frames == c.framesBeforeError, "\(c.name)")
    }
  }
}

private struct PairingVectors: Decodable {
  struct Valid: Decodable {
    var name: String
    var code: String
    var id: String
    var key: String
    var host: String
    var routes: [String]
    var psk: String
    var expiresUnix: Int64

    enum CodingKeys: String, CodingKey {
      case name, code, id, key, host, routes, psk
      case expiresUnix = "expires_unix"
    }
  }
  struct Invalid: Decodable {
    var name: String
    var code: String
  }
  struct HostID: Decodable {
    var `public`: String
    var id: String
  }
  var valid: [Valid]
  var invalid: [Invalid]
  var hostIds: [HostID]

  enum CodingKeys: String, CodingKey {
    case valid
    case invalid
    case hostIds = "host_ids"
  }
}

@Suite struct PairingReplay {
  @Test func codesAndHostIDs() throws {
    let v: PairingVectors = try vector("pairing.json")
    #expect(v.valid.count == 2 && v.invalid.count == 12)
    for c in v.valid {
      let code = try #require(PairingCode(c.code), "\(c.name)")
      #expect(code.id == c.id && code.key == hex(c.key) && code.name == c.host)
      #expect(code.routes == c.routes && code.psk == hex(c.psk))
      #expect(code.expires == Date(timeIntervalSince1970: TimeInterval(c.expiresUnix)))
    }
    for c in v.invalid { #expect(PairingCode(c.code) == nil, "\(c.name)") }
    for c in v.hostIds { #expect(hostID(of: hex(c.public)) == c.id) }
  }
}

private struct ResumeVectors: Decodable {
  struct SessionCase: Decodable {
    struct Want: Decodable {
      var snapshot: Bool
      var seqs: [UInt64]
      var cursor: UInt64
      var more: Bool
    }
    var name: String
    var have: [UInt64]
    var afterSeq: UInt64
    var items: [SessionItem]
    var want: Want

    enum CodingKeys: String, CodingKey {
      case name
      case have
      case afterSeq = "after_seq"
      case items
      case want
    }
  }
  struct CatalogCase: Decodable {
    var name: String
    var have: [SessionID]
    var item: CatalogItem
    var want: [SessionID]
    var wantVersion: UInt64

    enum CodingKeys: String, CodingKey {
      case name
      case have
      case item
      case want
      case wantVersion = "want_version"
    }
  }
  var sessions: [SessionCase]
  var catalog: [CatalogCase]
}

@Suite struct ResumeReplay {
  /// A log that already holds `seqs`, as an earlier connection left it.
  private func seeded(_ seqs: [UInt64]) -> SessionLog {
    var log = SessionLog()
    log.apply(SessionItem(events: seqs.map { Event(seq: $0) }))
    return log
  }

  @Test func sessions() throws {
    let v: ResumeVectors = try vector("resume.json")
    #expect(v.sessions.count == 5)
    for c in v.sessions {
      var log = seeded(c.have)
      #expect(log.cursor == c.afterSeq, "\(c.name)")
      for item in c.items { log.apply(item) }
      #expect(log.events.map(\.seq) == c.want.seqs, "\(c.name)")
      #expect(log.cursor == c.want.cursor && log.hasOlder == c.want.more, "\(c.name)")
      #expect((log.session != nil) == c.want.snapshot, "\(c.name)")
    }
  }

  @Test func catalog() throws {
    let v: ResumeVectors = try vector("resume.json")
    #expect(v.catalog.count == 3)
    for c in v.catalog {
      var view = CatalogView()
      view.apply(CatalogItem(sessions: c.have.map { Session(id: $0) }))
      view.apply(c.item)
      #expect(Set(view.sessions.keys) == Set(c.want), "\(c.name)")
      #expect(view.version == c.wantVersion, "\(c.name)")
    }
  }

  @Test func repeatedEventsAreIgnored() {
    var log = seeded([1, 2])
    log.apply(SessionItem(events: [Event(seq: 2), Event(seq: 3)]))
    #expect(log.events.map(\.seq) == [1, 2, 3] && log.cursor == 3)
  }
}
