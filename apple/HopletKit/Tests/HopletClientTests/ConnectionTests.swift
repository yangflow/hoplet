import CryptoKit
import Foundation
import HopletProto
import Testing

@testable import HopletClient

/// Messages waiting for one reader.
private actor Mailbox {
  private var messages: [Data] = []
  private var waiter: CheckedContinuation<Data, any Swift.Error>?
  private var closed = false

  func put(_ message: Data) {
    if let waiter {
      self.waiter = nil
      waiter.resume(returning: message)
    } else {
      messages.append(message)
    }
  }

  func take() async throws -> Data {
    if !messages.isEmpty { return messages.removeFirst() }
    if closed { throw ConnectionError.closed }
    return try await withCheckedThrowingContinuation { waiter = $0 }
  }

  func close() {
    closed = true
    waiter?.resume(throwing: ConnectionError.closed)
    waiter = nil
  }
}

/// One side of an in-memory message pipe.
private struct Pipe: MessageTransport {
  let inbox: Mailbox
  let outbox: Mailbox

  static func pair() -> (Pipe, Pipe) {
    let a = Mailbox()
    let b = Mailbox()
    return (Pipe(inbox: a, outbox: b), Pipe(inbox: b, outbox: a))
  }

  func send(_ message: Data) async throws { await outbox.put(message) }
  func receive() async throws -> Data { try await inbox.take() }

  func close() async {
    await inbox.close()
    await outbox.close()
  }
}

private func hmacKey(_ key: Data, _ parts: Data...) -> Data {
  var mac = HMAC<SHA256>(key: SymmetricKey(data: key))
  for part in parts { mac.update(data: part) }
  return Data(mac.finalize())
}

/// A minimal host: the responder half of a daily handshake, then frames.
private actor Host {
  let pipe: Pipe
  let identity = X25519Key()
  var channel: NoiseChannel?
  var answersPings = true
  var received: [ClientFrame] = []

  init(pipe: Pipe) { self.pipe = pipe }

  func accept(initiatorStatic: X25519Key) async throws {
    let first = try await pipe.receive()
    #expect(first.first == NoiseKind.daily.rawValue)
    var symmetric = Responder(hostKey: identity, kind: .daily)
    let response = try symmetric.respond(to: Data(first.dropFirst()))
    try await pipe.send(response.message)
    channel = NoiseChannel(send: response.send, recv: response.recv, maxFrame: 1 << 26)
  }

  func setAnswersPings(_ value: Bool) { answersPings = value }

  /// Reads until a client frame arrives, answering pings on the way.
  func read() async throws -> ClientFrame {
    while true {
      let message = try await pipe.receive()
      switch try channel!.open(message) {
      case .frame(let data):
        let frame = try ProtoCoder.decoder().decode(ClientFrame.self, from: data)
        received.append(frame)
        return frame
      case .ping(let nonce):
        if answersPings { try await pipe.send(try channel!.sealPong(nonce)) }
      default: break
      }
    }
  }

  func write(_ frame: ServerFrame) async throws {
    for message in try channel!.seal(frame: try ProtoCoder.encoder().encode(frame)) {
      try await pipe.send(message)
    }
  }

  func json<T: Encodable>(_ value: T) throws -> JSONValue {
    try ProtoCoder.decoder().decode(JSONValue.self, from: try ProtoCoder.encoder().encode(value))
  }

  func serveHello(hostID id: String) async throws {
    let frame = try await read()
    #expect(frame.method == "hello")
    try await write(ServerFrame(id: frame.id, result: try json(HelloResult(hostId: id))))
  }
}

/// The responder's side of IK, enough for connection tests.
private struct Responder {
  let hostKey: X25519Key
  let kind: NoiseKind

  mutating func respond(to message: Data) throws -> (
    message: Data, send: CipherState, recv: CipherState
  ) {
    var state = TestSymmetric(name: kind.protocolName)
    state.mixHash(Data("hoplet/1".utf8) + Data([kind.rawValue]))
    state.mixHash(hostKey.publicKey.rawRepresentation)
    let re = Data(message.prefix(32))
    state.mixHash(re)
    state.mixKey(try dh(hostKey, re))
    let peer = try state.decryptAndHash(Data(message.dropFirst(32).prefix(48)))
    state.mixKey(try dh(hostKey, peer))
    _ = try state.decryptAndHash(Data(message.dropFirst(80)))
    let ephemeral = X25519Key()
    let e = ephemeral.publicKey.rawRepresentation
    state.mixHash(e)
    state.mixKey(try dh(ephemeral, re))
    state.mixKey(try dh(ephemeral, peer))
    let tail = try state.encryptAndHash(Data())
    let (toResponder, toInitiator) = state.split()
    return (e + tail, toInitiator, toResponder)
  }

  private func dh(_ key: X25519Key, _ peer: Data) throws -> Data {
    let shared = try key.sharedSecretFromKeyAgreement(
      with: Curve25519.KeyAgreement.PublicKey(rawRepresentation: peer))
    return shared.withUnsafeBytes { Data($0) }
  }
}

private struct TestSymmetric {
  var ck: Data
  var h: Data
  var cipher = CipherState()

  init(name: String) {
    let raw = Data(name.utf8)
    h = raw.count <= 32 ? raw + Data(count: 32 - raw.count) : Data(SHA256.hash(data: raw))
    ck = h
  }

  mutating func mixHash(_ d: Data) { h = Data(SHA256.hash(data: h + d)) }

  mutating func mixKey(_ input: Data) {
    let temp = hmacKey(ck, input)
    ck = hmacKey(temp, Data([1]))
    cipher = CipherState(key: SymmetricKey(data: hmacKey(temp, ck, Data([2]))))
  }

