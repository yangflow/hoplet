import SwiftUI

struct InboxView: View {
  @Environment(Store.self) private var store
  @State private var path: [String] = []
  @State private var handling: Session?
  @State private var showSettings = false
  @State private var showNew = false

  var body: some View {
    NavigationStack(path: $path) {
      List {
        if !store.needsYou.isEmpty {
          Section("需要你") {
            ForEach(store.needsYou) { session in
              needsYouRow(session)
            }
          }
        }
        Section("最近") {
          ForEach(store.recent) { session in
            SessionLink(path: $path, session: session)
          }
        }
      }
      .listStyle(.plain)
      .navigationTitle("收件箱")
      .navigationDestination(for: String.self) { SessionView(id: $0) }
      .toolbar { TopActions(showSettings: $showSettings, showNew: $showNew) }
      .sheet(item: $handling) { session in
        HandleSheet(session: session) { path.append(session.id) }
      }
      .sheet(isPresented: $showSettings) { SettingsView() }
      .sheet(isPresented: $showNew) {
        NewSessionView(mode: .code) { path.append($0) }
      }
    }
  }

  private func needsYouRow(_ session: Session) -> some View {
    let offline = store.isOffline(session.host)
    return Button {
      if offline { path.append(session.id) } else { handling = session }
    } label: {
      SessionRow(session: session, subtitle: ask(session), symbol: icon(session))
    }
    .buttonStyle(.plain)
    .listRowSeparator(.hidden)
    .swipeActions(edge: .leading, allowsFullSwipe: true) {
      if !offline, case .approval = session.pending {
        Button("允许一次", systemImage: "checkmark") {
          store.resolve(session.id, summary: "已允许，正在运行")
        }
        .tint(.hopletTint)
      }
    }
    .swipeActions(edge: .trailing) {
      if !offline, case .approval = session.pending {
        Button("拒绝", systemImage: "xmark", role: .destructive) {
          store.resolve(session.id, summary: "你已拒绝")
        }
      }
    }
    .contextMenu {
      Button("打开会话", systemImage: "arrow.up.forward") { path.append(session.id) }
      Button("归档", systemImage: "archivebox") { store.archive(session.id) }
    }
  }

  private func icon(_ session: Session) -> String {
    if case .approval = session.pending { return "shield.lefthalf.filled" }
    return "questionmark.bubble.fill"
  }

  private func ask(_ session: Session) -> String {
    switch session.pending {
    case .approval(let command): "运行 \(command)"
    case .question(let text, _): text
    case nil: session.summary
    }
  }
}

/// Resolves an approval or question in place, without leaving the current screen.
struct HandleSheet: View {
  @Environment(Store.self) private var store
  @Environment(\.dismiss) private var dismiss
  let session: Session
  let onOpen: () -> Void
  @State private var answer = ""

  var body: some View {
    NavigationStack {
      ScrollView {
        Group {
          switch session.pending {
          case .approval(let command): approval(command)
          case .question(let text, let options): question(text, options)
          case nil: EmptyView()
          }
        }
        .padding(.horizontal, 20)
      }
      .scrollBounceBehavior(.basedOnSize)
      .navigationTitle(session.title)
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .topBarLeading) {
          Button("关闭", systemImage: "xmark") { dismiss() }
        }
        ToolbarItem(placement: .topBarTrailing) {
          Button("打开会话", systemImage: "arrow.up.forward") {
            dismiss()
            onOpen()
          }
        }
      }
    }
    .presentationDetents([.medium, .large])
    .presentationDragIndicator(.visible)
  }

  private func finish(_ summary: String) {
    store.resolve(session.id, summary: summary)
    dismiss()
  }

  private func approval(_ command: String) -> some View {
    VStack(alignment: .leading, spacing: 16) {
      Text("运行这条命令？").font(.title2.bold())
      Text("\(session.harness.rawValue) 在 \(session.host) 的 \(session.directory) 目录")
        .font(.subheadline).foregroundStyle(.secondary)
      Text(command).font(.system(.callout, design: .monospaced))
        .frame(maxWidth: .infinity, alignment: .leading).padding(16)
        .background(.quaternary.opacity(0.6), in: .rect(cornerRadius: 18))
      VStack(spacing: 10) {
        Button { finish("已允许，正在运行") } label: {
          Text("允许一次").frame(maxWidth: .infinity).foregroundStyle(Color.onHopletTint)
        }
        .buttonStyle(.glassProminent)
        .tint(.hopletTint)
        Button { finish("已始终允许，正在运行") } label: {
          Text("始终允许").frame(maxWidth: .infinity)
        }
        .buttonStyle(.glass)
        Button(role: .destructive) { finish("你已拒绝") } label: {
          Text("拒绝").frame(maxWidth: .infinity).foregroundStyle(.red)
        }
        .buttonStyle(.glass)
      }
      .controlSize(.extraLarge)
    }
  }

  private func question(_ text: String, _ options: [String]) -> some View {
    VStack(alignment: .leading, spacing: 16) {
      Text(text).font(.title2.bold())
      VStack(spacing: 10) {
        ForEach(options, id: \.self) { option in
          Button { finish("你选了：\(option)") } label: {
            Text(option).frame(maxWidth: .infinity)
          }
          .buttonStyle(.glass)
          .controlSize(.extraLarge)
        }
      }
      TextField("其他回答", text: $answer)
        .padding(14)
        .background(.quaternary.opacity(0.6), in: .capsule)
        .submitLabel(.send)
        .onSubmit { if !answer.isEmpty { finish("你回答了：\(answer)") } }
    }
  }
}
