import SwiftUI

struct Host: Identifiable, Hashable {
  let name: String
  let symbol: String
  var online: Bool

  var id: String { name }

  static let all = [
    Host(name: "MacBook", symbol: "laptopcomputer", online: true),
    Host(name: "Mac mini", symbol: "macmini", online: true),
    Host(name: "VPS", symbol: "server.rack", online: false),
  ]
}

struct ModelOption: Identifiable, Hashable {
  let harness: Harness
  let name: String
  let detail: String
  var efforts: [String] = []
  var fast = false

  var id: String { "\(harness.rawValue):\(name)" }

  static let catalog = [
    ModelOption(
      harness: .claude, name: "Opus 5.5", detail: "最强，适合复杂任务",
      efforts: ["Low", "Medium", "High", "X-High", "Max", "Ultra"], fast: true),
    ModelOption(
      harness: .claude, name: "Sonnet 5.5", detail: "速度和能力的平衡",
      efforts: ["Low", "Medium", "High", "Max"]),
    ModelOption(harness: .claude, name: "Haiku 5.5", detail: "最快，适合简单任务"),
    ModelOption(
      harness: .codex, name: "GPT-5.5", detail: "通用编码模型",
      efforts: ["Low", "Medium", "High", "X-High"], fast: true),
    ModelOption(
      harness: .codex, name: "GPT-5.5 mini", detail: "更快、更省", efforts: ["Low", "Medium", "High"]),
    ModelOption(harness: .hermes, name: "默认模型", detail: "使用 Hermes 配置里的模型"),
  ]

  static func first(of harness: Harness) -> ModelOption {
    catalog.first { $0.harness == harness } ?? catalog[0]
  }
}

enum ApprovalLevel: String, CaseIterable, Identifiable {
  case confirm = "需确认", autoEdit = "自动编辑", autoReview = "智能审批", fullAuto = "全自动"

  var id: String { rawValue }
  var symbol: String {
    switch self {
    case .confirm: "shield"
    case .autoEdit: "shield.lefthalf.filled"
    case .autoReview: "checkmark.shield"
    case .fullAuto: "exclamationmark.shield"
    }
  }
}

struct NewSessionView: View {
  @Environment(Store.self) private var store
  @Environment(\.dismiss) private var dismiss
  let mode: Mode
  let onCreate: (String) -> Void

  @State private var prompt = ""
  @State private var name = ""
  private var hosts: [Host] {
    Host.all.map { Host(name: $0.name, symbol: $0.symbol, online: !store.isOffline($0.name)) }
  }
  @State private var hostName = "MacBook"
  @State private var directories = Workdir.recent
  @State private var workdir: Workdir? = Workdir.recent[0]
  @State private var model = ModelOption.first(of: .claude)
  @State private var effort = "默认"
  @State private var plan = false
  @State private var fast = false
  @State private var approval = ApprovalLevel.confirm
  @State private var browsing = false
  @State private var choosingModel = false
  @FocusState private var focus: Field?

  private enum Field { case name, prompt }

  private var host: Host { hosts.first { $0.name == hostName } ?? hosts[0] }
  private var offline: Bool { !host.online }
  private var needsDirectory: Bool { mode == .code && workdir == nil }
  private var canSend: Bool {
    !offline && !needsDirectory && !prompt.isEmpty && (mode == .code || !name.isEmpty)
  }
  private var placeholder: String {
    if needsDirectory { return "先选工作目录" }
    return mode == .code ? "描述任务" : "和它说第一句话"
  }

