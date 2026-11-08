import SwiftUI

struct ChangedFile: Identifiable, Hashable {
  let path: String
  let status: Character
  let added: Int
  let removed: Int

  var id: String { path }
  var name: String { (path as NSString).lastPathComponent }
  var directory: String { (path as NSString).deletingLastPathComponent }
}

struct RepoChange: Identifiable {
  let name: String
  let branch: String
  var error: String?
  var files: [ChangedFile] = []

  var id: String { name }
  var added: Int { files.reduce(0) { $0 + $1.added } }
  var removed: Int { files.reduce(0) { $0 + $1.removed } }
}

struct Commit: Identifiable, Hashable {
  let repo: String
  let title: String
  let sha: String

  var id: String { "\(repo):\(sha)" }
}

enum ChangeScope: Hashable {
  case session, uncommitted, branch
  case commit(Commit)

  var title: String {
    switch self {
    case .session: "会话累计"
    case .uncommitted: "未提交的改动"
    case .branch: "分支改动"
    case .commit(let commit): commit.sha
    }
  }
}

/// Phone layout of the session diff: one scope at a time, one section per repository.
struct ChangesSheet: View {
  @Environment(\.dismiss) private var dismiss
  let session: Session
  let onOpen: (String, [String]) -> Void
  @State private var scope = ChangeScope.session
  @State private var path: [Int] = []

  private static let commits = [
    Commit(repo: "hoplet", title: "fix(relay): drop stale dials", sha: "8547b5d"),
    Commit(repo: "hoplet", title: "test(relay): cover network switch", sha: "53d9029"),
    Commit(repo: "hoplet-site", title: "docs: note reconnect behavior", sha: "6c5ca40"),
  ]

  private var repos: [RepoChange] {
    let edited = session.messages.compactMap { message -> ChangedFile? in
      if case .change(let file, let added, let removed) = message.kind {
        return ChangedFile(path: file, status: "M", added: added, removed: removed)
      }
      return nil
    }
    guard !edited.isEmpty else { return [] }
    let added = ChangedFile(path: "internal/relay/dial_test.go", status: "A", added: 41, removed: 0)
    let gone = ChangedFile(path: "internal/relay/retry.go", status: "D", added: 0, removed: 37)
    let main: [ChangedFile]
    switch scope {
    case .session: main = edited + [added]
    case .uncommitted: main = Array(edited.prefix(1))
    case .branch: main = edited + [added, gone]
    case .commit: main = [added]
    }
    return [
      RepoChange(name: "hoplet", branch: "fix/relay-reconnect", files: main),
      RepoChange(name: "hoplet-site", branch: "main"),
      RepoChange(name: "vendor-proto", branch: "main", error: "读取失败：仓库不存在"),
    ]
  }

  private var allFiles: [ChangedFile] { repos.flatMap(\.files) }

  private var summary: String {
    let added = allFiles.reduce(0) { $0 + $1.added }
    let removed = allFiles.reduce(0) { $0 + $1.removed }
    return "\(allFiles.count) 个文件 · \(Self.counts(added, removed))"
  }

  private static func counts(_ added: Int, _ removed: Int) -> String {
    [added > 0 ? "+\(added)" : nil, removed > 0 ? "−\(removed)" : nil]
      .compactMap { $0 }.joined(separator: " ")
  }

  var body: some View {
    NavigationStack(path: $path) {
      Group {
        if session.directory == "deploy" {
          ContentUnavailableView("这个目录不是 git 仓库，看不到改动", systemImage: "folder")
        } else if repos.isEmpty {
          ContentUnavailableView("没有改动", systemImage: "checkmark.circle")
        } else {
          list
        }
      }
      .navigationTitle("变更")
      .navigationSubtitle(repos.isEmpty ? "" : summary)
      .navigationBarTitleDisplayMode(.inline)
      .navigationDestination(for: Int.self) { _ in commitList }
      .toolbar {
        ToolbarItem(placement: .topBarLeading) {
          Button("关闭", systemImage: "xmark") { dismiss() }
        }
        ToolbarItem(placement: .topBarTrailing) { scopeMenu }
      }
    }
    .presentationDetents([.medium, .large])
    .presentationDragIndicator(.visible)
  }

  private var scopeMenu: some View {
    Menu {
      option(.session, "建会话以来")
      option(.uncommitted, "对比 HEAD")
      option(.branch, "对比 main")
      Divider()
      Button {
        path = [0]
      } label: {
        Text("提交")
        Text("\(Self.commits.count) 个，选一个查看")
      }
    } label: {
      HStack(spacing: 4) {
        Text(scope.title).font(.subheadline.weight(.medium)).lineLimit(1)
        Image(systemName: "chevron.up.chevron.down").font(.caption2)
      }
      .padding(.horizontal, 8)
    }
  }

  private func option(_ value: ChangeScope, _ detail: String) -> some View {
    Button {
      scope = value
    } label: {
      Text(value.title)
      Text(detail)
      if scope == value { Image(systemName: "checkmark") }
    }
  }

  private var list: some View {
    List {
      ForEach(repos) { repo in
        Section {
          if let error = repo.error {
            Text(error).font(.subheadline).foregroundStyle(.secondary)
              .listRowSeparator(.hidden)
          } else if repo.files.isEmpty {
            Text("无改动").font(.subheadline).foregroundStyle(.secondary)
              .listRowSeparator(.hidden)
          }
          ForEach(repo.files) { row($0) }
        } header: {
          HStack(spacing: 4) {
            Text(repo.name).fontWeight(.semibold).foregroundStyle(.primary)
            Text("· \(repo.branch)").lineLimit(1)
            Spacer()
            if !repo.files.isEmpty { Text(Self.counts(repo.added, repo.removed)) }
          }
          .font(.footnote)
        }
      }
    }
    .listStyle(.plain)
  }

  private func row(_ file: ChangedFile) -> some View {
    Button {
      onOpen(file.path, allFiles.map(\.path))
    } label: {
      HStack(spacing: 12) {
        Text(String(file.status))
          .font(.system(.footnote, design: .monospaced).weight(.semibold))
          .foregroundStyle(.secondary)
          .frame(width: 14)
        VStack(alignment: .leading, spacing: 2) {
          Text(file.name).lineLimit(1)
          Text(file.directory).font(.footnote).foregroundStyle(.secondary).lineLimit(1)
        }
        Spacer()
        if file.added > 0 { Text("+\(file.added)").font(.footnote).foregroundStyle(.green) }
        if file.removed > 0 { Text("−\(file.removed)").font(.footnote).foregroundStyle(.red) }
      }
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .listRowSeparator(.hidden)
  }

  private var commitList: some View {
    List {
      ForEach(Array(Set(Self.commits.map(\.repo))).sorted(), id: \.self) { repo in
        Section(repo) {
          ForEach(Self.commits.filter { $0.repo == repo }) { commit in
            Button {
              scope = .commit(commit)
              path = []
            } label: {
              HStack {
                Text(commit.title).lineLimit(1)
                Spacer()
                Text(commit.sha).font(.system(.footnote, design: .monospaced))
                  .foregroundStyle(.secondary)
              }
              .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .listRowSeparator(.hidden)
          }
        }
      }
    }
    .listStyle(.plain)
    .navigationTitle("提交")
    .navigationBarTitleDisplayMode(.inline)
  }
}
