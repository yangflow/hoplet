import SwiftUI

@main
struct HopletPrototypeApp: App {
  @State private var store = Store()
  @Environment(\.scenePhase) private var scenePhase

  var body: some Scene {
    WindowGroup {
      Group {
        if store.hasHosts { RootView() } else { WelcomeView() }
      }
      .environment(store)
      .tint(.primary)
      .onChange(of: scenePhase) { _, phase in
        if phase == .active { store.connections.reconnectAll() }
      }
    }
  }
}

struct RootView: View {
  @Environment(Store.self) private var store
  @State private var groupName = ""

  private var namingGroup: Binding<Bool> {
    Binding(get: { store.newGroup != nil }, set: { if !$0 { store.newGroup = nil } })
  }

  var body: some View {
    TabView {
      Tab("收件箱", systemImage: "tray.fill") { InboxView() }
        .badge(store.needsYou.count)
      Tab("Personal", systemImage: "bubble.left.fill") { PersonalView() }
      Tab("Code", systemImage: "chevron.left.forwardslash.chevron.right") { CodeView() }
      Tab(role: .search) { SearchView() }
    }
    .overlay(alignment: .bottom) {
      if let archived = store.lastArchived {
        archiveToast
          .padding(.bottom, 72)
          .transition(.move(edge: .bottom).combined(with: .opacity))
          .task(id: archived.session.id) {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled else { return }
            withAnimation { store.lastArchived = nil }
          }
      }
    }
    .onReceive(NotificationCenter.default.publisher(for: .hopletResolved)) { note in
      guard let id = note.userInfo?["id"] as? String,
        let summary = note.userInfo?["summary"] as? String
      else { return }
      store.resolve(id, summary: summary)
    }
    .task { SystemDemo.registerCategories() }
    .alert("新建分组", isPresented: namingGroup) {
      TextField("名称", text: $groupName)
      Button("创建") {
        store.createGroup(groupName)
        groupName = ""
      }
      Button("取消", role: .cancel) { groupName = "" }
    }
  }

  private var archiveToast: some View {
    HStack(spacing: 14) {
      Text("已归档").font(.subheadline.weight(.medium))
      Button("撤销") { store.undoArchive() }
        .font(.subheadline.weight(.semibold))
    }
    .padding(.horizontal, 18)
    .frame(height: 44)
    .glassEffect(.regular.interactive(), in: .capsule)
  }
}

/// One mark at the end of the row; only an idle session shows its time.
struct RowCorner: View {
  @Environment(Store.self) private var store
  let session: Session

  private var disconnected: Bool {
    let link = store.link(session.host)
    return link == .offline || link == .revoked
  }

  var body: some View {
    Group {
      if disconnected {
        Image(systemName: "wifi.slash").foregroundStyle(.tertiary).accessibilityLabel("断线")
      } else if session.pending != nil {
        dot(.needsYou).accessibilityLabel("需要你")
      } else {
        switch session.status {
        case .running:
          Image(systemName: "progress.indicator")
            .symbolEffect(.variableColor.iterative.dimInactiveLayers, options: .repeating)
            .foregroundStyle(.secondary)
            .accessibilityLabel("运行中")
        case .failed:
          RoundedRectangle(cornerRadius: 2).fill(.primary).frame(width: 9, height: 9)
            .accessibilityLabel("失败")
        case .unread:
          dot(.hopletTint).accessibilityLabel("未读")
        case .idle:
          Text(session.time).foregroundStyle(.tertiary)
        }
      }
    }
    .font(.footnote.weight(.medium))
  }

  private func dot(_ color: Color) -> some View {
    Circle().fill(color).frame(width: 9, height: 9)
  }
}

/// Connection state as a mark: filled green when online, hollow grey when not.
struct OnlineDot: View {
  let online: Bool

  var body: some View {
    Image(systemName: online ? "circle.fill" : "circle")
      .font(.system(size: 10))
      .foregroundStyle(online ? Color.green : Color.secondary)
      .accessibilityLabel(online ? "在线" : "未连接")
  }

  /// Menus drop view styling, so the color has to live in the image.
  static func menuImage(online: Bool) -> Image {
    let config = UIImage.SymbolConfiguration(pointSize: 10)
    let image = UIImage(systemName: online ? "circle.fill" : "circle", withConfiguration: config)!
    return Image(
      uiImage: image.withTintColor(online ? .systemGreen : .systemGray, renderingMode: .alwaysOriginal))
  }
}

/// A quiet rounded fill while the finger is down, in place of the system row highlight.
struct RowPressStyle: ButtonStyle {
  func makeBody(configuration: Configuration) -> some View {
    configuration.label.background {
      RoundedRectangle(cornerRadius: 16)
        .fill(.quaternary)
        .padding(.horizontal, -12)
        .padding(.vertical, -2)
        .opacity(configuration.isPressed ? 1 : 0)
    }
  }
}

struct HarnessMark: View {
  let harness: Harness
  var size: CGFloat = 22

  var body: some View {
    Image(harness.asset)
      .resizable()
      .renderingMode(.template)
      .scaledToFit()
      .frame(width: size, height: size)
      .foregroundStyle(.primary)
      .accessibilityLabel(harness.rawValue)
  }
}

