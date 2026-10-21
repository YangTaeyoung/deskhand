import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Rounded search field with a clear button.
struct SearchField: View {
    @Binding var text: String
    let prompt: String
    var focused: FocusState<Bool>.Binding

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(DS.Status.neutralText)
                .imageScale(.small)
                .accessibilityHidden(true)
            TextField(prompt, text: $text)
                .textFieldStyle(.plain)
                .focused(focused)
                .accessibilityLabel(prompt)
                .onKeyPress(.escape) {
                    guard !text.isEmpty else { return .ignored }
                    text = ""
                    return .handled
                }
            if !text.isEmpty {
                Button {
                    text = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(DS.Status.neutralText)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(String.ui("files.search.clear", "검색어 지우기"))
            }
        }
        .font(DS.Font.body)
        .padding(.horizontal, DS.Space.s)
        .frame(height: 24)
        .background(DS.Color.neutral.opacity(0.12), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .accessibilityElement(children: .contain)
    }
}

/// Back, forward, up, places and the breadcrumb of the open folder; a field to type a path.
struct PathBar: View {
    let model: FilesModel
    let side: FileSide
    let host: Host
    @State private var typing = false
    @State private var typed = ""

    private var browser: FileBrowser { model.browser(side) }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Group {
                iconButton("chevron.left", String.ui("files.back", "뒤로 (⌘[)"), disabled: browser.backStack.isEmpty) {
                    Task { await model.goBack(side) }
                }
                iconButton("chevron.right", String.ui("files.forward", "앞으로 (⌘])"), disabled: browser.forwardStack.isEmpty) {
                    Task { await model.goForward(side) }
                }
                iconButton("arrow.up", String.ui("files.up", "상위 폴더 (⌘↑)"), disabled: browser.parentPath == nil) {
                    Task { await model.goUp(side) }
                }
            }
            placesMenu
            crumbs
            iconButton("arrow.right.circle", String.ui("files.goTo", "경로로 이동"), disabled: false) {
                typed = browser.path
                typing = true
            }
            .popover(isPresented: $typing, arrowEdge: .bottom) { goToField }
        }
        .padding(.horizontal, DS.Space.s)
        .frame(height: 32)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    private func iconButton(_ symbol: String, _ label: String, disabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .imageScale(.medium)
                .frame(width: 24, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .disabled(disabled)
        .help(label)
        .accessibilityLabel(label)
    }

    private var placesMenu: some View {
        let places = side == .local ? LocalFileSource.places : RemoteFileSource.places(for: host)
        let roots = side == .remote ? model.preferences.workRoots(for: host.hostID) : []
        return Menu {
            ForEach(places) { place in
                Button(place.title) { Task { await model.open(side, path: place.path) } }
            }
            if !roots.isEmpty {
                Section(String.ui("files.places.workRoots", "작업 루트")) {
                    ForEach(roots, id: \.self) { root in
                        Button(root) { Task { await model.open(side, path: root) } }
                    }
                }
            }
        } label: {
            Image(systemName: side == .local ? "house" : "externaldrive")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(String.ui("files.places", "자주 쓰는 위치"))
        .accessibilityLabel(String.ui("files.places", "자주 쓰는 위치"))
    }

    /// The crumbs that fit, trailing ones first; the leading crumbs that do not fit fold into a
    /// `…` menu, so no crumb is cut.
    private var crumbs: some View {
        let crumbs = browser.crumbs
        return ViewThatFits(in: .horizontal) {
            ForEach(CrumbLayout.candidates(crumbs), id: \.folded.count) { candidate in
                crumbRow(candidate, compressible: candidate.shown.count == 1)
            }
        }
        .padding(.horizontal, DS.Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("files.a11y.path", "경로: \(browser.path)"))
    }

    private func crumbRow(_ candidate: CrumbLayout.Candidate, compressible: Bool) -> some View {
        HStack(spacing: DS.Space.xs) {
            if !candidate.folded.isEmpty {
                Menu {
                    ForEach(candidate.folded.reversed()) { crumb in
                        Button(crumb.title) { Task { await model.open(side, path: crumb.path) } }
                    }
                } label: {
                    Text(verbatim: "…")
                        .font(DS.Font.body)
                        .foregroundStyle(DS.Status.neutralText)
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help(String.ui("files.crumbs.folded", "상위 폴더 \(String(candidate.folded.count))개"))
                .accessibilityLabel(String.ui("files.crumbs.folded", "상위 폴더 \(String(candidate.folded.count))개"))
                chevron
            }
            ForEach(Array(candidate.shown.enumerated()), id: \.element.id) { index, crumb in
                if index > 0 { chevron }
                CrumbButton(crumb: crumb, isLast: index == candidate.shown.count - 1, compressible: compressible) {
                    Task { await model.open(side, path: crumb.path) }
                }
            }
        }
        .fixedSize(horizontal: !compressible, vertical: false)
    }

    private var chevron: some View {
        Image(systemName: "chevron.right")
            .font(DS.Font.caption.weight(.semibold))
            .imageScale(.small)
            .foregroundStyle(.tertiary)
            .accessibilityHidden(true)
    }

    private var goToField: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            Text(String.ui("files.goTo.title", "폴더로 이동"))
                .font(DS.Font.headline)
            TextField(String.ui("files.goTo.prompt", "경로"), text: $typed)
                .font(DS.Font.mono)
                .textFieldStyle(.roundedBorder)
                .frame(width: 360)
                .onSubmit(go)
            HStack {
                Spacer()
                Button(String.ui("action.cancel", "취소")) { typing = false }
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("files.goTo.go", "이동"), action: go)
                    .keyboardShortcut(.defaultAction)
                    .disabled(typed.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(DS.Space.l)
    }

    private func go() {
        let path = typed.trimmingCharacters(in: .whitespaces)
        guard !path.isEmpty else { return }
        typing = false
        let expanded = side == .local ? (path as NSString).expandingTildeInPath : path
        Task { await model.open(side, path: expanded) }
    }
}

/// One crumb: the folder name, truncated in the middle past 180 pt or, when `compressible`, to
/// the width left; the full path as tooltip.
struct CrumbButton: View {
    let crumb: PathCrumb
    let isLast: Bool
    var compressible = false
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Text(crumb.title)
                .font(isLast ? DS.Font.body.weight(.semibold) : DS.Font.body)
                .foregroundStyle(isLast ? AnyShapeStyle(.primary) : AnyShapeStyle(DS.Status.neutralText))
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(maxWidth: 180)
                .fixedSize(horizontal: !compressible, vertical: false)
                .padding(.horizontal, DS.Space.s)
                .frame(height: 24)
                .background(hovering ? DS.Color.neutral.opacity(0.14) : .clear,
                            in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(crumb.path)
        .accessibilityLabel(crumb.title)
        .accessibilityHint(String.ui("files.a11y.crumbHint", "이 폴더를 엽니다"))
    }
}
