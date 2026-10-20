import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The runs of the scope, newest first: a search field and a status filter over rows with the
/// status badge, name, start time, step counts, duration and, for every computer's list, the
/// computer. ↑ and ↓ move the selection; older runs load at the end.
struct RunList: View {
    let store: AppStore
    @Bindable var model: QAModel
    let scope: QAScope
    /// Called after a row is chosen.
    var chosen: () -> Void = {}
    @FocusState private var focused: Bool

    private func hostName(_ hostID: String) -> String? { store.host(hostID)?.name }

    var body: some View {
        let runs = model.visibleRuns(in: scope, hostName: hostName)
        VStack(spacing: 0) {
            header
            Rectangle().fill(DS.Color.separator).frame(height: 1)
            if runs.isEmpty {
                filteredOut
            } else {
                rows(runs)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("qa.list.a11y", "QA 실행 목록"))
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            HStack(spacing: DS.Space.s) {
                HStack(spacing: DS.Space.xs) {
                    Image(systemName: "magnifyingglass")
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(DS.Status.neutralText)
                        .accessibilityHidden(true)
                    TextField(String.ui("qa.search.placeholder", "이름·시나리오·실행 ID"), text: $model.search)
                        .textFieldStyle(.plain)
                        .font(DS.Font.body)
                        .accessibilityLabel(String.ui("qa.search", "실행 검색"))
                    if !model.search.isEmpty {
                        Button {
                            model.search = ""
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.tertiary)
                        }
                        .buttonStyle(.plain)
                        .help(String.ui("qa.search.clear", "검색어 지우기"))
                        .accessibilityLabel(String.ui("qa.search.clear", "검색어 지우기"))
                    }
                }
                .padding(.horizontal, DS.Space.s)
                .frame(height: 26)
                .background(DS.Color.neutral.opacity(0.1), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                filterMenu
            }
            if let error = model.listing(scope).reloadError {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.xs) {
                    Image(systemName: "exclamationmark.arrow.circlepath")
                        .foregroundStyle(DS.Status.warningText)
                        .accessibilityHidden(true)
                    Text(String.ui("qa.list.reloadFailed", "목록을 새로 읽지 못했습니다"))
                        .font(DS.Font.caption.weight(.semibold))
                        .help(error.message)
                    Button(String.ui("action.retry", "다시 시도")) { Task { await model.load(scope) } }
                        .buttonStyle(.link)
                        .tint(DS.Color.accent)
                        .font(DS.Font.caption)
                    Spacer(minLength: 0)
                }
                .accessibilityElement(children: .combine)
            }
            HStack(spacing: DS.Space.xs) {
                Text(countText)
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
                if model.filter != .all {
                    Button {
                        model.filter = .all
                    } label: {
                        HStack(spacing: 2) {
                            Text(model.filter.title)
                            Image(systemName: "xmark")
                                .font(.system(size: 8, weight: .bold))
                        }
                        .font(DS.Font.caption.weight(.semibold))
                        .foregroundStyle(DS.Status.accentText)
                        .padding(.horizontal, DS.Space.s)
                        .frame(height: 18)
                        .background(DS.Color.accent.opacity(DS.Status.badgeOpacity), in: Capsule())
                    }
                    .buttonStyle(.plain)
                    .help(String.ui("qa.filter.clear", "상태 필터 지우기"))
                    .accessibilityLabel(String.ui("qa.filter.clear.a11y", "상태 필터 \(model.filter.title) 지우기"))
                }
                Spacer(minLength: 0)
            }
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.top, DS.Space.s)
        .padding(.bottom, DS.Space.s)
    }

    private var countText: String {
        let shown = model.visibleRuns(in: scope, hostName: hostName).count
        let total = model.runs(in: scope).count
        let more = model.listing(scope).nextCursor != nil
        let shownText = String(shown)
        let totalText = String(total)
        if shown == total {
            return more ? .ui("qa.list.countMore", "실행 \(totalText)개 · 더 있음") : .ui("qa.list.count", "실행 \(totalText)개")
        }
        return .ui("qa.list.countFiltered", "실행 \(totalText)개 중 \(shownText)개")
    }

    private var filterMenu: some View {
        Menu {
            Picker(String.ui("qa.filter", "상태"), selection: $model.filter) {
                ForEach(QARunFilter.allCases) { filter in
                    Text(filter.title).tag(filter)
                }
            }
            .pickerStyle(.inline)
        } label: {
            Image(systemName: model.filter == .all ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
                .font(.system(size: 14))
                .foregroundStyle(model.filter == .all ? AnyShapeStyle(DS.Status.neutralText) : AnyShapeStyle(DS.Color.accent))
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(String.ui("qa.filter.help", "상태로 거르기"))
        .accessibilityLabel(String.ui("qa.filter.a11y", "상태 필터"))
        .accessibilityValue(model.filter.title)
    }

    @ViewBuilder
    private var filteredOut: some View {
        VStack(spacing: DS.Space.m) {
            DebugPanePlaceholder(symbol: "line.3.horizontal.decrease.circle", title: String.ui("qa.list.noMatch", "조건에 맞는 실행이 없습니다"),
                                 message: String.ui("qa.list.noMatch.detail", "검색어나 상태 필터를 바꿔 보세요."))
                .frame(maxHeight: 180)
            Button(String.ui("qa.list.clearFilters", "검색과 필터 지우기")) {
                model.search = ""
                model.filter = .all
            }
            .buttonStyle(.link)
            .tint(DS.Color.accent)
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func rows(_ runs: [QARun]) -> some View {
        let selected = model.selectedRun(in: scope)?.runID
        return ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 2) {
                    ForEach(runs) { run in
                        RunRow(run: run, counts: QAStepCounts(run: run, steps: model.record(run.runID)?.steps),
                               hostName: scope == .all ? hostName(run.hostID) : nil, selected: run.runID == selected)
                            .id(run.runID)
                            .contentShape(Rectangle())
                            .onTapGesture {
                                focused = true
                                model.select(run.runID, in: scope)
                                chosen()
                            }
                            .accessibilityAction {
                                model.select(run.runID, in: scope)
                                chosen()
                            }
                    }
                    footer
                }
                .padding(DS.Space.s)
            }
            .focusable()
            .focused($focused)
            .focusEffectDisabled()
            .onKeyPress(.upArrow) {
                move(runs, by: -1, proxy: proxy)
                return .handled
            }
            .onKeyPress(.downArrow) {
                move(runs, by: 1, proxy: proxy)
                return .handled
            }
        }
    }

    private func move(_ runs: [QARun], by delta: Int, proxy: ScrollViewProxy) {
        guard !runs.isEmpty else { return }
        let current = model.selectedRun(in: scope)?.runID
        let index = runs.firstIndex { $0.runID == current } ?? (delta > 0 ? -1 : runs.count)
        let next = runs[min(max(index + delta, 0), runs.count - 1)].runID
        model.select(next, in: scope)
        proxy.scrollTo(next)
    }

    @ViewBuilder
    private var footer: some View {
        let listing = model.listing(scope)
        if listing.nextCursor != nil {
            Group {
                if listing.loadingMore {
                    DebugLoadingLine(text: String.ui("qa.list.loadingMore", "이전 실행을 불러오는 중…"))
                } else if let error = listing.moreError {
                    VStack(spacing: DS.Space.xs) {
                        Text(String.ui("qa.list.moreFailed", "이전 실행을 불러오지 못했습니다"))
                            .font(DS.Font.caption.weight(.semibold))
                        Text(error.message)
                            .font(DS.Font.caption)
                            .foregroundStyle(DS.Status.neutralText)
                            .multilineTextAlignment(.center)
                        Button(String.ui("action.retry", "다시 시도")) { Task { await model.loadMore(scope) } }
                            .buttonStyle(.link)
                            .tint(DS.Color.accent)
                    }
                } else {
                    Button(String.ui("qa.list.more", "이전 실행 더 보기")) { Task { await model.loadMore(scope) } }
                        .buttonStyle(.link)
                        .tint(DS.Color.accent)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, DS.Space.m)
        }
    }
}

/// One run: the status badge, name and start time over the step counts or progress, the
/// duration and, in every computer's list, the computer. A selected row is filled with the
/// selection color and draws everything in white.
struct RunRow: View {
    let run: QARun
    let counts: QAStepCounts
    /// The computer, shown in every computer's list.
    let hostName: String?
    let selected: Bool
    @State private var hovering = false

    private var secondary: AnyShapeStyle { selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText) }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            HStack(spacing: DS.Space.s) {
                QAStatusBadge(run: run, inverted: selected)
                Text(run.name)
                    .font(DS.Font.body.weight(.semibold))
                    .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.primary))
                    .lineLimit(1)
                    .truncationMode(.tail)
                Spacer(minLength: DS.Space.xs)
                Text(QAFormat.started(run.startedAt.date))
                    .font(DS.Font.caption)
                    .foregroundStyle(secondary)
                    .monospacedDigit()
                    .lineLimit(1)
                    .fixedSize()
            }
            HStack(spacing: DS.Space.s) {
                if run.isRunning {
                    RunProgress(run: run, inverted: selected)
                        .layoutPriority(2)
                } else {
                    StepTally(counts: counts, inverted: selected)
                        .layoutPriority(2)
                }
                Spacer(minLength: DS.Space.xs)
                if let hostName {
                    Label(hostName, systemImage: "desktopcomputer")
                        .labelStyle(.titleAndIcon)
                        .font(DS.Font.caption)
                        .foregroundStyle(secondary)
                        .lineLimit(1)
                    Text("·")
                        .font(DS.Font.caption)
                        .foregroundStyle(secondary)
                        .accessibilityHidden(true)
                }
                RunDuration(run: run)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(secondary)
                    .lineLimit(1)
                    .fixedSize()
            }
            .padding(.leading, QAStatusBadge.width + DS.Space.s)
        }
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.s)
        .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .onHover { hovering = $0 }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : [.isButton])
    }

    private var background: AnyShapeStyle {
        if selected { return AnyShapeStyle(DS.Status.selectionFill) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.08)) : AnyShapeStyle(.clear)
    }

    private var accessibilityText: String {
        let status = QAStatusBadge.title(run)
        let started = QAFormat.started(run.startedAt.date)
        let tally = StepTally.spoken(counts)
        var text = String.ui("qa.row.a11y", "\(run.name), \(status), \(started) 시작, \(tally)")
        if let hostName { text += ", " + hostName }
        return text
    }
}

