import Foundation
import HopletProto

/// One session as a subscription has delivered it, and the cursor to resume from.
public struct SessionLog: Hashable, Sendable {
  public private(set) var session: Session?
  public private(set) var events: [Event] = []
  public private(set) var pending: [ApprovalRequested] = []
  public private(set) var queue: [QueuedMessage] = []
  /// The `after_seq` of the next subscription.
  public private(set) var cursor: UInt64 = 0
  /// Set when older events can be paged in.
  public private(set) var hasOlder = false

  public init() {}

  /// Merges a subscription item: a snapshot replaces the view, events
  /// already held are ignored.
  public mutating func apply(_ item: SessionItem) {
    if let snapshot = item.snapshot {
      session = snapshot.session
      events = snapshot.events
      pending = snapshot.pending
      hasOlder = snapshot.more
      cursor = snapshot.session.lastSeq
    }
    for event in item.events where event.seq > cursor {
      events.append(event)
      cursor = event.seq
    }
    if let update = item.queue { queue = update.messages }
  }
}

/// The session list as a subscription has delivered it.
public struct CatalogView: Hashable, Sendable {
  public private(set) var sessions: [SessionID: Session] = [:]
  /// The `after_version` of the next subscription.
  public private(set) var version: UInt64 = 0

  public init() {}

  /// Merges a subscription item: a snapshot replaces the list.
  public mutating func apply(_ item: CatalogItem) {
    if item.snapshot { sessions = [:] }
    for session in item.sessions { sessions[session.id] = session }
    version = item.version
  }
}
