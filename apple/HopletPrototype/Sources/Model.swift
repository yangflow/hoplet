import SwiftUI

enum Mode: String { case personal = "Personal", code = "Code" }

enum Pending {
  case approval(command: String)
  case question(text: String, options: [String])
}

enum Status { case idle, running, unread, failed }

enum Harness: String, CaseIterable, Identifiable {
  case claude = "Claude Code", codex = "Codex", hermes = "Hermes"

  var id: String { rawValue }
  var asset: String {
    switch self {
    case .claude: "claude-mark"
    case .codex: "openai-mark"
    case .hermes: "hermes-mark"
    }
  }
}

struct Message: Identifiable {
  enum Kind {
    case user(String)
    case agent(String)
    case steps(String, [String])
    case change(String, Int, Int)
  }
  let id = UUID()
  let kind: Kind
}

struct Session: Identifiable {
  let id: String
  var title: String
  var mode: Mode
  var harness: Harness
  var host: String
  var directory: String
  var status: Status
  var summary: String
  var time: String
  var pending: Pending?
  var messages: [Message]
  var queued: [String] = []
  var pinned = false
  var group: String?
}

/// One visible block of a session list; an untitled one has no header and cannot fold.
struct ListSection: Identifiable {
  let id: String
  let title: String?
  let isGroup: Bool
  let sessions: [Session]
}

/// A directory always belongs to one host, so choosing it also chooses where the session runs.
struct Workdir: Hashable, Identifiable {
  let host: String
  let path: String

  var id: String { "\(host):\(path)" }
  var name: String { (path as NSString).lastPathComponent }

  static let recent = [
    Workdir(host: "MacBook", path: "~/github/hoplet"),
    Workdir(host: "MacBook", path: "~/dotfiles"),
    Workdir(host: "VPS", path: "~/deploy"),
  ]
}

@MainActor @Observable
final class Store {
  var sessions: [Session] = Store.sample
  var drafts: [String: String] = [:]
  var lastArchived: (session: Session, index: Int)?
  var archived: [Session] = []
  var groups: [Mode: [String]] = [:]
  var collapsed: Set<String> = []
  var groupByDirectory = true
  /// The mode a new group is being named for, and the session to move into it, if any.
  var newGroup: (mode: Mode, sessionID: String?)?

  var needsYou: [Session] { sessions.filter { $0.pending != nil } }
  private var ordered: [Session] { sessions.filter(\.pinned) + sessions.filter { !$0.pinned } }
  var recent: [Session] { ordered.filter { $0.pending == nil && $0.status != .running } }

  func sections(_ mode: Mode) -> [ListSection] {
    let inMode = ordered.filter { $0.mode == mode }
    let named = (groups[mode] ?? []).map { name in
      ListSection(
        id: "group:\(name)", title: name, isGroup: true,
        sessions: inMode.filter { $0.group == name })
    }
    let loose = inMode.filter { $0.group == nil }
    guard mode == .code, groupByDirectory else {
      return loose.isEmpty
        ? named : named + [ListSection(id: "all", title: nil, isGroup: false, sessions: loose)]
    }
    let directories = Dictionary(grouping: loose, by: \.directory).sorted { $0.key > $1.key }
      .map { ListSection(id: "dir:\($0.key)", title: $0.key, isGroup: false, sessions: $0.value) }
    return named + directories
  }

  func allCollapsed(_ mode: Mode) -> Bool {
    let ids = sections(mode).filter { $0.title != nil }.map(\.id)
    return !ids.isEmpty && ids.allSatisfy(collapsed.contains)
  }

  func setAllCollapsed(_ mode: Mode, _ value: Bool) {
    let ids = sections(mode).filter { $0.title != nil }.map(\.id)
    withAnimation { value ? collapsed.formUnion(ids) : collapsed.subtract(ids) }
  }

  func toggleCollapsed(_ id: String) {
    withAnimation {
      if collapsed.remove(id) == nil { collapsed.insert(id) }
    }
  }

  func createGroup(_ name: String) {
    guard let request = newGroup, !name.isEmpty else { return }
    withAnimation {
      if groups[request.mode]?.contains(name) != true { groups[request.mode, default: []].append(name) }
      if let id = request.sessionID { update(id) { $0.group = name } }
    }
    newGroup = nil
  }

  func deleteGroup(_ name: String, mode: Mode) {
    withAnimation {
      groups[mode]?.removeAll { $0 == name }
      for index in sessions.indices where sessions[index].mode == mode && sessions[index].group == name {
        sessions[index].group = nil
      }
    }
  }

  func move(_ id: String, to group: String?) {
    withAnimation { update(id) { $0.group = group } }
  }

  func session(_ id: String) -> Session { sessions.first { $0.id == id } ?? sessions[0] }

  private func update(_ id: String, _ change: (inout Session) -> Void) {
    guard let index = sessions.firstIndex(where: { $0.id == id }) else { return }
    change(&sessions[index])
  }

  func resolve(_ id: String, summary: String) {
    withAnimation {
      update(id) {
        $0.pending = nil
        $0.status = .running
        $0.summary = summary
        $0.time = "刚刚"
      }
    }
  }

  func send(_ text: String, to id: String) {
    withAnimation {
      update(id) {
        if $0.status == .running {
          $0.queued.append(text)
        } else {
          $0.messages.append(Message(kind: .user(text)))
          $0.status = .running
          $0.summary = "正在处理"
        }
      }
    }
  }

