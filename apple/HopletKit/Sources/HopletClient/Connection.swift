import CryptoKit
import Foundation
import HopletProto

/// A connection carrying one Noise message per transport message.
public protocol MessageTransport: Sendable {
  func send(_ message: Data) async throws
  func receive() async throws -> Data
  func close() async
}

/// Why a connection stopped working.
public enum ConnectionError: Swift.Error, Equatable, Sendable {
  case closed
  case heartbeatTimeout
  case wrongHost(String)
  case badFrame
}

/// How to reach and prove ourselves to a host.
public struct ConnectOptions: Sendable {
  /// A one-time pairing: the code's secret and this device's display name.
  public struct Pairing: Sendable {
    public var psk: Data
    public var name: String

    public init(psk: Data, name: String) {
      self.psk = psk
      self.name = name
    }
  }

  public var identity: Curve25519.KeyAgreement.PrivateKey
  public var hostKey: Data
  public var pairing: Pairing?
  public var pingInterval: Duration = .seconds(15)
  public var pingTimeout: Duration = .seconds(10)
  /// The largest frame accepted from the host; a snapshot can be large.
  public var maxFrame = 64 << 20

  public init(
    identity: Curve25519.KeyAgreement.PrivateKey, hostKey: Data, pairing: Pairing? = nil
  ) {
    self.identity = identity
    self.hostKey = hostKey
    self.pairing = pairing
  }
}

