import AppKit
import DeskhandClient
import SwiftUI

/// An evidence file laid over the tab in the manner of Quick Look: a screenshot fitted to the
/// panel or a text file in a monospaced font, with its name, kind, size and step, the previous and
/// next evidence of the run (← →), Finder and open-with-app buttons, and Esc or Space to close.
struct EvidenceViewer: View {
    let model: QAModel
    let selection: QAEvidenceSelection
    @FocusState private var focused: Bool
    @Environment(\.colorScheme) private var colorScheme

    /// Every evidence file of the run in step order, with its step.
    private var items: [(step: QAStep, artifact: Artifact)] {
        (model.record(selection.runID)?.steps ?? []).flatMap { step in step.evidence.map { (step, $0) } }
    }

    private var position: Int? {
        items.firstIndex { $0.step.index == selection.stepIndex && $0.artifact.artifactID == selection.artifactID }
    }

    var body: some View {
        GeometryReader { proxy in
            ZStack {
                Color.black.opacity(colorScheme == .dark ? 0.45 : 0.28)
                    .contentShape(Rectangle())
                    .onTapGesture { close() }
                    .accessibilityHidden(true)
                if let position {
                    panel(items[position], position: position)
                        .frame(width: min(1120, proxy.size.width - DS.Space.xxl * 2), height: max(240, proxy.size.height - DS.Space.xxl * 2))
                }
            }
        }
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onAppear { focused = true }
        .onKeyPress(.escape) {
            close()
            return .handled
        }
        .onKeyPress(.space) {
            close()
            return .handled
        }
        .onKeyPress(.leftArrow) {
            step(by: -1)
            return .handled
        }
        .onKeyPress(.rightArrow) {
            step(by: 1)
            return .handled
        }
    }

    private func close() { model.viewer = nil }

    private func step(by delta: Int) {
        guard let position, !items.isEmpty else { return }
        let next = items[(position + delta + items.count) % items.count]
        model.viewer = QAEvidenceSelection(runID: selection.runID, stepIndex: next.step.index, artifactID: next.artifact.artifactID)
    }

    private func panel(_ item: (step: QAStep, artifact: Artifact), position: Int) -> some View {
        VStack(spacing: 0) {
            header(item, position: position)
            Rectangle().fill(DS.Color.separator).frame(height: 1)
            content(item.artifact)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(DS.Color.surface)
        .clipShape(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
        .shadow(color: .black.opacity(colorScheme == .dark ? 0.55 : 0.25), radius: 28, y: 14)
        .onAppear { model.evidence.load(item.artifact, size: .full) }
        .onChange(of: item.artifact.path) { _, _ in model.evidence.load(item.artifact, size: .full) }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("qa.viewer.a11y", "증거 보기: \(item.artifact.name)"))
    }

    private func header(_ item: (step: QAStep, artifact: Artifact), position: Int) -> some View {
        let artifact = item.artifact
        return HStack(spacing: DS.Space.m) {
            Image(systemName: QAEvidenceStore.isImage(artifact) ? "photo" : EvidenceChip.symbol(artifact.kind))
                .font(.system(size: 18))
                .foregroundStyle(DS.Status.neutralText)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(artifact.name)
                    .font(.system(size: 13, weight: .semibold, design: .monospaced))
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                Text(subtitle(item))
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
                    .lineLimit(1)
            }
            Spacer(minLength: DS.Space.m)
            if items.count > 1 {
                HStack(spacing: DS.Space.xs) {
                    navButton("chevron.left", String.ui("qa.viewer.previous", "이전 증거 (←)")) { step(by: -1) }
                    Text(String.ui("qa.viewer.position", "\(String(position + 1)) / \(String(items.count))"))
                        .font(.system(size: 11, weight: .semibold).monospacedDigit())
                        .foregroundStyle(DS.Status.neutralText)
                        .frame(minWidth: 40)
                    navButton("chevron.right", String.ui("qa.viewer.next", "다음 증거 (→)")) { step(by: 1) }
                }
            }
            Button(String.ui("qa.evidence.reveal", "Finder에서 보기")) { FinderReveal.reveal(path: artifact.path) }
                .help(artifact.path.abbreviatingHome)
            Button(String.ui("qa.evidence.openApp", "기본 앱으로 열기")) { NSWorkspace.shared.open(URL(fileURLWithPath: artifact.path)) }
            Button {
                close()
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(DS.Status.neutralText)
                    .frame(width: 24, height: 24)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(String.ui("qa.viewer.close", "닫기 (Esc)"))
            .accessibilityLabel(String.ui("action.close", "닫기"))
        }
        .padding(.horizontal, DS.Space.l)
        .frame(height: 56)
    }

    private func navButton(_ symbol: String, _ title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 11, weight: .semibold))
                .frame(width: 24, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(title)
        .accessibilityLabel(title)
    }