/// Two lines beside the harness mark; separators and disclosure arrows are left out on purpose.
struct SessionRow: View {
  @Environment(Store.self) private var store
  let session: Session
  var subtitle: String?
  var symbol: String?

  var body: some View {
    HStack(alignment: .top, spacing: 14) {
      HarnessMark(harness: session.harness, size: 20).padding(.top, 2)
      VStack(alignment: .leading, spacing: 4) {
        HStack(alignment: .firstTextBaseline) {
          Text(session.title).lineLimit(1)
            .fontWeight(session.status == .unread ? .semibold : .medium)
          Spacer()
          if symbol == nil || store.link(session.host) == .offline
            || store.link(session.host) == .revoked
          {
            RowCorner(session: session)
          } else {
            Text(session.time).font(.footnote).foregroundStyle(.tertiary)
          }
        }
        HStack(spacing: 6) {
          if let symbol {
            Image(systemName: symbol).font(.footnote).foregroundStyle(Color.needsYou)
          }
          Text(subtitle ?? session.summary).font(.subheadline).foregroundStyle(.secondary)
            .lineLimit(1)
        }
      }
    }
    .padding(.vertical, 6)
    .contentShape(.rect)
  }
}

struct SessionLink: View {
  @Environment(Store.self) private var store
  @Binding var path: [String]
  let session: Session
  var subtitle: String?

  private var pinTitle: String { session.pinned ? "取消置顶" : "置顶" }
  private var pinSymbol: String { session.pinned ? "pin.slash" : "pin" }

  var body: some View {
    Button {
      path.append(session.id)
    } label: {
      SessionRow(session: session, subtitle: subtitle)
    }
    .buttonStyle(RowPressStyle())
    .listRowSeparator(.hidden)
    .swipeActions(edge: .leading, allowsFullSwipe: true) {
      Button(pinTitle, systemImage: pinSymbol) { store.togglePin(session.id) }
    }
    .swipeActions(edge: .trailing, allowsFullSwipe: true) {
      Button("归档", systemImage: "archivebox") { store.archive(session.id) }
    }
    .contextMenu {
      Button(pinTitle, systemImage: pinSymbol) { store.togglePin(session.id) }
      GroupMenu(session: session)
      Button("归档", systemImage: "archivebox") { store.archive(session.id) }
    }
  }
}

struct TopActions: ToolbarContent {
  @Binding var showSettings: Bool
  @Binding var showNew: Bool

  var body: some ToolbarContent {
    ToolbarItem(placement: .topBarLeading) {
      Button("设置", systemImage: "person.crop.circle") { showSettings = true }
    }
    ToolbarItem(placement: .topBarTrailing) {
      Button("新建", systemImage: "square.and.pencil") { showNew = true }
    }
  }
}

struct PersonalView: View {
  var body: some View { SessionListScreen(mode: .personal) }
}

struct CodeView: View {
  var body: some View { SessionListScreen(mode: .code) }
}

struct SearchView: View {
  @Environment(Store.self) private var store
  @State private var query = ""
  @State private var path: [String] = []

  private var results: [Session] {
    query.isEmpty ? store.sessions : store.sessions.filter {
      $0.title.contains(query) || $0.summary.contains(query)
    }
  }

  var body: some View {
    NavigationStack(path: $path) {
      List(results) { session in
        SessionLink(path: $path, session: session)
      }
      .listStyle(.plain)
      .navigationTitle("搜索")
      .navigationDestination(for: String.self) { SessionView(id: $0) }
      .searchable(text: $query, prompt: "搜索会话")
    }
  }
}

struct SettingsView: View {
  @Environment(\.dismiss) private var dismiss
  @Environment(Store.self) private var store
  @State private var notify = true
  @State private var pairing = false

  var body: some View {
    NavigationStack {
      Form {
        Section("设备") {
          ForEach(store.hosts) { host in deviceRow(host) }
          Button("配对新设备", systemImage: "qrcode.viewfinder") { pairing = true }
        }
        ConnectionDemoSection()
        Section("通知") {
          Toggle("需要你时通知", isOn: $notify).tint(.green)
        }
        SystemDemoSections()
      }
      .navigationTitle("设置")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .topBarLeading) {
          Button("关闭", systemImage: "xmark") { dismiss() }
        }
      }
    }
    .sheet(isPresented: $pairing) { PairView() }
  }

  /// An offline row reconnects when tapped; a revoked device has to pair again.
  @ViewBuilder private func deviceRow(_ host: Host) -> some View {
    let link = store.link(host.name)
    let row = LabeledContent {
      if link == .revoked {
        Text("重新配对")
      } else {
        HostLinkMark(link: link)
      }
    } label: {
      Label(host.name, systemImage: host.symbol)
    }
    switch link {
    case .offline:
      Button { store.reconnect(host.name) } label: { row }.accessibilityHint("重新连接")
    case .revoked:
      Button { pairing = true } label: { row }
    case .online, .reconnecting:
      row
    }
  }
}