  var body: some View {
    NavigationStack {
      VStack(spacing: 14) {
        HarnessMark(harness: model.harness, size: 34)
        Text(mode == .code ? "要做什么？" : "想聊什么？").font(.title3.weight(.semibold))
      }
      .frame(maxWidth: .infinity, maxHeight: .infinity)
      .background { NewSessionBackdrop() }
      .contentShape(.rect)
      .onTapGesture { focus = nil }
      .safeAreaBar(edge: .bottom) { composer }
      .navigationTitle(mode == .code ? "新建会话" : "新建伙伴")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .topBarLeading) {
          Button("关闭", systemImage: "xmark") { dismiss() }
        }
      }
    }
    .presentationDragIndicator(.visible)
    .sheet(isPresented: $browsing) {
      DirectoryBrowser(hosts: hosts.map(\.name), host: hostName) { picked in
        if !directories.contains(picked) { directories.insert(picked, at: 0) }
        hostName = picked.host
        workdir = picked
      }
    }
    .sheet(isPresented: $choosingModel) {
      ModelSheet(selection: model, host: host, harnessFixed: false) { picked in
        model = picked
        effort = "默认"
        if !picked.fast { fast = false }
      }
    }
    .onAppear {
      if mode == .personal { model = ModelOption.first(of: .hermes) }
      prompt = store.drafts[draftKey] ?? ""
      name = store.drafts[draftKey + ":name"] ?? ""
      focus = mode == .code ? .prompt : .name
    }
    .onDisappear {
      store.drafts[draftKey] = prompt
      store.drafts[draftKey + ":name"] = name
    }
  }

  private var draftKey: String { "new:\(mode.rawValue)" }

  private var composer: some View {
    VStack(alignment: .leading, spacing: 2) {
      if mode == .personal {
        TextField("伙伴名字", text: $name)
          .font(.body.weight(.semibold))
          .focused($focus, equals: .name)
          .padding(.horizontal, 12)
          .padding(.top, 10)
      }
      TextField(placeholder, text: $prompt, axis: .vertical)
        .lineLimit(1...6)
        .focused($focus, equals: .prompt)
        .padding(.horizontal, 12)
        .padding(.top, mode == .personal ? 4 : 10)
        .padding(.bottom, 6)
      HStack(spacing: 0) {
        AttachMenu()
        chips
        Button(action: create) {
          ComposerCircle(symbol: "arrow.up", prominent: canSend)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("发送")
        .disabled(!canSend)
      }
    }
    .padding(4)
    .glassEffect(.regular, in: .rect(cornerRadius: 25))
    .overlay {
      if offline {
        RoundedRectangle(cornerRadius: 25)
          .strokeBorder(.secondary, style: StrokeStyle(lineWidth: 1, dash: [5, 4]))
          .allowsHitTesting(false)
      }
    }
    .padding(.horizontal, 12)
    .padding(.bottom, 8)
  }

  private var chips: some View {
    ChipScroller {
        if mode == .code && !offline { directoryChip }
        hostChip
        Button {
          choosingModel = true
        } label: {
          ComposerChip(title: model.name, harness: model.harness)
        }
        .buttonStyle(.plain)
        .disabled(offline)
        if !model.efforts.isEmpty { effortChip }
        approvalChip
    }
  }

  private var directoryChip: some View {
    Menu {
      Section("\(hostName) 上的工作目录") {
        ForEach(directories.filter { $0.host == hostName }) { directory in
          Button {
            workdir = directory
          } label: {
            Text(directory.name)
            Text(directory.path)
            Image(systemName: directory == workdir ? "checkmark" : "folder")
          }
        }
      }
      Divider()
      Button("浏览目录", systemImage: "folder.badge.plus") { browsing = true }
    } label: {
      ComposerChip(title: workdir?.name ?? "选择工作目录", symbol: "folder")
    }
    .buttonStyle(.plain)
  }

  private var hostSelection: Binding<String> {
    Binding(
      get: { hostName },
      set: { name in if let item = hosts.first(where: { $0.name == name }) { select(item) } })
  }

  private var hostChip: some View {
    Menu {
      Picker("运行在", selection: hostSelection) {
        ForEach(hosts) { item in
          Label {
            Text(item.name)
          } icon: {
            OnlineDot.menuImage(online: item.online)
          }
          .tag(item.name)
        }
      }
      .pickerStyle(.inline)
      if offline {
        Divider()
        Button("重新连接", systemImage: "arrow.clockwise") { reconnect() }
      }
    } label: {
      ComposerChip(title: hostName, symbol: offline ? "wifi.slash" : host.symbol)
    }
    .buttonStyle(.plain)
  }

  private var effortChip: some View {
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
    .disabled(offline)
  }

  private var approvalChip: some View {
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

  private func select(_ item: Host) {
    guard item.name != hostName else { return }
    hostName = item.name
    workdir = directories.first { $0.host == item.name }
  }

  private func reconnect() {
    store.reconnect(hostName)
  }

  private func create() {
    let id = store.create(
      mode == .code ? prompt : name, mode: mode, harness: model.harness,
      directory: mode == .code ? workdir?.name ?? "" : "伙伴", host: hostName)
    prompt = ""
    name = ""
    dismiss()
    onCreate(id)
  }
}

/// A quiet neutral field behind the composer, so the glass has something to refract.
private struct NewSessionBackdrop: View {
  var body: some View {
    MeshGradient(
      width: 3, height: 3,
      points: [
        [0, 0], [0.5, 0], [1, 0],
        [0, 0.5], [0.55, 0.45], [1, 0.5],
        [0, 1], [0.5, 1], [1, 1],
      ],
      colors: [
        Color(.systemGray5), Color(.systemGray3), Color(.systemGray5),
        Color(.systemGray4), Color(.systemGray6), Color(.systemGray3),
        Color(.systemBackground), Color(.systemGray5), Color(.systemBackground),
      ]
    )
    .ignoresSafeArea()
  }
}

/// Horizontal chips that fade only at an edge with more content beyond it.
struct ChipScroller<Content: View>: View {
  @ViewBuilder let content: Content
  @State private var fade = Fade()

  private struct Fade: Equatable {
    var leading: CGFloat = 0
    var trailing: CGFloat = 0
  }

  private static var width: CGFloat { 24 }

  var body: some View {
    ScrollView(.horizontal) {
      HStack(spacing: 6) { content }
    }
    .scrollIndicators(.hidden)
    .contentMargins(.horizontal, 8, for: .scrollContent)
    .onScrollGeometryChange(for: Fade.self) { geometry in
      let inset = geometry.contentInsets
      let offset = geometry.contentOffset.x + inset.leading
      let travel = geometry.contentSize.width + inset.leading + inset.trailing
        - geometry.containerSize.width
      guard travel > 0.5 else { return Fade() }
      return Fade(
        leading: min(Self.width, max(0, offset)),
        trailing: min(Self.width, max(0, travel - offset)))
    } action: { _, new in
      fade = new
    }
    .mask {
      HStack(spacing: 0) {
        LinearGradient(colors: [.clear, .black], startPoint: .leading, endPoint: .trailing)
          .frame(width: fade.leading)
        Color.black
        LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing)
          .frame(width: fade.trailing)
      }
    }
  }
}