  mutating func encryptAndHash(_ p: Data) throws -> Data {
    let c = try cipher.encrypt(p, ad: h)
    mixHash(c)
    return c
  }

  mutating func decryptAndHash(_ c: Data) throws -> Data {
    let p = try cipher.decrypt(c, ad: h)
    mixHash(c)
    return p
  }

  func split() -> (CipherState, CipherState) {
    let temp = hmacKey(ck, Data())
    let one = hmacKey(temp, Data([1]))
    let two = hmacKey(temp, one, Data([2]))
    return (CipherState(key: SymmetricKey(data: one)), CipherState(key: SymmetricKey(data: two)))
  }
}

private func connected(
  configure: (inout ConnectOptions) -> Void = { _ in },
  reportedID: String? = nil
) async throws -> (Connection, Host) {
  let (client, server) = Pipe.pair()
  let host = Host(pipe: server)
  let me = X25519Key()
  let hostKey = host.identity.publicKey.rawRepresentation
  let id = reportedID ?? hostID(of: hostKey)!
  var options = ConnectOptions(identity: me, hostKey: hostKey)
  options.pingInterval = .seconds(3600)
  configure(&options)
  let finalOptions = options
  async let connection = Connection.connect(transport: client, options: finalOptions)
  try await host.accept(initiatorStatic: me)
  try await host.serveHello(hostID: id)
  return (try await connection, host)
}

@Suite struct ConnectionBehavior {
  @Test func helloRecordsTheHost() async throws {
    let (connection, _) = try await connected()
    #expect(await !connection.hello.hostId.isEmpty)
    await connection.close()
  }

  @Test func aDifferentHostIsRefused() async throws {
    await #expect(throws: ConnectionError.wrongHost("someone-else")) {
      _ = try await connected(reportedID: "someone-else")
    }
  }

  @Test func callsMatchResponsesByID() async throws {
    let (connection, host) = try await connected()
    let one = Task {
      try await connection.call("a", params: CatalogParams(), as: HelloResult.self)
    }
    let first = try await host.read()
    let two = Task {
      try await connection.call("b", params: CatalogParams(), as: HelloResult.self)
    }
    let second = try await host.read()
    try await host.write(
      ServerFrame(id: second.id, result: try await host.json(HelloResult(host: "two"))))
    try await host.write(
      ServerFrame(id: first.id, result: try await host.json(HelloResult(host: "one"))))
    #expect(try await one.value.host == "one")
    #expect(try await two.value.host == "two")
    await connection.close()
  }

  @Test func apiErrorsAreThrown() async throws {
    let (connection, host) = try await connected()
    let result = Task {
      try await connection.call("a", params: CatalogParams(), as: HelloResult.self)
    }
    let frame = try await host.read()
    let failure = APIError(code: .notFound, message: "no")
    try await host.write(ServerFrame(id: frame.id, error: failure))
    await #expect(throws: failure) { _ = try await result.value }
    await connection.close()
  }

  @Test func subscriptionDeliversItemsThenEnds() async throws {
    let (connection, host) = try await connected()
    let opened = Task {
      try await connection.subscribe(
        "subscribe.catalog", params: CatalogParams(), as: CatalogItem.self)
    }
    let frame = try await host.read()
    try await host.write(ServerFrame(id: frame.id, result: .object([:])))
    let stream = try await opened.value
    let item = try await host.json(CatalogItem(version: 7))
    try await host.write(ServerFrame(sub: frame.id, item: item))
    try await host.write(ServerFrame(sub: frame.id, end: true))
    var versions: [UInt64] = []
    for try await item in stream { versions.append(item.version) }
    #expect(versions == [7])
    await connection.close()
  }

  @Test func droppingAStreamCancelsTheSubscription() async throws {
    let (connection, host) = try await connected()
    let opened = Task {
      try await connection.subscribe(
        "subscribe.catalog", params: CatalogParams(), as: CatalogItem.self)
    }
    let frame = try await host.read()
    try await host.write(ServerFrame(id: frame.id, result: .object([:])))
    let reader = Task { for try await _ in try await opened.value {} }
    reader.cancel()
    let cancel = try await host.read()
    #expect(cancel.sub == frame.id && cancel.cancel)
    await connection.close()
  }

  @Test func closeFailsOpenCalls() async throws {
    let (connection, host) = try await connected()
    let result = Task {
      try await connection.call("a", params: CatalogParams(), as: HelloResult.self)
    }
    _ = try await host.read()
    await connection.close()
    await #expect(throws: ConnectionError.closed) { _ = try await result.value }
  }

  @Test func anUnansweredPingEndsTheConnection() async throws {
    let (connection, host) = try await connected {
      $0.pingInterval = .milliseconds(5)
      $0.pingTimeout = .milliseconds(5)
    }
    await host.setAnswersPings(false)
    let drain = Task { _ = try? await host.read() }
    let opened = Task {
      try await connection.subscribe(
        "subscribe.catalog", params: CatalogParams(), as: CatalogItem.self)
    }
    await #expect(throws: ConnectionError.heartbeatTimeout) { _ = try await opened.value }
    drain.cancel()
  }

  @Test func aLargeFrameCrossesPieces() async throws {
    let (connection, host) = try await connected()
    let text = String(repeating: "a", count: 200_000)
    let result = Task {
      try await connection.call("a", params: CatalogParams(), as: HelloResult.self)
    }
    let frame = try await host.read()
    try await host.write(
      ServerFrame(id: frame.id, result: try await host.json(HelloResult(host: text))))
    #expect(try await result.value.host == text)
    await connection.close()
  }
}
