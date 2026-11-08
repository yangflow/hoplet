import SwiftUI

struct SessionView: View {
  @Environment(Store.self) private var store
  @Environment(\.dismiss) private var dismiss
  let id: String
  @State private var archiveOnLeave = false
  @State private var draft = ""
  @State private var handling: Session?
  @State private var steps: StepList?
  @State private var showChanges = false
  @State private var openFile: String?
  @State private var pagerFiles: [String] = []
  @State private var model = ModelOption.first(of: .claude)
  @State private var effort = "默认"
  @State private var plan = false
  @State private var fast = false
  @State private var approval = ApprovalLevel.confirm
  @State private var choosingModel = false
  @State private var panelCollapsed = false
  @State private var position = ScrollPosition(edge: .bottom)
  @State private var awayFromBottom = false
  @FocusState private var composing: Bool

  private var session: Session { store.session(id) }
  private var link: HostLink { store.link(session.host) }
  private var offline: Bool { link != .online }
  @State private var pairing = false

  private var files: [(String, Int, Int)] {
    session.messages.compactMap {
      if case .change(let file, let added, let removed) = $0.kind { return (file, added, removed) }
      return nil
    }
  }

  var body: some View {
    ScrollView {
      LazyVStack(alignment: .leading, spacing: 16) {
        ForEach(session.messages) { message($0) }
      }
      .padding(.horizontal, 20)
      .padding(.vertical, 12)
    }
    .defaultScrollAnchor(.bottom)
    .scrollPosition($position)
    .scrollDismissesKeyboard(.interactively)
    .simultaneousGesture(TapGesture().onEnded { composing = false })
    .onScrollGeometryChange(for: Bool.self) { geometry in
      geometry.contentSize.height - geometry.visibleRect.maxY > 140
    } action: { _, away in
      withAnimation(.snappy) { awayFromBottom = away }
    }
    .overlay(alignment: .bottomTrailing) {
      if awayFromBottom {
        Button {
          withAnimation { position.scrollTo(edge: .bottom) }
        } label: {
          Image(systemName: "arrow.down").font(.body.weight(.medium))
            .frame(width: 40, height: 40)
            .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .glassEffect(.regular.interactive(), in: .circle)
        .accessibilityLabel("回到最新")
        .padding(.trailing, 16)
        .padding(.bottom, 12)
        .transition(.scale(scale: 0.6).combined(with: .opacity))
      }
    }
    .safeAreaBar(edge: .bottom) { bottomBar }
    .toolbar(.hidden, for: .tabBar)
    .navigationBarTitleDisplayMode(.inline)
    .toolbar { toolbarContent }
    .onAppear {
      draft = store.drafts[id] ?? ""
      model = ModelOption.catalog.first { $0.harness == session.harness && $0.name == model.name }
        ?? ModelOption.first(of: session.harness)
    }
    .onDisappear {
      store.drafts[id] = draft
      if archiveOnLeave { store.archive(id) }
    }
    .sheet(item: $handling) { current in HandleSheet(session: current) {} }
    .sheet(isPresented: $choosingModel) {
      ModelSheet(
        selection: model, host: Host.all.first { $0.name == session.host } ?? Host.all[0],
        harnessFixed: true
      ) { picked in
        model = picked
        effort = "默认"
        if !picked.fast { fast = false }
      }
    }
    .sheet(isPresented: $pairing) { PairView() }
    .sheet(item: $steps) { list in StepsSheet(list: list) }
    .sheet(isPresented: $showChanges) {
      ChangesSheet(session: session) { file, all in
        showChanges = false
        pagerFiles = all
        openFile = file
      }
    }
    .navigationDestination(item: $openFile) { file in
      DiffPager(files: pagerFiles.isEmpty ? [file] : pagerFiles, selection: file)
    }
  }

  @ToolbarContentBuilder private var toolbarContent: some ToolbarContent {
    ToolbarItem(placement: .principal) {
      VStack(spacing: 0) {
        Text(session.title).font(.headline).lineLimit(1)
        HStack(spacing: 4) {
          HarnessMark(harness: session.harness, size: 11)
          if offline {
            Image(systemName: "wifi.slash").font(.caption2).foregroundStyle(.secondary)
              .accessibilityLabel("断线")
          }
          Text("\(session.host) · \(session.directory)").font(.caption)
            .foregroundStyle(.secondary)
        }
      }
    }
    ToolbarItemGroup(placement: .topBarTrailing) {
      if session.mode == .code {
        Button("变更", systemImage: "plus.forwardslash.minus") { showChanges = true }
      }
      Menu("更多", systemImage: "ellipsis") {
        Section("\(session.title)\n\(session.directory) · \(session.host)") {
          Button("产物", systemImage: "square.stack.3d.up") {}
          if session.mode == .code {
            Button("终端", systemImage: "apple.terminal") {}
          }
          Button("分叉", systemImage: "arrow.triangle.branch") {}
        }
        Section {
          Button(session.pinned ? "取消置顶" : "置顶", systemImage: session.pinned ? "pin.slash" : "pin") {
            store.togglePin(id)
          }
          Button("归档", systemImage: "archivebox") {
            archiveOnLeave = true
            dismiss()
          }
        }
      }
    }
  }

  @ViewBuilder private func message(_ message: Message) -> some View {
    switch message.kind {
    case .user(let text):
      Text(text)
        .padding(.horizontal, 14).padding(.vertical, 9)
        .background(.quaternary, in: .rect(cornerRadius: 20))
        .frame(maxWidth: 300, alignment: .trailing)
        .frame(maxWidth: .infinity, alignment: .trailing)
        .contextMenu { copyButton(text) }
    case .agent(let text):
      Text(text).lineSpacing(5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(.rect)
        .contextMenu {
          copyButton(text)
          if session.mode == .personal {
            Button("转到 Code", systemImage: "chevron.left.forwardslash.chevron.right") {}
            Button("记住这个规律", systemImage: "brain") {}
          }
        }
    case .steps(let title, let items):
      Button {
        steps = StepList(title: title, items: items)
      } label: {
        HStack(spacing: 4) {
          Text(title).font(.subheadline)
          Image(systemName: "chevron.right").font(.caption2.weight(.semibold))
        }
        .foregroundStyle(.secondary)
        .frame(minHeight: 44)
      }
      .buttonStyle(.plain)
    case .change(let file, let added, let removed):
      Button {
        pagerFiles = files.map(\.0)
        openFile = file
      } label: {
        HStack(spacing: 8) {
          Image(systemName: "doc.text").foregroundStyle(.secondary)
          Text((file as NSString).lastPathComponent).font(.subheadline)
          Text("+\(added)").font(.footnote).foregroundStyle(.green)
          Text("-\(removed)").font(.footnote).foregroundStyle(.red)
        }
        .padding(.horizontal, 14).frame(height: 44)
        .background(.quaternary.opacity(0.6), in: .capsule)
      }
      .buttonStyle(.plain)
    }
  }

  private func copyButton(_ text: String) -> some View {
    Button("拷贝", systemImage: "doc.on.doc") { UIPasteboard.general.string = text }
  }

  private var bottomBar: some View {
    VStack(spacing: 8) {
      if !session.queued.isEmpty { queueCard }
      if session.pending != nil && !panelCollapsed {
        PendingPanel(session: session) {
          withAnimation(.snappy) { panelCollapsed = true }
        }
        .transition(.move(edge: .bottom).combined(with: .opacity))
      } else {
        if session.pending != nil { pendingCapsule }
        composer
      }
    }
    .padding(.horizontal, 12)
    .padding(.bottom, 8)
    .animation(.spring(response: 0.42, dampingFraction: 0.86), value: session.pending == nil)
  }

  private var queueCard: some View {
    VStack(spacing: 0) {
      ForEach(session.queued, id: \.self) { item in
        HStack(spacing: 8) {
          Image(systemName: "clock").font(.caption.weight(.medium)).foregroundStyle(.tertiary)
          Text(item).font(.subheadline).lineLimit(1)
          Spacer(minLength: 4)
          Button("插话", systemImage: "bolt.fill") {
            store.removeQueued(item, in: id)
            store.steer(item, in: id)
          }
          .labelStyle(.iconOnly)
          .buttonStyle(.plain)
          .frame(width: 36, height: 44)
          Menu("更多", systemImage: "ellipsis") {
            Button("编辑", systemImage: "pencil") {
              store.removeQueued(item, in: id)
              draft = item
              composing = true
            }
            Button("撤回", systemImage: "xmark", role: .destructive) {
              store.removeQueued(item, in: id)
            }
          }
          .labelStyle(.iconOnly)
          .buttonStyle(.plain)
          .frame(width: 36, height: 44)
        }
        .padding(.leading, 14)
        .padding(.trailing, 4)
      }
    }
    .glassEffect(.regular, in: .rect(cornerRadius: 20))
    .padding(.horizontal, 10)
  }

  private var pendingCapsule: some View {
    Button {
      withAnimation(.snappy) { panelCollapsed = false }
    } label: {
      HStack(spacing: 8) {
        Circle().fill(Color.needsYou).frame(width: 9, height: 9)
        Text(pendingTitle).font(.subheadline.weight(.semibold))
        Text(pendingDetail).font(.footnote).foregroundStyle(.secondary).lineLimit(1)
        Spacer()
        Image(systemName: "chevron.up").font(.footnote).foregroundStyle(.secondary)
      }
      .padding(.horizontal, 16).frame(height: 48)
      .contentShape(.capsule)
    }
    .buttonStyle(.plain)
    .glassEffect(.regular.interactive(), in: .capsule)
  }

  private var pendingTitle: String {
    if case .approval = session.pending { return "等待审批" }
    return "等你回答"
  }

  private var pendingDetail: String {
    switch session.pending {
    case .approval(let command): command
    case .question(let text, _): text
    case nil: ""
    }
  }

  /// A capsule at rest; a card with the session's controls while writing.
  private var composer: some View {
    VStack(alignment: .leading, spacing: 0) {
      HStack(alignment: .bottom, spacing: 2) {
        if !composing { AttachMenu() }
        TextField("发消息给 \(session.harness.rawValue)", text: $draft, axis: .vertical)
          .lineLimit(composing ? 1...8 : 1...2)
          .focused($composing)
          .padding(.vertical, 11)
          .padding(.horizontal, composing ? 12 : 4)
        if !composing { sendButton }
      }
      if composing {
        HStack(spacing: 0) {
          AttachMenu()
          sessionChips
          sendButton
        }
        .transition(.opacity)
      }
    }
    .padding(4)
    .contentShape(.rect(cornerRadius: 25))
    .onTapGesture { composing = true }
    .glassEffect(.regular, in: .rect(cornerRadius: 25))
    .overlay { if offline { OfflineOutline() } }
    .animation(.spring(response: 0.42, dampingFraction: 0.86), value: composing)
  }

  private var sessionChips: some View {
    ChipScroller {
      Button {
        choosingModel = true
      } label: {
        ComposerChip(title: model.name, harness: model.harness)
      }
      .buttonStyle(.plain)
      if !model.efforts.isEmpty {
        Menu {
          Section("强度") {
            Picker("强度", selection: $effort) {
              ForEach(["默认"] + model.efforts, id: \.self) { Text($0).tag($0) }
            }
            .pickerStyle(.inline)
          }
          Section {
            Toggle("Plan", isOn: $plan)
            if model.fast { Toggle("Fast", isOn: $fast) }
          }
        } label: {
          ComposerChip(title: effort, symbol: "gauge.with.dots.needle.67percent")
        }
        .buttonStyle(.plain)
      }
      Menu {
        Picker("审批档位", selection: $approval) {
          ForEach(ApprovalLevel.allCases) { Label($0.rawValue, systemImage: $0.symbol).tag($0) }
        }
        .pickerStyle(.inline)
      } label: {
        ComposerChip(title: approval.rawValue, symbol: approval.symbol)
      }
      .buttonStyle(.plain)
    }
  }

  @ViewBuilder private var sendButton: some View {
    if link == .reconnecting {
      ComposerCircle(symbol: "progress.indicator")
        .symbolEffect(.variableColor.iterative.dimInactiveLayers, options: .repeating)
        .accessibilityLabel("重连中")
    } else if link == .offline {
      Button { store.reconnect(session.host) } label: {
        ComposerCircle(symbol: "wifi.slash")
      }
      .buttonStyle(.plain)
      .accessibilityLabel("重新连接")
    } else if link == .revoked {
      Button { pairing = true } label: {
        ComposerCircle(symbol: "qrcode.viewfinder")
      }
      .buttonStyle(.plain)
      .accessibilityLabel("重新配对")
    } else if draft.isEmpty && session.status == .running {
      Button { store.stop(id) } label: {
        ComposerCircle(symbol: "stop.fill")
      }
      .buttonStyle(.plain)
      .accessibilityLabel("停止")
    } else {
      Button {
        store.send(draft, to: id)
        draft = ""
        composing = false
      } label: {
        ComposerCircle(symbol: "arrow.up", prominent: !draft.isEmpty)
      }
      .buttonStyle(.plain)
      .accessibilityLabel("发送")
      .disabled(draft.isEmpty)
      .contextMenu {
        if session.status == .running && !draft.isEmpty {
          Button("插话", systemImage: "bolt") {
            store.steer(draft, in: id)
            draft = ""
            composing = false
          }
        }
      }
    }
  }
}

/// Takes the composer's place while the agent waits, so answering stays on this screen.
struct PendingPanel: View {
  @Environment(Store.self) private var store
  let session: Session
  let onCollapse: () -> Void
  @State private var picked: String?
  @State private var answer = ""

  private var offline: Bool { store.isOffline(session.host) }

  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      switch session.pending {
      case .approval(let command): approval(command)
      case .question(let text, let options): question(text, options)
      case nil: EmptyView()
      }
    }
    .disabled(offline)
    .padding(16)
    .frame(maxWidth: .infinity, alignment: .leading)
    .overlay(alignment: .topTrailing) {
      Button("收起", systemImage: "chevron.down", action: onCollapse)
        .labelStyle(.iconOnly)
        .font(.footnote.weight(.semibold))
        .foregroundStyle(.secondary)
        .buttonStyle(.plain)
        .frame(width: 36, height: 36)
        .contentShape(.circle)
        .glassEffect(.regular.interactive(), in: .circle)
        .padding(8)
    }
    .glassEffect(.regular, in: .rect(cornerRadius: 26))
    .sensoryFeedback(.selection, trigger: picked)
  }

