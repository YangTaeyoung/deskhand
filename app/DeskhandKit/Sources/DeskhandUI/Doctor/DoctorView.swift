import DeskhandClient
import SwiftUI

/// Diagnostics sheet: the `host.doctor` checks with pass, fail, warning and skipped states,
/// each with its detail and a copyable fix. The sheet grows with the checks up to
/// `maxListHeight`; beyond that the list scrolls and a fade marks the hidden rows.
public struct DoctorView: View {
    let model: DoctorModel
    let onClose: () -> Void

    public static let width: CGFloat = 640
    static let maxListHeight: CGFloat = 480
    static let placeholderHeight: CGFloat = 280
    nonisolated private static let listSpace = "doctorList"

    @State private var listHeight: CGFloat = 0
    @State private var rowBottoms: [Int: CGFloat] = [:]
    @State private var scrollOffset: CGFloat = 0

    /// Rows whose bottom edge is below the visible part of the list.
    private var hiddenRowIndices: [Int] {
        rowBottoms.filter { $0.value > scrollOffset + visibleListHeight + 1 }.keys.sorted()
    }

    /// Height of the visible list: all of it when it fits, otherwise up to the last row that
    /// fits completely under `maxListHeight`.
    private var visibleListHeight: CGFloat {
        guard listHeight > Self.maxListHeight else { return max(listHeight, 1) }
        return rowBottoms.values.filter { $0 <= Self.maxListHeight }.max() ?? Self.maxListHeight
    }

    public init(model: DoctorModel, onClose: @escaping () -> Void) {
        self.model = model
        self.onClose = onClose
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding([.horizontal, .top], DS.Space.xl)
                .padding(.bottom, DS.Space.l)
            content
                .frame(maxWidth: .infinity, alignment: .top)
            Divider()
            HStack {
                if let finishedAt = model.finishedAt, !model.isRunning {
                    Text(String.ui("doctor.finishedAt", "마지막 진단 \(DS.Format.clock(finishedAt))"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button(String.ui("action.close", "닫기"), action: onClose)
                    .keyboardShortcut(.cancelAction)
                    .controlSize(.large)
            }
            .padding(.horizontal, DS.Space.xl)
            .padding(.vertical, DS.Space.l)
        }
        .frame(width: Self.width)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .fill(DS.Color.accent.gradient)
                .frame(width: 40, height: 40)
                .overlay {
                    Image(systemName: "stethoscope")
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundStyle(.white)
                }
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(String.ui("doctor.title", "\(model.hostName) 진단"))
                    .font(DS.Font.title)
                    .lineLimit(1)
                Text(String.ui("doctor.subtitle", "SSH, 에이전트, 보안 연결, 세션과 방화벽을 차례로 확인합니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            .accessibilityAddTraits(.isHeader)
            Spacer(minLength: DS.Space.l)
            Button {
                model.run()
            } label: {
                Label(String.ui("doctor.rerun", "다시 진단"), systemImage: "arrow.clockwise")
            }
            .disabled(model.isRunning)
            .keyboardShortcut("r")
        }
    }

    @ViewBuilder
    private var content: some View {
        switch model.status {
        case .loading:
            VStack(spacing: DS.Space.m) {
                ProgressView()
                    .controlSize(.regular)
                Text(String.ui("doctor.loading", "진단하는 중…"))
                    .font(DS.Font.headline)
                Text(String.ui("doctor.loading.detail", "원격 컴퓨터에 따라 몇 초 걸릴 수 있습니다."))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity)
            .frame(height: Self.placeholderHeight)
            .accessibilityElement(children: .combine)
        case .failed(let error):
            ErrorBanner(message: String.ui("doctor.failed", "진단을 실행하지 못했습니다"),
                        fix: [error.message, error.fix].compactMap { $0 }.joined(separator: " "),
                        code: error.code,
                        retry: { model.run() })
                .padding(.horizontal, DS.Space.xl)
                .padding(.bottom, DS.Space.xl)
        case .loaded(let checks) where checks.isEmpty:
            EmptyStateView(symbol: "stethoscope",
                           title: String.ui("doctor.empty.title", "진단 항목이 없습니다"),
                           message: String.ui("doctor.empty.message", "데몬이 이 컴퓨터에 대해 확인할 항목을 돌려주지 않았습니다."))
                .frame(maxWidth: .infinity)
                .frame(height: Self.placeholderHeight)
        case .loaded(let unordered):
            let checks = DoctorModel.problemsFirst(unordered)
            VStack(alignment: .leading, spacing: DS.Space.m) {
                DoctorSummary(checks: checks)
                    .padding(.horizontal, DS.Space.xl)
                ScrollViewReader { proxy in
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(Array(checks.enumerated()), id: \.offset) { index, check in
                            DoctorRow(check: check)
                                .id(index)
                                .onGeometryChange(for: CGFloat.self) {
                                    $0.frame(in: .named(Self.listSpace)).maxY
                                } action: { rowBottoms[index] = $0 }
                            if index < checks.count - 1 {
                                Divider().padding(.leading, DS.Space.xxl + DS.Space.l)
                            }
                        }
                    }
                    .dsCard(padding: 0)
                    .padding(.horizontal, DS.Space.xl)
                    .padding(.bottom, DS.Space.l)
                    .coordinateSpace(.named(Self.listSpace))
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { listHeight = $0 }
                }
                .scrollIndicators(.visible)
                .frame(height: visibleListHeight)
                .onScrollGeometryChange(for: CGFloat.self) { $0.contentOffset.y } action: { _, offset in
                    scrollOffset = offset
                }
                .overlay(alignment: .bottom) {
                    if !hiddenRowIndices.isEmpty {
                        LinearGradient(colors: [DS.Color.surface.opacity(0), DS.Color.surface],
                                       startPoint: .top, endPoint: .bottom)
                            .frame(height: DS.Space.m)
                            .allowsHitTesting(false)
                            .accessibilityHidden(true)
                    }
                }
                if let next = hiddenRowIndices.first {
                    Button {
                        withAnimation { proxy.scrollTo(next, anchor: .bottom) }
                    } label: {
                        Label(String.ui("doctor.more", "아래에 \(hiddenRowIndices.count)개 더"), systemImage: "chevron.down")
                            .font(DS.Font.caption.weight(.semibold))
                            .padding(.horizontal, DS.Space.m)
                            .padding(.vertical, DS.Space.xs)
                            .background(DS.Color.surfaceRaised, in: Capsule())
                            .overlay(Capsule().strokeBorder(DS.Color.separator))
                            .contentShape(Capsule())
                    }
                    .buttonStyle(.plain)
                    .frame(maxWidth: .infinity)
                    .padding(.bottom, DS.Space.m)
                    .help(String.ui("help.doctorMore", "다음 항목으로 스크롤합니다"))
                }
                }
            }
        }
    }
}

/// Counts per status, problems first.
struct DoctorSummary: View {
    let checks: [DoctorCheck]