/// A connection to a host. A lost connection is not reestablished: connect
/// again and resume subscriptions from their cursors. Call `close` when done.
public actor Connection {
  private struct Subscriber {
    var yield: (JSONValue) -> Void
    var finish: ((any Swift.Error)?) -> Void
  }

  private let transport: any MessageTransport
  private var channel: NoiseChannel
  private let outbound: AsyncStream<Data>.Continuation
  private var outboundMessages: AsyncStream<Data>?
  private var tasks: [Task<Void, Never>] = []
  private var failure: (any Swift.Error)?
  private var nextID: UInt64 = 0
  private var pending: [UInt64: CheckedContinuation<JSONValue, any Swift.Error>] = [:]
  private var subscribers: [UInt64: Subscriber] = [:]
  private var awaitedPong: Data?
  private var lossWaiters: [CheckedContinuation<Void, Never>] = []
  public private(set) var hello = HelloResult()

  private init(transport: any MessageTransport, channel: NoiseChannel) {
    self.transport = transport
    self.channel = channel
    (outboundMessages, outbound) = {
      let (stream, continuation) = AsyncStream.makeStream(of: Data.self)
      return (stream, continuation)
    }()
  }

  /// Runs the handshake and hello on `transport`.
  public static func connect(
    transport: any MessageTransport, options: ConnectOptions
  ) async throws -> Connection {
    try await connect(
      transport: transport, options: options,
      ephemeral: Curve25519.KeyAgreement.PrivateKey())
  }

  static func connect(
    transport: any MessageTransport, options: ConnectOptions,
    ephemeral: Curve25519.KeyAgreement.PrivateKey
  ) async throws -> Connection {
    guard let want = hostID(of: options.hostKey) else { throw NoiseError.handshake }
    do {
      var handshake = try NoiseHandshake(
        kind: options.pairing == nil ? .daily : .pair, identity: options.identity,
        peerStatic: options.hostKey, psk: options.pairing?.psk,
        payload: Data((options.pairing?.name ?? "").utf8), ephemeral: ephemeral)
      try await transport.send(try handshake.writeFirst())
      let keys = try handshake.readResponse(try await transport.receive())
      let connection = Connection(
        transport: transport,
        channel: NoiseChannel(send: keys.send, recv: keys.recv, maxFrame: options.maxFrame))
      await connection.start(interval: options.pingInterval, timeout: options.pingTimeout)
      do {
        try await connection.sayHello(want: want)
      } catch {
        await connection.close()
        throw error
      }
      return connection
    } catch {
      await transport.close()
      throw error
    }
  }

  private func sayHello(want: String) async throws {
    let result = try await call(
      "hello", params: HelloParams(protocolMajor: 1), as: HelloResult.self)
    guard result.hostId == want else { throw ConnectionError.wrongHost(result.hostId) }
    hello = result
  }

  private func start(interval: Duration, timeout: Duration) {
    let transport = self.transport
    tasks.append(
      Task {
        do {
          while self.handle(try await transport.receive()) {}
        } catch {
          self.lose(error)
        }
      })
    if let messages = outboundMessages {
      outboundMessages = nil
      tasks.append(
        Task {
          do {
            for await message in messages { try await transport.send(message) }
          } catch {
            self.lose(error)
          }
        })
    }
    tasks.append(
      Task {
        let rest = interval > timeout ? interval - timeout : .zero
        do {
          try await Task.sleep(for: interval)
          while true {
            self.ping()
            try await Task.sleep(for: timeout)
            guard self.pongArrived() else {
              self.lose(ConnectionError.heartbeatTimeout)
              return
            }
            try await Task.sleep(for: rest)
          }
        } catch {}  // canceled: the connection ended
      })
  }

  private func ping() {
    guard failure == nil else { return }
    let nonce = Data(SymmetricKey(size: .bits128).withUnsafeBytes { $0.prefix(channelNonceSize) })
    do {
      outbound.yield(try channel.sealPing(nonce))
      awaitedPong = nonce
    } catch {
      lose(error)
    }
  }

  private func pongArrived() -> Bool { awaitedPong == nil }

  /// Takes one transport message; false once the connection has ended.
  private func handle(_ message: Data) -> Bool {
    guard failure == nil else { return false }
    do {
      switch try channel.open(message) {
      case nil: break
      case .ping(let nonce): outbound.yield(try channel.sealPong(nonce))
      case .pong(let nonce): if awaitedPong == nonce { awaitedPong = nil }
      case .frame(let data): try dispatch(data)
      }
      return true
    } catch {
      lose(error)
      return false
    }
  }

  private func dispatch(_ data: Data) throws {
    guard let frame = try? ProtoCoder.decoder().decode(ServerFrame.self, from: data) else {
      throw ConnectionError.badFrame
    }
    if frame.sub != 0 {
      if frame.end {
        subscribers.removeValue(forKey: frame.sub)?.finish(frame.error)
      } else {
        subscribers[frame.sub]?.yield(frame.item)
      }
      return
    }
    guard let waiter = pending.removeValue(forKey: frame.id) else { return }
    if let error = frame.error {
      waiter.resume(throwing: error)
    } else {
      waiter.resume(returning: frame.result)
    }
  }

  /// Ends the connection with `error` unless it has already ended.
  private func lose(_ error: any Swift.Error) {
    guard failure == nil else { return }
    failure = error
    let waiters = pending
    let open = subscribers
    pending = [:]
    subscribers = [:]
    for waiter in waiters.values { waiter.resume(throwing: error) }
    for subscriber in open.values { subscriber.finish(error) }
    for waiter in lossWaiters { waiter.resume() }
    lossWaiters = []
    outbound.finish()
    for task in tasks { task.cancel() }
    let transport = self.transport
    Task { await transport.close() }
  }

  /// Closes the connection. Open calls and subscriptions fail with
  /// `ConnectionError.closed`.
  public func close() { lose(ConnectionError.closed) }

  /// Returns once the connection has stopped working.
  public func waitUntilLost() async {
    if failure != nil { return }
    await withCheckedContinuation { lossWaiters.append($0) }
  }

  private func send(_ frame: ClientFrame) throws {
    if let failure { throw failure }
    let data = try ProtoCoder.encoder().encode(frame)
    do {
      for message in try channel.seal(frame: data) { outbound.yield(message) }
    } catch {
      lose(error)
      throw error
    }
  }

  private func request(_ id: UInt64, method: String, params: JSONValue) async throws -> JSONValue {
    try await withTaskCancellationHandler {
      try await withCheckedThrowingContinuation { waiter in
        if Task.isCancelled {
          waiter.resume(throwing: CancellationError())
          return
        }
        pending[id] = waiter
        do {
          try send(ClientFrame(id: id, method: method, params: params))
        } catch {
          pending.removeValue(forKey: id)?.resume(throwing: error)
        }
      }
    } onCancel: {
      Task { await self.abandon(id) }
    }
  }

  /// Stops waiting for request `id`.
  private func abandon(_ id: UInt64) {
    pending.removeValue(forKey: id)?.resume(throwing: CancellationError())
  }

  private func allocateID() -> UInt64 {
    nextID += 1
    return nextID
  }

  private func encoded<P: Encodable>(_ params: P) throws -> JSONValue {
    try ProtoCoder.decoder().decode(
      JSONValue.self, from: try ProtoCoder.encoder().encode(params))
  }

  /// Sends a request and decodes its result; a host's refusal throws `APIError`.
  public func call<P: Encodable & Sendable, R: Decodable & Sendable>(
    _ method: String, params: P, as type: R.Type
  ) async throws -> R {
    let result = try await request(allocateID(), method: method, params: try encoded(params))
    return try result.decode(type)
  }

  /// Opens a subscription. Items wait in memory until read. Ending iteration
  /// early asks the host to drop the subscription.
  public func subscribe<P: Encodable & Sendable, Item: Decodable & Sendable>(
    _ method: String, params: P, as type: Item.Type
  ) async throws -> AsyncThrowingStream<Item, any Swift.Error> {
    let id = allocateID()
    let (stream, continuation) = AsyncThrowingStream.makeStream(
      of: Item.self, throwing: (any Swift.Error).self)
    subscribers[id] = Subscriber(
      yield: { item in
        do {
          continuation.yield(try item.decode(type))
        } catch {
          continuation.finish(throwing: error)
        }
      },
      finish: { error in continuation.finish(throwing: error) })
    continuation.onTermination = { termination in
      if case .cancelled = termination { Task { await self.cancelSubscription(id) } }
    }
    do {
      _ = try await request(id, method: method, params: try encoded(params))
    } catch {
      let wasOpen = subscribers.removeValue(forKey: id) != nil
      if error is CancellationError, wasOpen { try? send(ClientFrame(sub: id, cancel: true)) }
      continuation.finish(throwing: error)
      throw error
    }
    return stream
  }

  private func cancelSubscription(_ id: UInt64) {
    guard subscribers.removeValue(forKey: id) != nil else { return }
    try? send(ClientFrame(sub: id, cancel: true))
  }
}