  private func header(_ title: String) -> some View {
    HStack(spacing: 6) {
      if offline {
        Image(systemName: "wifi.slash").font(.caption).foregroundStyle(.secondary)
      } else {
        Circle().fill(Color.needsYou).frame(width: 9, height: 9)
      }
      Text(offline ? "连上后才能处理" : title).font(.footnote.weight(.medium))
        .foregroundStyle(.secondary)
    }
  }

  private func approval(_ command: String) -> some View {
    Group {
      header("等待审批")
      Text(command).font(.system(.footnote, design: .monospaced)).lineLimit(3)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(.quaternary, in: .rect(cornerRadius: 14))
      Text("\(session.harness.rawValue) 在 \(session.host) 的 \(session.directory) 目录")
        .font(.footnote).foregroundStyle(.secondary).lineLimit(1)
      HStack(spacing: 8) {
        Button { store.resolve(session.id, summary: "你已拒绝") } label: {
          Text("拒绝").frame(maxWidth: .infinity).foregroundStyle(.red)
        }
        .buttonStyle(.glass)
        Button { store.resolve(session.id, summary: "已始终允许，正在运行") } label: {
          Text("始终允许").frame(maxWidth: .infinity)
        }
        .buttonStyle(.glass)
        Button { store.resolve(session.id, summary: "已允许，正在运行") } label: {
          Text("允许一次").frame(maxWidth: .infinity).foregroundStyle(Color.onHopletTint)
        }
        .buttonStyle(.glassProminent)
        .tint(.hopletTint)
      }
      .controlSize(.large)
    }
  }

