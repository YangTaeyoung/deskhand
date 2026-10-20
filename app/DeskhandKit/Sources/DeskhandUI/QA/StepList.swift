import DeskhandClient
import SwiftUI

/// Steps grouped by phase under phase titles; ↑ and ↓ move the selection. A list that fits stays at
/// its top. A longer one scrolls when it appears and when the run moves on, unless the person chose
/// a step, so that whole rows and phase titles — the step's own phase title when it is close — sit
/// above the focused step, a row or title at the top edge; space under the last step lets any of
/// them reach the top.
struct StepList: View {
    let model: QAModel
    let run: QARun
    let steps: [QAStep]
    @FocusState private var focused: Bool
    @State private var height: CGFloat = 0

    static let headerHeight: CGFloat = 30

    /// The height of a row: two lines when it has an outcome line, one otherwise.
    static func rowHeight(_ step: QAStep) -> CGFloat {
        let line = !(step.message ?? "").isEmpty || [.blocked, .fail, .running].contains(step.stepStatus)
        return line ? 47 : 35
    }

    static func contentHeight(_ groups: [(phase: QAStepPhase, steps: [QAStep])]) -> CGFloat {
        groups.reduce(0) { total, group in total + headerHeight + group.steps.reduce(0) { $0 + rowHeight($1) } }
    }

    /// The steps of each phase, in phase order.
    private var groups: [(phase: QAStepPhase, steps: [QAStep])] {
        QAStepPhase.allCases.compactMap { phase in
            let group = steps.filter { ($0.stepPhase ?? .steps) == phase }
            return group.isEmpty ? nil : (phase, group)
        }
    }

    /// What to put at the top edge so that `index` shows: its phase title when it fits below the
    /// title in `height`, otherwise the row two steps above it in its phase.
    /// What to put at the top edge so that `index` shows with context above it: the first title of a
    /// list that fits in `height`; otherwise the earliest row or title whose rows and titles down to
    /// the step fill no more than 40% of `height`, or the step's phase title when that leaves the
    /// title out and the title is within 60% of `height` above the step.
    static func scrollTarget(for index: Int, in groups: [(phase: QAStepPhase, steps: [QAStep])], height: CGFloat) -> String? {
        var items: [(id: String, height: CGFloat)] = []
        for group in groups {
            items.append((headerID(group.phase), headerHeight))
            for step in group.steps { items.append((rowID(step.index), rowHeight(step))) }
        }
        guard let focus = items.firstIndex(where: { $0.id == rowID(index) }) else { return nil }
        if contentHeight(groups) <= height { return items.first?.id }
        var top = focus
        var above: CGFloat = 0
        while top > 0, above + items[top - 1].height <= height * 0.4 {
            above += items[top - 1].height
            top -= 1
        }
        if let phase = groups.first(where: { $0.steps.contains { $0.index == index } })?.phase,
           let header = items.firstIndex(where: { $0.id == headerID(phase) }), header < top,
           items[header..<focus].reduce(0, { $0 + $1.height }) <= height * 0.6 {
            return items[header].id
        }
        return items[top].id
    }

    static func headerID(_ phase: QAStepPhase) -> String { "phase-" + phase.rawValue }
    static func rowID(_ index: Int) -> String { "step-" + String(index) }

    var body: some View {
        let focusIndex = model.focusedStep(of: run.runID)?.index
        let groups = groups
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(groups, id: \.phase) { group in
                        StepPhaseHeader(phase: group.phase, steps: group.steps)
                            .id(Self.headerID(group.phase))
                        ForEach(group.steps) { step in
                            StepRow(step: step, selected: step.index == focusIndex)
                                .id(Self.rowID(step.index))
                                .contentShape(Rectangle())
                                .onTapGesture {
                                    focused = true
                                    model.selectStep(step.index, of: run.runID)
                                }
                                .accessibilityAction { model.selectStep(step.index, of: run.runID) }
                                .padding(.horizontal, DS.Space.s)
                                .padding(.bottom, 1)
                        }
                    }
                    Color.clear
                        .frame(height: Self.contentHeight(groups) > height ? max(0, height - Self.headerHeight - 47) : DS.Space.s)
                        .accessibilityHidden(true)
                }
            }
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
            .focusable()
            .focused($focused)
            .focusEffectDisabled()
            .onKeyPress(.upArrow) {
                move(by: -1, proxy: proxy)
                return .handled
            }
            .onKeyPress(.downArrow) {
                move(by: 1, proxy: proxy)
                return .handled
            }
            .onChange(of: run.currentStep) { _, current in
                guard model.selectedSteps[run.runID] == nil, let current,
                      let target = Self.scrollTarget(for: current, in: groups, height: height) else { return }
                withAnimation(.snappy(duration: 0.2)) { proxy.scrollTo(target, anchor: .top) }
            }
            .onChange(of: height) { old, new in
                guard old == 0, new > 0, let focusIndex, let target = Self.scrollTarget(for: focusIndex, in: groups, height: new) else { return }
                Task { @MainActor in
                    await Task.yield()
                    proxy.scrollTo(target, anchor: .top)
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("qa.steps.a11y", "단계 목록"))
    }

    private func move(by delta: Int, proxy: ScrollViewProxy) {
        let order = groups.flatMap(\.steps).map(\.index)
        guard !order.isEmpty else { return }
        let current = model.focusedStep(of: run.runID)?.index
        let position = order.firstIndex { $0 == current } ?? (delta > 0 ? -1 : order.count)
        let next = order[min(max(position + delta, 0), order.count - 1)]
        model.selectStep(next, of: run.runID)
        proxy.scrollTo(Self.rowID(next))
    }
}