/// A 34 pt control-filled capsule inside a 44 pt touch target.
struct ComposerChip: View {
  @Environment(\.isEnabled) private var isEnabled
  let title: String
  var symbol: String?
  var harness: Harness?

  var body: some View {
    HStack(spacing: 6) {
      if let harness { HarnessMark(harness: harness, size: 13) }
      if let symbol { Image(systemName: symbol).font(.caption) }
      Text(title).lineLimit(1).fixedSize()
    }
    .font(.footnote.weight(.medium))
    .foregroundStyle(isEnabled ? .primary : .tertiary)
    .padding(.horizontal, 12)
    .frame(height: 34)
    .background(.quaternary, in: .capsule)
    .frame(height: 44)
    .contentShape(.capsule)
  }
}

/// Models grouped by harness; picking a model is what picks the harness.
struct ModelSheet: View {
  @Environment(\.dismiss) private var dismiss
  let selection: ModelOption
  let host: Host
  let harnessFixed: Bool
  let onPick: (ModelOption) -> Void
  @State private var tab = Harness.claude
  @Namespace private var thumb

  var body: some View {
    VStack(spacing: 14) {
      if !harnessFixed { tabs }
      HStack(spacing: 6) {
        Image(systemName: host.symbol).font(.caption)
        Text("运行在 \(host.name)").font(.footnote)
      }
      .foregroundStyle(.secondary)
      ScrollView {
        VStack(spacing: 2) {
          ForEach(ModelOption.catalog.filter { $0.harness == tab }) { row($0) }
        }
        .padding(.horizontal, 12)
      }
    }
    .padding(.top, 28)
    .presentationDetents([.medium, .large])
    .presentationDragIndicator(.visible)
    .onAppear { tab = selection.harness }
  }

  private var tabs: some View {
    HStack(spacing: 0) {
      ForEach(Harness.allCases) { harness in
        Button {
          withAnimation(.snappy) { tab = harness }
        } label: {
          HStack(spacing: 6) {
            HarnessMark(harness: harness, size: 15)
            Text(harness.rawValue).font(.footnote.weight(.semibold)).lineLimit(1)
          }
          .frame(maxWidth: .infinity, minHeight: 36)
          .background {
            if tab == harness {
              Capsule().fill(Color(.tertiarySystemBackground))
                .shadow(color: .black.opacity(0.12), radius: 3, y: 1)
                .matchedGeometryEffect(id: "thumb", in: thumb)
            }
          }
          .contentShape(.capsule)
        }
        .buttonStyle(.plain)
      }
    }
    .padding(3)
    .background(.quaternary, in: .capsule)
    .padding(.horizontal, 16)
  }

  private func row(_ option: ModelOption) -> some View {
    Button {
      onPick(option)
      dismiss()
    } label: {
      HStack(spacing: 12) {
        VStack(alignment: .leading, spacing: 3) {
          Text(option.name).fontWeight(.medium)
          Text(option.detail).font(.footnote).foregroundStyle(.secondary)
        }
        Spacer()
        if option == selection {
          Image(systemName: "checkmark").font(.subheadline.bold())
        }
      }
      .padding(.horizontal, 14).padding(.vertical, 12)
      .background(
        option == selection ? AnyShapeStyle(.quaternary) : AnyShapeStyle(.clear),
        in: .rect(cornerRadius: 18)
      )
      .contentShape(.rect(cornerRadius: 18))
    }
    .buttonStyle(.plain)
  }
}