  private func question(_ text: String, _ options: [String]) -> some View {
    Group {
      header("等你回答")
      Text(text).font(.headline).padding(.trailing, 32)
      VStack(spacing: 6) {
        ForEach(Array(options.enumerated()), id: \.element) { index, option in
          Button {
            choose(option)
          } label: {
            HStack(spacing: 10) {
              Image(systemName: picked == option ? "checkmark.circle.fill" : "\(index + 1).circle")
              Text(option).fontWeight(.medium)
              Spacer()
            }
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(.quaternary, in: .rect(cornerRadius: 14))
            .contentShape(.rect(cornerRadius: 14))
          }
          .buttonStyle(.plain)
        }
      }
      TextField("其他回答", text: $answer)
        .padding(.horizontal, 12).frame(height: 44)
        .background(.quaternary.opacity(0.6), in: .rect(cornerRadius: 12))
        .submitLabel(.send)
        .onSubmit {
          if !answer.isEmpty { store.resolve(session.id, summary: "你回答了：\(answer)") }
        }
    }
  }

  private func choose(_ option: String) {
    picked = option
    Task {
      try? await Task.sleep(for: .milliseconds(220))
      store.resolve(session.id, summary: "你选了：\(option)")
    }
  }
}

/// A 36 pt filled circle inside a 44 pt touch target, shared by every composer control.
struct ComposerCircle: View {
  @Environment(\.isEnabled) private var isEnabled
  let symbol: String
  var prominent = false

