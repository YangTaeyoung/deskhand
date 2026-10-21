import SwiftUI

/// Inline error: what failed (`message`), how to fix it (`fix`), the error code on a small
/// secondary line when known, a prominent retry button under the text when `retry` is given,
/// and a close button at the trailing edge when `dismiss` is given.
public struct ErrorBanner: View {
    public let message: String
    public let fix: String?
    public let code: String?
    public let retry: (() -> Void)?
    public let dismiss: (() -> Void)?

    nonisolated static let symbols = ["exclamationmark.octagon.fill", "arrow.clockwise"]
    private static let symbolWidth = DS.Space.l

    public init(message: String, fix: String? = nil, code: String? = nil, retry: (() -> Void)? = nil,
                dismiss: (() -> Void)? = nil) {
        self.message = message
        self.fix = fix
        self.code = code
        self.retry = retry
        self.dismiss = dismiss
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
                Image(systemName: Self.symbols[0])
                    .foregroundStyle(DS.Color.danger)
                    .font(DS.Font.headline)
                    .frame(width: Self.symbolWidth)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(message)
                        .font(DS.Font.headline)
                        .foregroundStyle(.primary)
                    if let fix {
                        Text(fix)
                            .font(DS.Font.body)
                            .foregroundStyle(DS.Status.secondaryOnTint)
                    }
                    if let code {
                        ErrorCodeLine(code: code)
                            .padding(.top, DS.Space.xs)
                    }
                }
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
                .accessibilityElement(children: .combine)
                if let dismiss {
                    Button(action: dismiss) {
                        Image(systemName: "xmark")
                            .font(DS.Font.caption.weight(.bold))
                            .frame(width: DS.Space.l, height: DS.Space.l)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.borderless)
                    .foregroundStyle(.secondary)
                    .help(String.ui("action.dismiss", "닫기"))
                    .accessibilityLabel(String.ui("action.dismiss", "닫기"))
                }
            }
            if let retry {
                Button(action: retry) {
                    Label(String.ui("action.retry", "다시 시도"), systemImage: Self.symbols[1])
                }
                .buttonStyle(.borderedProminent)
                .tint(DS.Status.selectionFill)
                .controlSize(.regular)
                .padding(.leading, Self.symbolWidth + DS.Space.m)
            }
        }
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.danger.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.danger.opacity(0.28), lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
    }
}

/// Small secondary line `오류 코드 CODE` (or another `label`, such as `사유 코드`) with a selectable
/// monospaced code, in the neutral gray that stays legible on tinted boxes. Every error and stop
/// notice ends with it.
public struct ErrorCodeLine: View {
    public let label: String
    public let code: String

    public init(label: String? = nil, code: String) {
        self.label = label ?? String.ui("error.codeLabel", "오류 코드")
        self.code = code
    }

    public var body: some View {
        HStack(spacing: DS.Space.xs) {
            Text(label)
            Text(code)
                .monospaced()
                .textSelection(.enabled)
        }
        .font(DS.Font.caption)
        .foregroundStyle(DS.Status.neutralText)
        .accessibilityElement(children: .combine)
    }
}