    private func subtitle(_ item: (step: QAStep, artifact: Artifact)) -> String {
        var parts = [EvidenceChip.kindTitle(item.artifact.kind)]
        if case .image(_, let pixels) = model.evidence.content(of: item.artifact, size: .full) {
            parts.append(String(Int(pixels.width)) + "×" + String(Int(pixels.height)))
        }
        parts.append(DS.Format.bytes(item.artifact.size))
        parts.append(String(item.step.index) + ". " + item.step.name)
        return parts.joined(separator: " · ")
    }

    @ViewBuilder
    private func content(_ artifact: Artifact) -> some View {
        switch model.evidence.content(of: artifact, size: .full) {
        case .image(let image, _):
            ZStack {
                DS.Color.screenBackdrop
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
                    .aspectRatio(contentMode: .fit)
                    .padding(DS.Space.l)
                    .accessibilityLabel(String.ui("qa.viewer.image", "스크린샷 \(artifact.name)"))
            }
        case .text(let text, let truncated):
            VStack(spacing: 0) {
                if truncated {
                    Label(String.ui("qa.viewer.truncated", "앞 512 KB만 보여 줍니다. 전체는 기본 앱으로 여세요."), systemImage: "scissors")
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Status.neutralText)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, DS.Space.l)
                        .padding(.vertical, DS.Space.s)
                        .background(DS.Color.warning.opacity(0.08))
                }
                EvidenceTextView(text: text)
            }
        case .failed(let error):
            DebugPanePlaceholder(symbol: "exclamationmark.triangle", title: String.ui("qa.viewer.failed", "증거 파일을 읽지 못했습니다"),
                                 message: error.message)
        case .loading, nil:
            DebugLoadingLine(text: String.ui("qa.viewer.loading", "증거 파일을 읽는 중…"))
        }
    }
}

/// Read-only selectable monospaced text in an `NSTextView` that scrolls both ways without wrapping.
struct EvidenceTextView: NSViewRepresentable {
    let text: String

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        scroll.hasHorizontalScroller = true
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = true
        scroll.backgroundColor = .textBackgroundColor
        if let view = scroll.documentView as? NSTextView {
            view.isEditable = false
            view.isSelectable = true
            view.isRichText = false
            view.drawsBackground = true
            view.backgroundColor = .textBackgroundColor
            view.textColor = .labelColor
            view.font = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
            view.textContainerInset = NSSize(width: DS.Space.l, height: DS.Space.l)
            view.isHorizontallyResizable = true
            view.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
            view.textContainer?.widthTracksTextView = false
            view.textContainer?.containerSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
            view.setAccessibilityLabel(String.ui("qa.viewer.text.a11y", "증거 파일 내용"))
        }
        update(scroll)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        update(scroll)
    }

    private func update(_ scroll: NSScrollView) {
        guard let view = scroll.documentView as? NSTextView, view.string != text else { return }
        view.string = text
        view.textColor = .labelColor
        view.font = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
    }
}