  var body: some View {
    Image(systemName: symbol)
      .font(.body.weight(.semibold))
      .foregroundStyle(prominent ? Color.onHopletTint : isEnabled ? .primary : Color(.tertiaryLabel))
      .frame(width: 36, height: 36)
      .background(
        prominent ? AnyShapeStyle(Color.hopletTint) : AnyShapeStyle(.quaternary), in: .circle
      )
      .frame(width: 44, height: 44)
      .contentShape(.circle)
  }
}

struct AttachMenu: View {
  var body: some View {
    Menu {
      Button("照片", systemImage: "photo") {}
      Button("文件", systemImage: "doc") {}
    } label: {
      ComposerCircle(symbol: "plus")
    }
    .buttonStyle(.plain)
  }
}

struct StepList: Identifiable {
  let id = UUID()
  let title: String
  let items: [String]
}

struct StepsSheet: View {
  @Environment(\.dismiss) private var dismiss
  let list: StepList

  var body: some View {
    NavigationStack {
      List(list.items, id: \.self) { Text($0).font(.subheadline) }
        .listStyle(.plain)
        .navigationTitle(list.title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
          ToolbarItem(placement: .topBarLeading) {
            Button("关闭", systemImage: "xmark") { dismiss() }
          }
        }
    }
    .presentationDetents([.medium, .large])
    .presentationDragIndicator(.visible)
  }
}

