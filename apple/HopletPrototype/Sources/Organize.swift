import SwiftUI

/// The list of one mode, with its groups, folding and archive.
struct SessionListScreen: View {
  @Environment(Store.self) private var store
  let mode: Mode
  @State private var path: [String] = []
  @State private var showSettings = false
  @State private var showNew = false
  @State private var showArchived = false

  private func subtitle(_ session: Session) -> String? {
    guard mode == .code else { return nil }
    return store.groupByDirectory
      ? "\(session.summary) · \(session.host)" : "\(session.summary) · \(session.directory)"
  }

  var body: some View {
    NavigationStack(path: $path) {
      List {
        ForEach(store.sections(mode)) { section in
          Section {
            if !store.collapsed.contains(section.id) {
              ForEach(section.sessions) { session in
                SessionLink(path: $path, session: session, subtitle: subtitle(session))
              }
            }
          } header: {
            if section.title != nil { header(section) }
          }
        }
      }
      .listStyle(.plain)
      .listSectionSpacing(.compact)
      .environment(\.defaultMinListHeaderHeight, 28)
      .navigationTitle(mode.rawValue)
      .navigationDestination(for: String.self) { SessionView(id: $0) }
      .toolbar {
        ToolbarItem(placement: .topBarTrailing) { organizeMenu }
        TopActions(showSettings: $showSettings, showNew: $showNew)
      }
      .sheet(isPresented: $showSettings) { SettingsView() }
      .sheet(isPresented: $showNew) { NewSessionView(mode: mode) { path.append($0) } }
      .navigationDestination(isPresented: $showArchived) { ArchivedView(mode: mode) }
    }
  }

  private func header(_ section: ListSection) -> some View {
    Button {
      store.toggleCollapsed(section.id)
    } label: {
      HStack(spacing: 6) {
        Text(section.title ?? "").fontWeight(.semibold).foregroundStyle(Color(uiColor: .label))
        Text("\(section.sessions.count)").foregroundStyle(Color(uiColor: .secondaryLabel))
        Spacer()
        Image(systemName: "chevron.right")
          .font(.caption2.weight(.semibold))
          .foregroundStyle(Color(uiColor: .secondaryLabel))
          .rotationEffect(.degrees(store.collapsed.contains(section.id) ? 0 : 90))
      }
      .font(.footnote)
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .contextMenu {
      if section.isGroup, let title = section.title {
        Button("删除分组", systemImage: "trash", role: .destructive) {
          store.deleteGroup(title, mode: mode)
        }
      }
    }
  }

  private var organizeMenu: some View {
    @Bindable var store = store
    let folded = store.allCollapsed(mode)
    return Menu {
      Button("新建分组", systemImage: "folder.badge.plus") { store.newGroup = (mode, nil) }
      Button(
        folded ? "全部展开" : "全部折叠",
        systemImage: folded ? "rectangle.expand.vertical" : "rectangle.compress.vertical"
      ) { store.setAllCollapsed(mode, !folded) }
      if mode == .code {
        Toggle("按工作目录分组", systemImage: "square.stack.3d.up", isOn: $store.groupByDirectory)
      }
      Divider()
      Button("已归档", systemImage: "archivebox") { showArchived = true }
    } label: {
      Label("整理", systemImage: "line.3.horizontal.decrease")
    }
  }
}

struct GroupMenu: View {
  @Environment(Store.self) private var store
  let session: Session

  var body: some View {
    Menu("移到分组", systemImage: "folder") {
      ForEach(store.groups[session.mode] ?? [], id: \.self) { name in
        Button {
          store.move(session.id, to: name)
        } label: {
          Text(name)
          if session.group == name { Image(systemName: "checkmark") }
        }
      }
      Divider()
      Button("新建分组", systemImage: "folder.badge.plus") {
        store.newGroup = (session.mode, session.id)
      }
      if session.group != nil {
        Button("移出分组", systemImage: "folder.badge.minus") { store.move(session.id, to: nil) }
      }
    }
  }
}

struct ArchivedView: View {
  @Environment(Store.self) private var store
  let mode: Mode

  private var sessions: [Session] { store.archived.filter { $0.mode == mode } }

  var body: some View {
    Group {
      if sessions.isEmpty {
        ContentUnavailableView("没有归档的会话", systemImage: "archivebox")
      } else {
        List(sessions) { session in
          SessionRow(session: session)
            .listRowSeparator(.hidden)
            .swipeActions(edge: .trailing, allowsFullSwipe: true) {
              Button("恢复", systemImage: "arrow.uturn.backward") { store.restore(session.id) }
            }
            .contextMenu {
              Button("恢复", systemImage: "arrow.uturn.backward") { store.restore(session.id) }
            }
        }
        .listStyle(.plain)
      }
    }
    .navigationTitle("已归档")
    .navigationBarTitleDisplayMode(.inline)
  }
}
