import AVFoundation
import HopletClient
import SwiftUI
import VisionKit

/// How the phone stands with one host.
enum HostLink: String, CaseIterable, Identifiable {
  case online = "在线"
  case reconnecting = "重连中"
  case offline = "断线"
  case revoked = "已吊销"

  var id: String { rawValue }
}

/// A host's state as one mark at the end of a row.
struct HostLinkMark: View {
  let link: HostLink

  var body: some View {
    if link == .reconnecting {
      Image(systemName: "progress.indicator")
        .symbolEffect(.variableColor.iterative.dimInactiveLayers, options: .repeating)
        .font(.footnote.weight(.medium))
        .foregroundStyle(.secondary)
        .accessibilityLabel("重连中")
    } else {
      OnlineDot(online: link == .online)
    }
  }
}

/// The dashed outline of a composer that cannot send.
struct OfflineOutline: View {
  var body: some View {
    RoundedRectangle(cornerRadius: 25)
      .strokeBorder(.secondary, style: StrokeStyle(lineWidth: 1, dash: [5, 4]))
      .allowsHitTesting(false)
  }
}

/// What the app shows until the first host is paired.
struct WelcomeView: View {
  @State private var pairing = false

  var body: some View {
    VStack(spacing: 10) {
      Spacer()
      Image(systemName: "laptopcomputer.and.iphone")
        .font(.system(size: 44, weight: .light))
        .padding(.bottom, 10)
        .accessibilityHidden(true)
      Text("连接你的机器").font(.title2.weight(.semibold))
      Text("在机器上运行").foregroundStyle(.secondary)
      PairCommand()
      Spacer()
      Button {
        pairing = true
      } label: {
        Text("扫码配对").fontWeight(.semibold).frame(maxWidth: .infinity)
      }
      .buttonStyle(.glassProminent)
      .controlSize(.large)
      .padding(.horizontal, 24)
      .padding(.bottom, 12)
    }
    .sheet(isPresented: $pairing) { PairView() }
  }
}

private struct PairCommand: View {
  var body: some View {
    Text("hoplet pair")
      .font(.system(.body, design: .monospaced))
      .padding(.horizontal, 14).padding(.vertical, 8)
      .background(.quaternary, in: .rect(cornerRadius: 12))
  }
}

/// Scan or paste a code, confirm the host, then connect.
struct PairView: View {
  @Environment(Store.self) private var store
  @Environment(\.dismiss) private var dismiss
  @Environment(\.openURL) private var openURL

  private enum Stage: Equatable {
    case scan, confirm, connecting, done, unreachable, rejected
  }

  @State private var stage = Stage.scan
  @State private var notice: String?
  @State private var deviceName = "iPhone"
  @State private var code: PairingCode?
  @State private var camera = AVCaptureDevice.authorizationStatus(for: .video)

  private var hostName: String { code?.name ?? "" }

