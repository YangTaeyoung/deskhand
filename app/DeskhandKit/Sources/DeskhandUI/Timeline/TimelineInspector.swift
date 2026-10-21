import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Trailing inspector of the workspace (⌥⌘0): the timeline of human, AI and system actions,
/// and the computer's connection, session and capability details.
struct TimelineInspector: View {
    let store: AppStore
    let host: Host

    var body: some View {
        @Bindable var layout = store.layout
        VStack(spacing: 0) {
            HStack(spacing: DS.Space.xs) {
                ForEach(WorkspaceLayout.InspectorPane.allCases) { pane in
                    TabButton(title: pane.title, isSelected: layout.inspectorPane == pane) {
                        layout.inspectorPane = pane
                    }
                }
            }
            .padding(.horizontal, DS.Space.l)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(height: WorkspaceTabBar.height)
            .overlay(alignment: .bottom) {
                Rectangle()
                    .fill(DS.Color.separator)
                    .frame(height: 1)
            }
            .accessibilityElement(children: .contain)
            .accessibilityLabel(String.ui("inspector.pane", "인스펙터 패널"))
            switch layout.inspectorPane {
            case .activity:
                ActivityPane(store: store, host: host)
            case .info:
                HostInfoPane(store: store, host: host)
            }
        }
        .frame(maxHeight: .infinity, alignment: .top)
    }
}

/// Timeline entries newest first, grouped by day, with loading, empty and error states and a
/// button that reads older entries.
struct ActivityPane: View {
    let store: AppStore
    let host: Host

    var body: some View {
        let timeline = store.timelineState(for: host.hostID)
        let error = store.timelineError(for: host.hostID)
        if !(timeline?.loaded ?? false) && error == nil {
            VStack(spacing: DS.Space.s) {
                ProgressView()
                    .controlSize(.small)
                Text(String.ui("timeline.loading", "활동 기록을 불러오는 중"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.top, DS.Space.xxl)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .accessibilityElement(children: .combine)
        } else {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if let error {
                        ErrorBanner(message: String.ui("timeline.failed", "활동 기록을 불러오지 못했습니다"),
                                    fix: error.sentences,
                                    code: error.code,
                                    retry: { Task { await store.loadTimeline(for: host.hostID) } })
                            .padding(.bottom, DS.Space.l)
                    }
                    if let timeline, !timeline.entries.isEmpty {
                        ForEach(timeline.days) { day in
                            Text(DS.Format.day(day.day))
                                .font(DS.Font.caption.weight(.semibold))
                                .foregroundStyle(.secondary)
                                .padding(.bottom, DS.Space.s)
                                .padding(.top, day.isFirst ? 0 : DS.Space.s)
                                .accessibilityAddTraits(.isHeader)
                            ForEach(day.rows) { row in
                                TimelineRow(entry: row.entry, continuesRail: row.continuesRail, evidence: store.qa.evidence)
                            }
                        }
                        if timeline.hasOlder {
                            OlderTimelineFooter(timeline: timeline) {
                                Task { await store.loadOlderTimeline(for: host.hostID) }
                            }
                        }
                    } else if error == nil {
                        empty
                    }
                }
                .padding(DS.Space.l)
            }
        }
    }

    private var empty: some View {
        VStack(spacing: DS.Space.s) {
            Image(systemName: "clock.arrow.circlepath")
                .font(.title)
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text(String.ui("timeline.empty.title", "아직 기록이 없습니다"))
                .font(DS.Font.headline)
            Text(String.ui("timeline.empty.message", "사람이나 AI가 이 PC를 조작하면 누가 무엇을 했는지 여기에 시간순으로 남습니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, DS.Space.xl)
        .accessibilityElement(children: .combine)
    }
}

/// `이전 기록 더 보기` under the oldest entry, a spinner while the page loads, and why it failed.
private struct OlderTimelineFooter: View {
    let timeline: HostTimeline
    let load: () -> Void
    private static let pageSize = String(HostTimeline.pageSize)

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            if timeline.isLoadingOlder {
                HStack(spacing: DS.Space.s) {
                    ProgressView()
                        .controlSize(.small)
                    Text(String.ui("timeline.older.loading", "이전 기록을 불러오는 중"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
            } else {
                Button(action: load) {
                    Label(String.ui("timeline.older", "이전 기록 더 보기"), systemImage: "clock.arrow.circlepath")
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .help(String.ui("timeline.older.help", "이보다 오래된 기록을 \(Self.pageSize)개 더 불러옵니다"))
            }
            if let error = timeline.olderError {
                Label(error.sentences, systemImage: "exclamationmark.triangle.fill")
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.warningText)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.leading, ActorBadge.size + DS.Space.m)
        .padding(.top, DS.Space.xs)
    }
}