  func steer(_ text: String, in id: String) {
    withAnimation {
      update(id) {
        $0.messages.append(Message(kind: .user(text)))
        $0.status = .running
      }
    }
  }

  func stop(_ id: String) {
    withAnimation {
      update(id) {
        $0.status = .idle
        $0.summary = "你已停止"
      }
    }
  }

  func removeQueued(_ text: String, in id: String) {
    withAnimation { update(id) { $0.queued.removeAll { $0 == text } } }
  }

  func create(
    _ prompt: String, mode: Mode, harness: Harness, directory: String, host: String
  ) -> String {
    let id = UUID().uuidString
    let session = Session(
      id: id, title: String(prompt.prefix(14)), mode: mode, harness: harness, host: host,
      directory: directory,
      status: .running, summary: "正在处理", time: "刚刚",
      messages: [Message(kind: .user(prompt))])
    withAnimation { sessions.insert(session, at: 0) }
    return id
  }

  func togglePin(_ id: String) {
    withAnimation { update(id) { $0.pinned.toggle() } }
  }

  func archive(_ id: String) {
    guard let index = sessions.firstIndex(where: { $0.id == id }) else { return }
    withAnimation {
      let session = sessions.remove(at: index)
      archived.insert(session, at: 0)
      lastArchived = (session, index)
    }
  }

  func undoArchive() {
    guard let (session, index) = lastArchived else { return }
    withAnimation {
      archived.removeAll { $0.id == session.id }
      sessions.insert(session, at: min(index, sessions.count))
      lastArchived = nil
    }
  }

  func restore(_ id: String) {
    guard let index = archived.firstIndex(where: { $0.id == id }) else { return }
    withAnimation { sessions.insert(archived.remove(at: index), at: 0) }
  }

  static let sample: [Session] = [
    Session(
      id: "relay", title: "修复重连抖动", mode: .code, harness: .claude, host: "MacBook", directory: "hoplet",
      status: .idle, summary: "运行 go test -race", time: "2 分",
      pending: .approval(command: "go test -race ./internal/relay/..."),
      messages: [
        Message(kind: .user("手机切网后 relay 会反复重连，查一下原因并修掉")),
        Message(kind: .steps("读取 6 个文件，编辑 2 个文件 · 38 秒", [
          "读取 internal/relay/dial.go", "读取 internal/relay/conn.go", "搜索 reconnect",
          "编辑 internal/relay/dial.go", "编辑 internal/relay/conn.go",
        ])),
        Message(kind: .agent("切网时旧连接还没超时，新连接已经建立，两边互相顶掉，于是 relay 不停重连。")),
        Message(kind: .agent("我在 dial.go 里加了代际号，旧连接不再触发重连。接下来跑一遍测试确认。")),
        Message(kind: .change("internal/relay/dial.go", 24, 6)),
        Message(kind: .change("internal/relay/conn.go", 9, 2)),
      ]),
    Session(
      id: "travel", title: "旅行规划", mode: .personal, harness: .hermes, host: "VPS", directory: "旅行",
      status: .idle, summary: "酒店住新宿还是上野？", time: "12 分",
      pending: .question(text: "酒店住新宿还是上野？", options: ["新宿，交通方便", "上野，便宜安静", "各住两晚"]),
      messages: [
        Message(kind: .user("帮我排 5 天东京行程，下月中旬去")),
        Message(kind: .agent("行程草案排好了，前两天在西边，后三天在东边。住宿会影响每天的动线，先定这个。")),
      ]),
    Session(
      id: "pr", title: "PR 巡检", mode: .personal, harness: .hermes, host: "VPS", directory: "巡检",
      status: .unread, summary: "CI 全部通过，3 个 PR 等你合并", time: "1 时",
      messages: [Message(kind: .agent("CI 全部通过，3 个 PR 等你合并。其中一个改动了发布脚本，值得多看一眼。"))]),
    Session(
      id: "assist", title: "日常助手", mode: .personal, harness: .hermes, host: "VPS", directory: "日常",
      status: .running, summary: "正在整理本周的账单", time: "刚刚",
      messages: [Message(kind: .user("整理本周账单")), Message(kind: .agent("正在读取账单邮件。"))]),
    Session(
      id: "swift", title: "iOS 协议类型生成", mode: .code, harness: .codex, host: "MacBook", directory: "hoplet",
      status: .running, summary: "正在编辑 main.go", time: "刚刚",
      messages: [Message(kind: .user("把 proto 生成 Swift Codable")), Message(kind: .agent("正在写生成器。"))]),
    Session(
      id: "zsh", title: "整理 zsh 配置", mode: .code, harness: .claude, host: "MacBook", directory: "dotfiles",
      status: .idle, summary: "改了 3 个文件", time: "昨天",
      messages: [Message(kind: .agent("配置已拆成 3 个文件，启动时间从 480 ms 降到 120 ms。"))]),
    Session(
      id: "notes", title: "读书笔记", mode: .personal, harness: .hermes, host: "VPS", directory: "读书",
      status: .idle, summary: "第 4 章的摘要已经写好", time: "周一",
      messages: [Message(kind: .agent("第 4 章的摘要已经写好。"))]),
    Session(
      id: "cert", title: "升级 relay 证书脚本", mode: .code, harness: .codex, host: "VPS", directory: "deploy",
      status: .failed, summary: "出错：磁盘空间不足", time: "周一",
      messages: [Message(kind: .agent("脚本在写入证书时失败：磁盘空间不足。"))]),
  ]
}
