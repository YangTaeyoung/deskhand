/// Status of one step in a multi-step operation.
public enum StepStatus: String, CaseIterable, Hashable, Sendable {
    case pending
    case active
    case done
    case failed
}

/// One row of a `StepProgressView`.
public struct StepItem: Identifiable, Hashable, Sendable {
    public let id: String
    public var title: String
    /// Secondary line under the title: progress text, or the failure reason when `status` is `.failed`.
    public var detail: String?
    public var status: StepStatus

    public init(id: String, title: String, detail: String? = nil, status: StepStatus) {
        self.id = id
        self.title = title
        self.detail = detail
        self.status = status
    }
}
