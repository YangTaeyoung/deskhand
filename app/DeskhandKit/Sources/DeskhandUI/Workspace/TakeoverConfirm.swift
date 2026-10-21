import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Sheet asking the person to confirm taking control from an AI client, with the AI's latest
/// action on that computer. Return confirms, Escape cancels.
public struct TakeoverConfirmView: View {
    let computerName: String
    let client: String?
    let lastAIAction: TimelineEntry?
    let confirm: () -> Void
    let cancel: () -> Void

    public init(computerName: String, client: String?, lastAIAction: TimelineEntry?,
                confirm: @escaping () -> Void, cancel: @escaping () -> Void) {
        self.computerName = computerName
        self.client = client
        self.lastAIAction = lastAIAction
        self.confirm = confirm
        self.cancel = cancel
    }

    /// The sheet for `pending`, reading the computer and its timeline from `store`.
    init(store: AppStore, pending: ControlActions.PendingTakeover) {
        self.init(
            computerName: store.host(pending.hostID)?.name ?? pending.hostID,
            client: pending.client,
            lastAIAction: store.timeline(for: pending.hostID).first { $0.actor == "ai" },
            confirm: { store.controlActions.confirmTakeover(pending.hostID) },
            cancel: { store.controlActions.cancelTakeover(pending.hostID) })
    }

    private var title: String {
        client.map { .ui("takeover.titleWithClient", "\($0)에게서 제어권을 가져올까요?") }
            ?? .ui("takeover.title", "AI에게서 제어권을 가져올까요?")
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xl) {
            HStack(alignment: .top, spacing: DS.Space.l) {
                RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                    .fill(DS.Color.human.opacity(0.14))
                    .frame(width: 48, height: 48)
                    .overlay {
                        Image(systemName: ControlActions.Action.takeOver.symbol)
                            .font(.system(.title, weight: .medium))
                            .foregroundStyle(DS.Color.human)
                    }
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    Text(title)
                        .font(DS.Font.title)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityAddTraits(.isHeader)
                    Text(String.ui("takeover.message", "‘\(computerName)’에서 AI의 입력을 멈추고 내가 조작합니다. 아직 전달되지 않은 AI 입력은 버립니다."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    if let lastAIAction {
                        LastActionBox(entry: lastAIAction)
                            .padding(.top, DS.Space.xs)
                    }
                    Text(String.ui("takeover.footnote", "다시 맡기려면 ‘AI에게 넘기기’를 누르세요."))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .padding(.top, DS.Space.xs)
                }
            }
            HStack(spacing: DS.Space.s) {
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(action: confirm) {
                    Text(ControlActions.Action.takeOver.title)
                }
                .keyboardShortcut(.defaultAction)
                .buttonStyle(.borderedProminent)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 480)
    }
}

/// The AI's latest action: summary and how long ago it happened.
private struct LastActionBox: View {
    let entry: TimelineEntry

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            Text(String.ui("takeover.lastAction", "AI의 마지막 동작"))
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                Image(systemName: ControlOwner.ai.symbol)
                    .foregroundStyle(DS.Color.ai)
                    .accessibilityHidden(true)
                Text(entry.summary)
                    .font(entry.isCommandLike ? DS.Font.mono : DS.Font.body)
                    .lineLimit(2)
                    .truncationMode(.middle)
                Spacer(minLength: DS.Space.s)
                Text(DS.Format.relative(entry.ts.date))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize()
            }
        }
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.surfaceRaised, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.separator, lineWidth: 1)
        )
        .accessibilityElement(children: .combine)
    }
}
