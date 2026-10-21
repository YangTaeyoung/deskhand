import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Sidebar row for one computer: OS tile, name, connection status line, a pause dot while a
/// debugged app on it is paused, and who holds control.
public struct HostRow: View {
    public let host: Host
    public var debuggerPaused: Bool
    @Environment(\.backgroundProminence) private var prominence

    public init(host: Host, debuggerPaused: Bool = false) {
        self.host = host
        self.debuggerPaused = debuggerPaused
    }

    public var body: some View {
        HStack(spacing: DS.Space.m) {
            HostTile(host: host, size: 32)
            VStack(alignment: .leading, spacing: 0) {
                Text(host.name)
                    .font(DS.Font.body.weight(.medium))
                    .lineLimit(1)
                    .truncationMode(.tail)
                HStack(spacing: DS.Space.xs) {
                    StatusDot(state: host.connection)
                    Text(host.statusLine())
                        .font(DS.Font.caption)
                        .foregroundStyle(prominence == .increased ? AnyShapeStyle(.white) : AnyShapeStyle(DS.Status.neutralText))
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            Spacer(minLength: 0)
            if debuggerPaused {
                PausedDot()
                    .help(String.ui("sidebar.debuggerPaused", "디버거에서 일시정지된 앱이 있습니다"))
            }
            if host.controlOwner != .none {
                ControlBadge(owner: host.controlOwner)
            }
        }
        .padding(.vertical, DS.Space.xs)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
        .help(host.connection == .ready ? host.platformTitle : host.statusLine())
    }

    private var accessibilityText: String {
        var parts = [host.name, host.connection.title, host.statusLine()]
        if host.controlOwner != .none {
            parts.append(ControlPill.label(owner: host.controlOwner, clientName: host.ownerClient))
        }
        if debuggerPaused { parts.append(String.ui("sidebar.debuggerPaused", "디버거에서 일시정지된 앱이 있습니다")) }
        var seen = Set<String>()
        return parts.filter { seen.insert($0).inserted }.joined(separator: ", ")
    }
}

/// Rounded tile with the OS symbol, tinted with the app accent while connected.
public struct HostTile: View {
    public let host: Host
    public let size: CGFloat
    @Environment(\.backgroundProminence) private var prominence

    public init(host: Host, size: CGFloat) {
        self.host = host
        self.size = size
    }

    private var tint: Color {
        host.connection == .ready || host.connection == .degraded ? DS.Color.accent : DS.Color.neutral
    }

    public var body: some View {
        RoundedRectangle(cornerRadius: size >= 40 ? DS.Radius.m : DS.Radius.s, style: .continuous)
            .fill(prominence == .increased ? AnyShapeStyle(.white.opacity(0.2)) : AnyShapeStyle(tint.opacity(0.14)))
            .frame(width: size, height: size)
            .overlay {
                Image(systemName: host.osSymbol)
                    .font(.system(size: size * 0.48, weight: .medium))
                    .foregroundStyle(prominence == .increased ? AnyShapeStyle(.white) : AnyShapeStyle(tint))
            }
            .accessibilityHidden(true)
    }
}

/// Compact control indicator for rows: the owner symbol on a tinted circle.
public struct ControlBadge: View {
    public let owner: ControlOwner
    @Environment(\.backgroundProminence) private var prominence

    public init(owner: ControlOwner) {
        self.owner = owner
    }

    public var body: some View {
        Image(systemName: owner.symbol)
            .font(.system(.caption, weight: .bold))
            .foregroundStyle(.white)
            .frame(width: 20, height: 20)
            .background(owner.fill.gradient, in: Circle())
            .overlay(Circle().strokeBorder(.white.opacity(prominence == .increased ? 0.9 : 0), lineWidth: 1.5))
            .help(owner.title)
            .accessibilityLabel(String.ui("a11y.control", "제어권: \(ControlPill.label(owner: owner, clientName: nil))"))
    }
}
