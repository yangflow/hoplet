import ActivityKit
import SwiftUI
import UserNotifications

/// Prototype controls that put the system surfaces into each designed state.
@MainActor
enum SystemDemo {
  private typealias State = SessionActivityAttributes.ContentState

  static func registerCategories() {
    let deny = UNNotificationAction(
      identifier: "deny", title: "拒绝", options: .destructive,
      icon: UNNotificationActionIcon(systemImageName: "xmark"))
    let allow = UNNotificationAction(
      identifier: "allow", title: "允许一次", options: .authenticationRequired,
      icon: UNNotificationActionIcon(systemImageName: "checkmark"))
    let approval = UNNotificationCategory(
      identifier: "approval", actions: [allow, deny], intentIdentifiers: [])
    UNUserNotificationCenter.current().setNotificationCategories([approval])
    UNUserNotificationCenter.current().delegate = NotificationDelegate.shared
  }

  private static func state(_ session: Session, others: Int) -> State {
    let phase: State.Phase
    let detail: String
    switch session.pending {
    case .approval(let command):
      phase = .approval
      detail = command
    case .question(let text, _):
      phase = .question
      detail = text
    case nil:
      phase = .running
      detail = session.summary
    }
    return State(
      sessionID: session.id, title: session.title, harnessAsset: session.harness.asset,
      phase: phase, detail: detail, others: others)
  }

  private static func isApproval(_ session: Session) -> Bool {
    if case .approval = session.pending { return true }
    return false
  }

  private static func start(_ states: [State]) async {
    await end()
    for state in states {
      _ = try? Activity.request(
        attributes: SessionActivityAttributes(),
        content: ActivityContent(state: state, staleDate: nil))
    }
  }

  static func end() async {
    for activity in Activity<SessionActivityAttributes>.activities {
      await activity.end(nil, dismissalPolicy: .immediate)
    }
  }

  static func showRunning(_ store: Store) async {
    guard let session = store.sessions.first(where: { $0.status == .running && $0.pending == nil })
    else { return }
    await start([state(session, others: 0)])
  }

  static func showApproval(_ store: Store, others: Int = 0) async {
    guard let session = store.needsYou.first(where: isApproval) else { return }
    await start([state(session, others: others)])
  }

  static func showQuestion(_ store: Store) async {
    guard let session = store.needsYou.first(where: { !isApproval($0) }) else { return }
    await start([state(session, others: 0)])
  }

  /// Two activities at once is what makes the system fall back to the minimal shape.
  static func showMinimal(_ store: Store) async {
    let sessions = store.needsYou.prefix(2).map { state($0, others: 0) }
    await start(sessions)
  }

  static func notifyApproval(_ store: Store) async {
    guard let session = store.needsYou.first(where: isApproval) else { return }
    let state = state(session, others: 0)
    await post(session, body: "运行 \(state.detail)", category: "approval")
  }

  static func notifyQuestion(_ store: Store) async {
    guard let session = store.needsYou.first(where: { !isApproval($0) }) else { return }
    await post(session, body: state(session, others: 0).detail, category: "")
  }

  static func notifyFailed(_ store: Store) async {
    guard let session = store.sessions.first(where: { $0.status == .failed }) else { return }
    await post(session, body: session.summary, category: "")
  }

  static func notifyLocked() async {
    let content = UNMutableNotificationContent()
    content.body = "有会话需要你"
    content.sound = .default
    await schedule(content, id: "locked")
  }

  private static func post(_ session: Session, body: String, category: String) async {
    let content = UNMutableNotificationContent()
    content.title = session.title
    content.body = body
    content.threadIdentifier = session.id
    content.categoryIdentifier = category
    content.userInfo = ["id": session.id]
    content.sound = .default
    await schedule(content, id: session.id)
  }

  private static func schedule(_ content: UNMutableNotificationContent, id: String) async {
    let center = UNUserNotificationCenter.current()
    _ = try? await center.requestAuthorization(options: [.alert, .badge, .sound])
    let trigger = UNTimeIntervalNotificationTrigger(timeInterval: 3, repeats: false)
    try? await center.add(UNNotificationRequest(identifier: id, content: content, trigger: trigger))
  }
}

final class NotificationDelegate: NSObject, UNUserNotificationCenterDelegate, Sendable {
  static let shared = NotificationDelegate()

  func userNotificationCenter(
    _ center: UNUserNotificationCenter, willPresent notification: UNNotification
  ) async -> UNNotificationPresentationOptions {
    [.banner, .sound]
  }

  func userNotificationCenter(
    _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse
  ) async {
    guard let id = response.notification.request.content.userInfo["id"] as? String else { return }
    let summary: String
    switch response.actionIdentifier {
    case "allow": summary = "已允许，正在运行"
    case "deny": summary = "你已拒绝"
    default: return
    }
    await MainActor.run {
      NotificationCenter.default.post(
        name: .hopletResolved, object: nil, userInfo: ["id": id, "summary": summary])
    }
  }
}

/// Reachable from Settings so each state can be tried on a device or simulator.
struct SystemDemoSections: View {
  @Environment(Store.self) private var store

  var body: some View {
    Section("灵动岛与锁屏") {
      demo("运行中", "progress.indicator") { await SystemDemo.showRunning(store) }
      demo("等待审批", "shield.lefthalf.filled") { await SystemDemo.showApproval(store) }
      demo("等你回答", "questionmark.bubble") { await SystemDemo.showQuestion(store) }
      demo("多个会话", "square.stack") { await SystemDemo.showApproval(store, others: 2) }
      demo("最小态", "circle.fill") { await SystemDemo.showMinimal(store) }
      demo("结束", "stop") { await SystemDemo.end() }
    }
    Section("通知（3 秒后）") {
      demo("审批", "shield.lefthalf.filled") { await SystemDemo.notifyApproval(store) }
      demo("提问", "questionmark.bubble") { await SystemDemo.notifyQuestion(store) }
      demo("出错", "exclamationmark.square") { await SystemDemo.notifyFailed(store) }
      demo("解密失败", "lock") { await SystemDemo.notifyLocked() }
    }
  }

  private func demo(_ title: String, _ symbol: String, action: @escaping () async -> Void) -> some View {
    Button(title, systemImage: symbol) { Task { await action() } }
  }
}