    var body: some View {
        let counts = Dictionary(grouping: checks, by: \.doctorStatus).mapValues(\.count)
        HStack(spacing: DS.Space.s) {
            if counts[.fail] == nil && counts[.warn] == nil {
                Label(String.ui("doctor.allPassed", "모든 항목을 통과했습니다"), systemImage: DoctorStatus.pass.symbol)
                    .font(DS.Font.headline)
                    .foregroundStyle(DS.Status.successText)
            }
            ForEach([DoctorStatus.fail, .warn, .pass, .skip], id: \.self) { status in
                if let count = counts[status] {
                    HStack(spacing: DS.Space.xs) {
                        Image(systemName: status.symbol)
                            .foregroundStyle(status.tint)
                            .imageScale(.small)
                        Text(String.ui("doctor.count", "\(status.title) \(count)"))
                            .monospacedDigit()
                    }
                    .font(DS.Font.caption.weight(.medium))
                    .padding(.horizontal, DS.Space.s)
                    .padding(.vertical, DS.Space.xs)
                    .background(status.tint.opacity(0.12), in: Capsule())
                    .accessibilityElement(children: .combine)
                }
            }
            Spacer()
        }
    }
}

/// One check: status symbol, title, detail and, when the check has one, the fix with a copy button.
struct DoctorRow: View {
    let check: DoctorCheck
    @State private var copied = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Image(systemName: check.doctorStatus.symbol)
                .foregroundStyle(check.doctorStatus.tint)
                .font(DS.Font.headline)
                .frame(width: DS.Space.l)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
                    Text(check.title)
                        .font(DS.Font.headline)
                        .layoutPriority(1)
                    if isCompact {
                        Text(check.detail)
                            .font(DS.Font.body)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                            .help(check.detail)
                    }
                    Spacer(minLength: DS.Space.s)
                    statusBadge
                }
                if !isCompact, !check.detail.isEmpty {
                    Text(check.detail)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let fix = check.fix, !fix.isEmpty {
                    fixBox(fix)
                        .padding(.top, DS.Space.xs)
                }
            }
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("a11y.doctorRow", "\(check.title), \(check.doctorStatus.title)"))
    }

    /// Passed checks without a fix take one line: title, detail and badge.
    private var isCompact: Bool {
        check.doctorStatus == .pass && (check.fix ?? "").isEmpty
    }

    private var statusBadge: some View {
        Text(check.doctorStatus.title)
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(.primary)
            .padding(.horizontal, DS.Space.s)
            .padding(.vertical, DS.Space.xs)
            .background(check.doctorStatus.tint.opacity(0.2), in: Capsule())
    }

    private func fixBox(_ fix: String) -> some View {
        HStack(alignment: .top, spacing: DS.Space.s) {
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(String.ui("doctor.fix", "해결 방법"))
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Text(fix)
                    .font(DoctorModel.looksLikeCommand(fix) ? DS.Font.mono : DS.Font.body)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            Button {
                Pasteboard.copy(fix)
                copied = true
                Task {
                    try? await Task.sleep(for: .seconds(1.5))
                    copied = false
                }
            } label: {
                Label(copied ? String.ui("action.copied", "복사됨") : String.ui("action.copy", "복사"),
                      systemImage: copied ? "checkmark" : "doc.on.doc")
                    .frame(minWidth: 56)
            }
            .controlSize(.small)
            .help(String.ui("help.copyFix", "해결 방법을 클립보드에 복사합니다"))
            .accessibilityLabel(String.ui("a11y.copyFix", "\(check.title) 해결 방법 복사"))
        }
        .padding(DS.Space.m)
        .background(DS.Color.surface, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
    }
}
