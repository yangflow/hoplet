import ActivityKit
import SwiftUI
import WidgetKit

@main
struct HopletActivityBundle: WidgetBundle {
  var body: some Widget { SessionActivityWidget() }
}

private typealias ActivityState = SessionActivityAttributes.ContentState

private struct Mark: View {
  let asset: String
  let size: CGFloat

  var body: some View {
    Image(asset)
      .resizable()
      .renderingMode(.template)
      .scaledToFit()
      .frame(width: size, height: size)
  }
}

/// The same marks as the end of a list row.
private struct PhaseMark: View {
  let phase: ActivityState.Phase

  var body: some View {
    if phase == .running {
      Image(systemName: "progress.indicator")
        .font(.footnote.weight(.medium))
        .foregroundStyle(.secondary)
        .accessibilityLabel("运行中")
    } else {
      Circle().fill(Color.needsYou).frame(width: 9, height: 9)
        .accessibilityLabel("需要你")
    }
  }
}

private struct Detail: View {
  let state: ActivityState

  var body: some View {
    Text(state.detail)
      .font(state.phase == .approval ? .system(.footnote, design: .monospaced) : .subheadline)
      .foregroundStyle(.secondary)
      .lineLimit(2)
      .frame(maxWidth: .infinity, alignment: .leading)
  }
}

private struct ApprovalButtons: View {
  var body: some View {
    HStack(spacing: 8) {
      Button(intent: DenyIntent()) {
        Text("拒绝").fontWeight(.medium).foregroundStyle(.red).frame(maxWidth: .infinity)
      }
      .buttonStyle(.bordered)
      .tint(.secondary)
      Button(intent: AllowOnceIntent()) {
        Text("允许一次").fontWeight(.semibold).foregroundStyle(Color.onHopletTint)
          .frame(maxWidth: .infinity)
      }
      .buttonStyle(.borderedProminent)
      .tint(.hopletTint)
    }
    .controlSize(.large)
    .buttonBorderShape(.capsule)
  }
}

private struct Others: View {
  let count: Int

  var body: some View {
    if count > 0 {
      Text("+\(count)")
        .font(.footnote.weight(.semibold))
        .foregroundStyle(.secondary)
        .accessibilityLabel("还有 \(count) 个会话")
    }
  }
}

private struct LockCard: View {
  let state: ActivityState

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      HStack(spacing: 10) {
        Mark(asset: state.harnessAsset, size: 20)
        Text(state.title).font(.headline).lineLimit(1)
        Spacer(minLength: 4)
        Others(count: state.others)
        PhaseMark(phase: state.phase)
      }
      Detail(state: state)
      if state.phase == .approval { ApprovalButtons() }
    }
    .padding(16)
  }
}

struct SessionActivityWidget: Widget {
  var body: some WidgetConfiguration {
    ActivityConfiguration(for: SessionActivityAttributes.self) { context in
      LockCard(state: context.state)
    } dynamicIsland: { context in
      let state = context.state
      return DynamicIsland {
        // One region for the whole content: the system gives separate leading,
        // center and trailing regions different heights, so they drift apart.
        DynamicIslandExpandedRegion(.bottom) {
          VStack(spacing: 10) {
            ZStack {
              Text(state.title)
                .font(.headline)
                .lineLimit(1)
                .padding(.horizontal, 64)
              HStack(spacing: 8) {
                Mark(asset: state.harnessAsset, size: 24)
                Spacer(minLength: 0)
                Others(count: state.others)
                PhaseMark(phase: state.phase)
              }
            }
            .frame(height: 24)
            Detail(state: state)
            if state.phase == .approval { ApprovalButtons() }
          }
          .padding(.horizontal, 6)
        }
      } compactLeading: {
        Mark(asset: state.harnessAsset, size: 18)
      } compactTrailing: {
        PhaseMark(phase: state.phase)
      } minimal: {
        PhaseMark(phase: state.phase)
      }
      .keylineTint(state.phase == .running ? Color.secondary : Color.needsYou)
    }
  }
}