/// A run's status or a step's status as a tinted capsule with its symbol, of one width. On a
/// selected row it draws white on an outline instead.
struct QAStatusBadge: View {
    static let width: CGFloat = 64

    let title: String
    let symbol: String
    let tint: Color
    let text: Color
    var spinning = false
    var large = false
    var inverted = false

    /// 취소하는 중 while a running run has taken a cancel, otherwise the run's status.
    static func title(_ run: QARun) -> String {
        run.isRunning && run.cancelRequested ? .ui("qa.status.cancelling", "취소하는 중") : QAFormat.title(run.runStatus)
    }

    init(run: QARun, large: Bool = false, inverted: Bool = false) {
        let cancelling = run.isRunning && run.cancelRequested
        title = Self.title(run)
        symbol = QAFormat.symbol(run.runStatus)
        tint = cancelling ? DS.Color.neutral : QAFormat.tint(run.runStatus)
        text = cancelling ? DS.Status.neutralText : QAFormat.text(run.runStatus)
        spinning = run.isRunning
        self.large = large
        self.inverted = inverted
    }

    init(step status: QAStepStatus?, large: Bool = false) {
        title = QAFormat.title(status)
        symbol = QAFormat.symbol(status)
        tint = QAFormat.tint(status)
        text = QAFormat.text(status)
        spinning = status == .running
        self.large = large
    }

