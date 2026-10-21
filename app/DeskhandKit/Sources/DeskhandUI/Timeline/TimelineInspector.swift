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

/// Timeline entries newest first, grouped by day, with loading, empty and error states.
struct ActivityPane: View {
    let store: AppStore
    let host: Host

    var body: some View {
        let entries = store.timeline(for: host.hostID)
        let error = store.timelineError(for: host.hostID)
        if !store.isTimelineLoaded(for: host.hostID) && error == nil {
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
                VStack(alignment: .leading, spacing: 0) {
                    if let error {
                        ErrorBanner(message: String.ui("timeline.failed", "활동 기록을 불러오지 못했습니다"),
                                    fix: error.detail,
                                    code: error.code,
                                    retry: { Task { await store.loadTimeline(for: host.hostID) } })
                            .padding(.bottom, DS.Space.l)
                    }
                    if entries.isEmpty {
                        if error == nil { empty }
                    } else {
                        ForEach(TimelineDay.group(entries)) { day in
                            Text(day.title)
                                .font(DS.Font.caption.weight(.semibold))
                                .foregroundStyle(.secondary)
                                .padding(.bottom, DS.Space.s)
                                .padding(.top, day.isFirst ? 0 : DS.Space.s)
                                .accessibilityAddTraits(.isHeader)
                            ForEach(Array(day.entries.enumerated()), id: \.element.seq) { index, entry in
                                TimelineRow(entry: entry, continuesRail: index < day.entries.count - 1)
                            }
                        }
                    }
                }
                .padding(DS.Space.l)
            }
        }
    }

    private var empty: some View {
        VStack(spacing: DS.Space.s) {
            Image(systemName: "clock.arrow.circlepath")
                .font(.system(size: 28, weight: .regular))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text(String.ui("timeline.empty.title", "아직 기록이 없습니다"))
                .font(DS.Font.headline)
            Text(String.ui("timeline.empty.message", "사람이나 AI가 이 컴퓨터를 조작하면 누가 무엇을 했는지 여기에 시간순으로 남습니다."))
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

/// Timeline entries of one calendar day, titled 오늘, 어제 or the date.
struct TimelineDay: Identifiable {
    let day: Date
    let title: String
    let isFirst: Bool
    let entries: [TimelineEntry]

    var id: Date { day }

    /// Groups newest-first `entries` by day, keeping their order.
    static func group(_ entries: [TimelineEntry], calendar: Calendar = .current) -> [TimelineDay] {
        var days: [(Date, [TimelineEntry])] = []
        for entry in entries {
            let day = calendar.startOfDay(for: entry.ts.date)
            if let last = days.last, last.0 == day {
                days[days.count - 1].1.append(entry)
            } else {
                days.append((day, [entry]))
            }
        }
        return days.enumerated().map { index, item in
            TimelineDay(day: item.0, title: title(for: item.0, calendar: calendar), isFirst: index == 0, entries: item.1)
        }
    }

    static func title(for day: Date, calendar: Calendar) -> String {
        DS.Format.day(day, calendar: calendar)
    }
}