  var body: some View {
    NavigationStack {
      Group {
        switch stage {
        case .scan: scan
        case .confirm: confirm
        case .connecting: connecting
        case .done: done
        case .unreachable:
          failure(
            "wifi.slash", "连不上 \(hostName)", "确认两台设备在同一网络，或给 \(hostName) 配置中继",
            action: "重试", retry: { connect() })
        case .rejected:
          failure(
            "xmark.seal", "配对码已失效", "它用过了，或已生成新的。在机器上重新运行 hoplet pair",
            action: "重新扫码", retry: { withAnimation { stage = .scan } })
        }
      }
      .navigationTitle(stage == .scan ? "配对新设备" : "")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        if stage != .connecting && stage != .done {
          ToolbarItem(placement: .topBarLeading) {
            Button("关闭", systemImage: "xmark") { dismiss() }
          }
        }
      }
      .toolbarBackground(.hidden, for: .navigationBar)
    }
    .sensoryFeedback(.error, trigger: notice) { _, new in new != nil }
    .sensoryFeedback(.success, trigger: stage) { _, new in new == .done }
    .task {
      store.startDiscovery()
      if camera == .notDetermined {
        _ = await AVCaptureDevice.requestAccess(for: .video)
        camera = AVCaptureDevice.authorizationStatus(for: .video)
      }
    }
  }

  @ViewBuilder private var scan: some View {
    if camera == .denied || camera == .restricted {
      ContentUnavailableView {
        Label("没有相机权限", systemImage: "camera")
      } description: {
        Text("在系统设置里打开，或粘贴配对码")
      } actions: {
        Button("打开设置") {
          if let url = URL(string: UIApplication.openSettingsURLString) { openURL(url) }
        }
        .buttonStyle(.glass)
      }
      .safeAreaBar(edge: .bottom) { pasteBar }
    } else {
      ZStack {
        Color.black.ignoresSafeArea()
        if camera == .authorized, DataScannerViewController.isSupported {
          QRScanner(onCode: submit).ignoresSafeArea()
        }
        Image(systemName: "viewfinder")
          .font(.system(size: 250, weight: .ultraLight))
          .foregroundStyle(.white)
          .accessibilityHidden(true)
      }
      .safeAreaBar(edge: .bottom) { pasteBar }
      .environment(\.colorScheme, .dark)
    }
  }

  private var pasteBar: some View {
    VStack(spacing: 14) {
      Text(notice ?? "在机器上运行 hoplet pair")
        .font(.subheadline.weight(notice == nil ? .regular : .semibold))
        .foregroundStyle(notice == nil ? .secondary : .primary)
        .contentTransition(.opacity)
      Button {
        submit(UIPasteboard.general.string ?? "")
      } label: {
        Label("粘贴配对码", systemImage: "doc.on.clipboard").frame(maxWidth: .infinity)
      }
      .buttonStyle(.glass)
      .controlSize(.large)
    }
    .padding(.horizontal, 24)
    .padding(.bottom, 12)
  }

  private var confirm: some View {
    VStack(spacing: 8) {
      Spacer()
      Image(systemName: "laptopcomputer")
        .font(.system(size: 48, weight: .light))
        .padding(.bottom, 10)
        .accessibilityHidden(true)
      Text(hostName).font(.title2.weight(.semibold))
      Spacer()
    }
    .frame(maxWidth: .infinity)
    .safeAreaBar(edge: .bottom) {
      VStack(alignment: .leading, spacing: 14) {
        VStack(alignment: .leading, spacing: 6) {
          Text("这台手机的名字").font(.footnote).foregroundStyle(.secondary)
          TextField("名字", text: $deviceName)
            .padding(.horizontal, 12).frame(height: 44)
            .background(.quaternary.opacity(0.6), in: .rect(cornerRadius: 12))
        }
        Button {
          connect()
        } label: {
          Text("配对").fontWeight(.semibold).frame(maxWidth: .infinity)
        }
        .buttonStyle(.glassProminent)
        .controlSize(.large)
        .disabled(deviceName.isEmpty)
      }
      .padding(.horizontal, 24)
      .padding(.bottom, 12)
    }
  }

  private var connecting: some View {
    VStack(spacing: 14) {
      ProgressView().controlSize(.large)
      Text("正在连接 \(hostName)").foregroundStyle(.secondary)
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  private var done: some View {
    VStack(spacing: 12) {
      Image(systemName: "checkmark.circle")
        .font(.system(size: 52, weight: .light))
        .foregroundStyle(.green)
        .symbolEffect(.bounce, value: stage)
        .accessibilityHidden(true)
      Text("已配对 \(hostName)").font(.title3.weight(.semibold))
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  private func failure(
    _ symbol: String, _ title: String, _ detail: String, action: String,
    retry: @escaping () -> Void
  ) -> some View {
    ContentUnavailableView {
      Label(title, systemImage: symbol)
    } description: {
      Text(detail)
    } actions: {
      Button(action, action: retry).buttonStyle(.glass).controlSize(.large)
    }
  }

  private func submit(_ text: String) {
    guard stage == .scan else { return }
    guard let parsed = PairingCode(text) else { return show(notice: "不是 Hoplet 配对码") }
    guard parsed.expires > .now else { return show(notice: "配对码已过期，重新运行 hoplet pair") }
    notice = nil
    code = parsed
    withAnimation { stage = .confirm }
  }

  private func show(notice text: String) {
    notice = nil
    withAnimation { notice = text }
  }

  private func connect() {
    guard let code else { return }
    withAnimation { stage = .connecting }
    Task {
      do {
        try await store.pair(code, deviceName: deviceName)
        withAnimation { stage = .done }
        try? await Task.sleep(for: .seconds(1))
        store.finishPairing(code.name)
        dismiss()
      } catch NoiseError.pairingRefused {
        withAnimation { stage = .rejected }
      } catch {
        withAnimation { stage = .unreachable }
      }
    }
  }
}

/// The system QR scanner; reports every code it recognizes.
private struct QRScanner: UIViewControllerRepresentable {
  let onCode: (String) -> Void

  func makeUIViewController(context: Context) -> DataScannerViewController {
    let scanner = DataScannerViewController(
      recognizedDataTypes: [.barcode(symbologies: [.qr])], isHighlightingEnabled: false)
    scanner.delegate = context.coordinator
    try? scanner.startScanning()
    return scanner
  }

  func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
    context.coordinator.onCode = onCode
  }

  static func dismantleUIViewController(
    _ scanner: DataScannerViewController, coordinator: Coordinator
  ) {
    scanner.stopScanning()
  }

  func makeCoordinator() -> Coordinator { Coordinator(onCode: onCode) }

  final class Coordinator: NSObject, DataScannerViewControllerDelegate {
    var onCode: (String) -> Void
    private var last: String?

    init(onCode: @escaping (String) -> Void) { self.onCode = onCode }

    func dataScanner(
      _ scanner: DataScannerViewController, didAdd items: [RecognizedItem],
      allItems: [RecognizedItem]
    ) {
      for case .barcode(let barcode) in items {
        guard let text = barcode.payloadStringValue, text != last else { continue }
        last = text
        onCode(text)
      }
    }
  }
}

/// The sample host's connection states, reachable from Settings for review.
struct ConnectionDemoSection: View {
  @Environment(Store.self) private var store

  private var macBook: Binding<HostLink> {
    Binding(get: { store.link("MacBook") }, set: { store.links["MacBook"] = $0 })
  }

  var body: some View {
    Section("配对与断线") {
      Picker("MacBook 连接", selection: macBook) {
        ForEach(HostLink.allCases) { Text($0.rawValue).tag($0) }
      }
      Button("首次打开", systemImage: "sparkles") { store.hasHosts = false }
    }
  }
}