/// One file per page: phones read a single-column diff and swipe between files.
struct DiffPager: View {
  let files: [String]
  @State var selection: String

  private let lines: [(Character, String)] = [
    (" ", "func (d *dialer) run(ctx context.Context) error {"),
    ("-", "  conn, err := d.dial(ctx)"),
    ("+", "  gen := d.gen.Add(1)"),
    ("+", "  conn, err := d.dial(ctx, gen)"),
    (" ", "  if err != nil {"),
    (" ", "    return fmt.Errorf(\"dial: %w\", err)"),
    (" ", "  }"),
    ("+", "  if gen != d.gen.Load() {"),
    ("+", "    return conn.Close()"),
    ("+", "  }"),
    (" ", "  return d.serve(ctx, conn)"),
    (" ", "}"),
  ]

  var body: some View {
    TabView(selection: $selection) {
      ForEach(files, id: \.self) { _ in
        ScrollView {
          VStack(alignment: .leading, spacing: 0) {
            ForEach(lines.indices, id: \.self) { index in
              let (sign, text) = lines[index]
              Text(String(sign) + " " + text)
                .font(.system(.footnote, design: .monospaced))
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 12).padding(.vertical, 2)
                .background(sign == "+" ? Color.green.opacity(0.14)
                  : sign == "-" ? Color.red.opacity(0.14) : .clear)
            }
          }
        }
      }
    }
    .tabViewStyle(.page(indexDisplayMode: .automatic))
    .navigationTitle((selection as NSString).lastPathComponent)
    .navigationBarTitleDisplayMode(.inline)
  }
}

