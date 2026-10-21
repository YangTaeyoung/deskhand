import SwiftUI

extension EnvironmentValues {
    /// The workspace's last 찾기 (⌘F) request.
    @Entry var findRequest = FindRequest()
}

extension View {
    /// Focuses `focused` on each 찾기 request, and on appearing just after one.
    func focusesOnFind(_ focused: FocusState<Bool>.Binding) -> some View {
        modifier(FocusOnFind(focused: focused))
    }
}

private struct FocusOnFind: ViewModifier {
    var focused: FocusState<Bool>.Binding
    @Environment(\.findRequest) private var request

    func body(content: Content) -> some View {
        content
            .onChange(of: request) { _, _ in focused.wrappedValue = true }
            .onAppear { if request.isRecent() { focused.wrappedValue = true } }
    }
}
