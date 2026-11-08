import ActivityKit
import AppIntents
import SwiftUI

extension Color {
  static let hopletTint = Color(
    uiColor: UIColor { $0.userInterfaceStyle == .dark
      ? UIColor(red: 0.235, green: 0.769, blue: 0.788, alpha: 1)
      : UIColor(red: 0.024, green: 0.416, blue: 0.439, alpha: 1)
    })
  static let onHopletTint = Color(
    uiColor: UIColor { $0.userInterfaceStyle == .dark ? .black : .white })
  static let needsYou = Color(
    uiColor: UIColor { $0.userInterfaceStyle == .dark
      ? UIColor(red: 0.941, green: 0.639, blue: 0.357, alpha: 1)
      : UIColor(red: 0.710, green: 0.396, blue: 0.102, alpha: 1)
    })
}

/// One activity covers every session; its state follows the session that matters most.
struct SessionActivityAttributes: ActivityAttributes {
  struct ContentState: Codable, Hashable {
    enum Phase: String, Codable { case running, approval, question }

    var sessionID: String
    var title: String
    var harnessAsset: String
    var phase: Phase
    var detail: String
    var others: Int
  }
}

extension Notification.Name {
  static let hopletResolved = Notification.Name("hopletResolved")
}

private func resolveActivity(summary: String) async {
  for activity in Activity<SessionActivityAttributes>.activities {
    var state = activity.content.state
    let sessionID = state.sessionID
    state.phase = .running
    state.detail = summary
    await activity.update(ActivityContent(state: state, staleDate: nil))
    await MainActor.run {
      NotificationCenter.default.post(
        name: .hopletResolved, object: nil, userInfo: ["id": sessionID, "summary": summary])
    }
  }
}

/// Granting from the lock screen has to unlock first; refusing never does.
struct AllowOnceIntent: LiveActivityIntent {
  static let title: LocalizedStringResource = "允许一次"
  static let authenticationPolicy = IntentAuthenticationPolicy.requiresAuthentication

  func perform() async throws -> some IntentResult {
    await resolveActivity(summary: "已允许，正在运行")
    return .result()
  }
}

struct DenyIntent: LiveActivityIntent {
  static let title: LocalizedStringResource = "拒绝"

  func perform() async throws -> some IntentResult {
    await resolveActivity(summary: "你已拒绝")
    return .result()
  }
}
