import SwiftUI

/// The symbol and title every tab uses for a PC that is offline.
enum HostOffline {
    static let symbol = "moon.zzz"
    static var title: String { .ui("host.offline.title", "PC가 오프라인입니다") }
}

/// A tab body for a PC that cannot serve it. Offline shows `moon.zzz`, `PC가 오프라인입니다` and
/// what the tab does once connected. Connecting, reconnecting and the certificate mismatch are
/// named by the workspace banner with their actions, so the body shows only a quiet note in the
/// tab's symbol.
struct HostStateBody: View {
    enum State: Equatable {
        case connecting
        case reconnecting
        case offline
        case blocked
    }

    let state: State
    /// The tab's symbol for the quiet note.
    let symbol: String
    /// What the tab does once the PC connects.
    let resumes: String
    /// What re-pairing gives back in this tab.
    let repairs: String

    var body: some View {
        Group {
            switch state {
            case .offline:
                EmptyStateView(symbol: HostOffline.symbol, title: HostOffline.title, message: resumes)
            case .connecting, .reconnecting:
                PlaceholderNote(symbol: symbol, message: resumes)
            case .blocked:
                PlaceholderNote(symbol: "lock.trianglebadge.exclamationmark", message: repairs)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// A strip over a tab whose records stay readable while the PC is unreachable: offline shows
/// `moon.zzz`, `PC가 오프라인입니다` and what still works; reconnecting shows what the tab reads
/// again once the connection is back.
struct HostOfflineStrip: View {
    let reconnecting: Bool
    let detail: String

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Image(systemName: reconnecting ? "arrow.triangle.2.circlepath" : HostOffline.symbol)
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            if !reconnecting {
                Text(HostOffline.title)
                    .font(DS.Font.caption.weight(.semibold))
                    .fixedSize()
            }
            Text(detail)
                .font(DS.Font.caption)
                .foregroundStyle(DS.Status.secondaryOnTint)
                .lineLimit(2)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, DS.Space.m)
        .frame(minHeight: WorkspaceMetrics.subToolbarHeight)
        .background(DS.Color.neutral.opacity(DS.Status.bandOpacity))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
    }
}