    init(step: QAStep, large: Bool = false) {
        title = QAFormat.title(step: step)
        symbol = StepRow.isHumanControl(step) ? "hand.raised.fill" : QAFormat.symbol(step: step)
        tint = StepRow.isHumanControl(step) ? DS.Color.human : QAFormat.tint(step: step)
        text = StepRow.isHumanControl(step) ? DS.Status.humanText : QAFormat.text(step: step)
        spinning = step.stepStatus == .running
        self.large = large
    }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            if spinning {
                ProgressView()
                    .controlSize(.mini)
                    .frame(width: 10, height: 10)
            } else {
                Image(systemName: symbol)
                    .font(.system(size: large ? 11 : 9, weight: .bold))
            }
            Text(title)
                .font(.system(size: large ? 12 : 11, weight: .semibold))
                .lineLimit(1)
        }
        .foregroundStyle(inverted ? DS.Color.onFill : text)
        .padding(.horizontal, DS.Space.s)
        .frame(minWidth: large ? nil : Self.width)
        .frame(height: large ? 22 : 18)
        .background(inverted ? AnyShapeStyle(.clear) : AnyShapeStyle(tint.opacity(DS.Status.badgeOpacity)), in: Capsule())
        .overlay {
            if inverted { Capsule().strokeBorder(DS.Color.onFill.opacity(0.7), lineWidth: 1) }
        }
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
    }
}

/// A thin bar of the steps that ended and `5/12단계`, on one line.
struct RunProgress: View {
    let run: QARun
    var inverted = false

    private var done: Int { run.stepsPassed + run.stepsFailed + run.stepsBlocked + run.stepsNotRun }

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Capsule()
                .fill(inverted ? DS.Color.onFill.opacity(0.3) : DS.Color.neutral.opacity(0.22))
                .frame(width: 64, height: 4)
                .overlay(alignment: .leading) {
                    Capsule()
                        .fill(inverted ? DS.Color.onFill : DS.Color.accent)
                        .frame(width: 64 * CGFloat(done) / CGFloat(max(run.stepsTotal, 1)), height: 4)
                }
            Text(String.ui("qa.progress", "\(String(done))/\(String(run.stepsTotal))단계"))
                .font(.system(size: 11, weight: .semibold).monospacedDigit())
                .foregroundStyle(inverted ? DS.Color.onFill : DS.Status.accentText)
                .lineLimit(1)
                .fixedSize()
        }
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String.ui("qa.progress.a11y", "\(String(run.stepsTotal))단계 중 \(String(done))단계 끝남"))
    }
}

/// How long a run took, or has been running, updated every second while it runs.
struct RunDuration: View {
    let run: QARun

    var body: some View {
        if run.isRunning {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                Text(QAFormat.duration(from: run.startedAt.date, to: max(context.date, run.startedAt.date)))
            }
        } else {
            Text(QAFormat.duration(from: run.startedAt.date, to: (run.finishedAt ?? run.updatedAt).date))
        }
    }
}