/// Walks a host's folders one level per screen, the way a phone browses any hierarchy.
struct DirectoryBrowser: View {
  @Environment(\.dismiss) private var dismiss
  let hosts: [String]
  @State var host: String
  let onPick: (Workdir) -> Void
  @State private var path: [String] = []
  @State private var tree = [
    "MacBook:~": ["dotfiles", "github", "work"],
    "MacBook:~/github": ["hoplet", "mygo"],
    "MacBook:~/work": ["api", "web"],
    "VPS:~": ["companions", "deploy"],
    "Mac mini:~": ["builds", "media"],
  ]
  @State private var creating = false
  @State private var newName = ""

  private var current: String { path.last ?? "~" }

  var body: some View {
    NavigationStack(path: $path) {
      level("~")
        .navigationDestination(for: String.self) { level($0) }
    }
    .presentationDetents([.large])
    .presentationDragIndicator(.visible)
    .alert("新建目录", isPresented: $creating) {
      TextField("目录名", text: $newName)
      Button("取消", role: .cancel) { newName = "" }
      Button("新建") { createFolder() }
    }
  }

  private func level(_ directory: String) -> some View {
    let children = tree["\(host):\(directory)"] ?? []
    return List(children, id: \.self) { name in
      NavigationLink(value: "\(directory)/\(name)") {
        Label(name, systemImage: "folder")
      }
      .listRowSeparator(.hidden)
    }
    .listStyle(.plain)
    .overlay {
      if children.isEmpty { ContentUnavailableView("没有子目录", systemImage: "folder") }
    }
    .navigationTitle(directory == "~" ? "选择工作目录" : (directory as NSString).lastPathComponent)
    .navigationBarTitleDisplayMode(.inline)
    .safeAreaBar(edge: .bottom) { chooseBar(directory) }
    .toolbar {
      if directory == "~" {
        ToolbarItem(placement: .topBarLeading) {
          Button("关闭", systemImage: "xmark") { dismiss() }
        }
      }
      ToolbarItemGroup(placement: .topBarTrailing) {
        if directory == "~" {
          Menu(host, systemImage: "laptopcomputer") {
            Picker("设备", selection: $host) {
              ForEach(hosts, id: \.self) { Text($0) }
            }
            .pickerStyle(.inline)
          }
        }
        Button("新建目录", systemImage: "folder.badge.plus") { creating = true }
      }
    }
  }

  private func chooseBar(_ directory: String) -> some View {
    VStack(spacing: 8) {
      Text("\(host) · \(directory)").font(.footnote).foregroundStyle(.secondary).lineLimit(1)
      Button {
        onPick(Workdir(host: host, path: directory))
        dismiss()
      } label: {
        Text("选择此目录").fontWeight(.semibold).frame(maxWidth: .infinity)
      }
      .buttonStyle(.glass)
      .controlSize(.extraLarge)
    }
    .padding(.horizontal, 16)
    .padding(.bottom, 8)
  }

  private func createFolder() {
    let name = newName.trimmingCharacters(in: .whitespaces)
    newName = ""
    guard !name.isEmpty else { return }
    tree["\(host):\(current)", default: []].append(name)
    path.append("\(current)/\(name)")
  }
}
