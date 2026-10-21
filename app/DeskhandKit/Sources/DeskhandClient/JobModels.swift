import Foundation

/// Whether the requested action ran.
public struct ActionResult: Codable, Sendable, Equatable {
    public var executed: Bool?
    public var outcome: String?

    public init(executed: Bool? = nil, outcome: String? = nil) {
        self.executed = executed
        self.outcome = outcome
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        executed = try c.decodeIfPresent(Bool.self, forKey: AnyKey("executed"))
        outcome = try c.decodeIfPresent(String.self, forKey: AnyKey("outcome"))
    }
}

/// A file produced by an operation.
public struct Artifact: Codable, Sendable, Equatable {
    public var artifactID: String
    public var jobID: String?
    public var kind: String
    public var name: String
    public var path: String
    public var size: Int
    public var mime: String
    public var sha256: String
    public var createdAt: Timestamp

    public init(artifactID: String, jobID: String? = nil, kind: String, name: String, path: String, size: Int, mime: String, sha256: String, createdAt: Timestamp) {
        self.artifactID = artifactID
        self.jobID = jobID
        self.kind = kind
        self.name = name
        self.path = path
        self.size = size
        self.mime = mime
        self.sha256 = sha256
        self.createdAt = createdAt
    }

    private enum CodingKeys: String, CodingKey {
        case artifactID = "artifactId"
        case jobID = "jobId"
        case kind
        case name
        case path
        case size
        case mime
        case sha256
        case createdAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        artifactID = try c.decode(String.self, forKey: .artifactID)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        kind = try c.decode(String.self, forKey: .kind)
        name = try c.decode(String.self, forKey: .name)
        path = try c.decode(String.self, forKey: .path)
        size = try c.decode(Int.self, forKey: .size)
        mime = try c.decode(String.self, forKey: .mime)
        sha256 = try c.decode(String.self, forKey: .sha256)
        createdAt = try c.decode(Timestamp.self, forKey: .createdAt)
    }
}

/// A background job (`jobs.*`).
public struct Job: Codable, Sendable, Equatable {
    public var jobID: String
    public var hostID: String
    public var kind: String
    public var status: String
    public var requestID: String?
    public var summary: String
    public var progress: Double?
    public var createdAt: Timestamp
    public var updatedAt: Timestamp
    public var finishedAt: Timestamp?
    public var result: JSONValue?

    public init(jobID: String, hostID: String, kind: String, status: String, requestID: String? = nil, summary: String, progress: Double? = nil, createdAt: Timestamp, updatedAt: Timestamp, finishedAt: Timestamp? = nil, result: JSONValue? = nil) {
        self.jobID = jobID
        self.hostID = hostID
        self.kind = kind
        self.status = status
        self.requestID = requestID
        self.summary = summary
        self.progress = progress
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.finishedAt = finishedAt
        self.result = result
    }

    private enum CodingKeys: String, CodingKey {
        case jobID = "jobId"
        case hostID = "hostId"
        case kind
        case status
        case requestID = "requestId"
        case summary
        case progress
        case createdAt
        case updatedAt
        case finishedAt
        case result
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = try c.decode(String.self, forKey: .jobID)
        hostID = try c.decode(String.self, forKey: .hostID)
        kind = try c.decode(String.self, forKey: .kind)
        status = try c.decode(String.self, forKey: .status)
        requestID = try c.decodeIfPresent(String.self, forKey: .requestID)
        summary = try c.decode(String.self, forKey: .summary)
        progress = try c.decodeIfPresent(Double.self, forKey: .progress)
        createdAt = try c.decode(Timestamp.self, forKey: .createdAt)
        updatedAt = try c.decode(Timestamp.self, forKey: .updatedAt)
        finishedAt = try c.decodeIfPresent(Timestamp.self, forKey: .finishedAt)
        result = try c.decodeIfPresent(JSONValue.self, forKey: .result)
    }
}

/// A human, AI or system action record (`timeline.list`).
public struct TimelineEntry: Codable, Sendable, Equatable {
    public var seq: Int
    public var ts: Timestamp
    public var hostID: String
    public var actor: String
    public var clientID: String?
    public var clientName: String?
    public var kind: String
    public var summary: String
    public var outcome: String
    public var jobID: String?
    public var artifacts: [Artifact]

    public init(seq: Int, ts: Timestamp, hostID: String, actor: String, clientID: String? = nil, clientName: String? = nil, kind: String, summary: String, outcome: String, jobID: String? = nil, artifacts: [Artifact] = []) {
        self.seq = seq
        self.ts = ts
        self.hostID = hostID
        self.actor = actor
        self.clientID = clientID
        self.clientName = clientName
        self.kind = kind
        self.summary = summary
        self.outcome = outcome
        self.jobID = jobID
        self.artifacts = artifacts
    }

    private enum CodingKeys: String, CodingKey {
        case seq
        case ts
        case hostID = "hostId"
        case actor
        case clientID = "clientId"
        case clientName
        case kind
        case summary
        case outcome
        case jobID = "jobId"
        case artifacts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(Int.self, forKey: .seq)
        ts = try c.decode(Timestamp.self, forKey: .ts)
        hostID = try c.decode(String.self, forKey: .hostID)
        actor = try c.decode(String.self, forKey: .actor)
        clientID = try c.decodeIfPresent(String.self, forKey: .clientID)
        clientName = try c.decodeIfPresent(String.self, forKey: .clientName)
        kind = try c.decode(String.self, forKey: .kind)
        summary = try c.decode(String.self, forKey: .summary)
        outcome = try c.decode(String.self, forKey: .outcome)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        artifacts = try c.decodeIfPresent([Artifact].self, forKey: .artifacts) ?? []
    }
}

public enum EnvelopeStatus: String, Codable, Sendable, Equatable {
    case succeeded
    case failed
    case running
    case waitingForUser = "waiting_for_user"
    case unknown
    case cancelled

    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = EnvelopeStatus(rawValue: raw) ?? .unknown
    }
}

/// The `error` member of a result envelope.
public struct EnvelopeError: Codable, Sendable, Equatable {
    public var code: String?
    public var message: String?
    public var retryable: Bool?
    public var nextAction: String?

    public init(code: String? = nil, message: String? = nil, retryable: Bool? = nil, nextAction: String? = nil) {
        self.code = code
        self.message = message
        self.retryable = retryable
        self.nextAction = nextAction
    }

    public init(from decoder: Decoder) throws {
        if let s = try? decoder.singleValueContainer().decode(String.self) {
            self.init(message: s)
            return
        }
        let c = try decoder.container(keyedBy: AnyKey.self)
        self.init(
            code: try c.decodeIfPresent(String.self, forKey: AnyKey("code")),
            message: try c.decodeIfPresent(String.self, forKey: AnyKey("message")),
            retryable: try c.decodeIfPresent(Bool.self, forKey: AnyKey("retryable")),
            nextAction: try c.decodeIfPresent(String.self, forKey: AnyKey("nextAction")))
    }
}

/// Common result envelope (local-api.md section 2) plus the method-specific payload decoded from the same object.
public struct Envelope<Payload: Decodable & Sendable>: Decodable, Sendable {
    public let schemaVersion: Int?
    public let requestID: String?
    public let jobID: String?
    public let hostID: String?
    public let sessionID: String?
    public let connectionGeneration: Int?
    public let status: EnvelopeStatus
    public let action: ActionResult?
    public let error: EnvelopeError?
    public let nextAction: String?
    public let artifacts: [Artifact]
    public let payload: Payload

    private enum CodingKeys: String, CodingKey {
        case schemaVersion
        case requestID = "requestId"
        case jobID = "jobId"
        case hostID = "hostId"
        case sessionID = "sessionId"
        case connectionGeneration
        case status
        case action
        case error
        case nextAction
        case artifacts
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        schemaVersion = try c.decodeIfPresent(Int.self, forKey: .schemaVersion)
        requestID = try c.decodeIfPresent(String.self, forKey: .requestID)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        hostID = try c.decodeIfPresent(String.self, forKey: .hostID)
        sessionID = try c.decodeIfPresent(String.self, forKey: .sessionID)
        connectionGeneration = try c.decodeIfPresent(Int.self, forKey: .connectionGeneration)
        status = try c.decodeIfPresent(EnvelopeStatus.self, forKey: .status) ?? .unknown
        action = try c.decodeIfPresent(ActionResult.self, forKey: .action)
        error = try c.decodeIfPresent(EnvelopeError.self, forKey: .error)
        nextAction = try? c.decodeIfPresent(String.self, forKey: .nextAction)
        artifacts = try c.decodeIfPresent([Artifact].self, forKey: .artifacts) ?? []
        payload = try Payload(from: decoder)
    }
}

/// An event notification (`event` method) delivered to `DeskhandClient.events`.
public struct DaemonEvent: Sendable, Equatable {
    public let seq: Int
    public let ts: Timestamp?
    public let hostID: String?
    public let kind: String
    /// Every member of the notification params, with the original key spelling.
    public let payload: [String: JSONValue]

    public init(seq: Int, ts: Timestamp? = nil, hostID: String? = nil, kind: String, payload: [String: JSONValue] = [:]) {
        self.seq = seq
        self.ts = ts
        self.hostID = hostID
        self.kind = kind
        self.payload = payload
    }

    init?(params: JSONValue) {
        guard case .object(let o) = params, let kind = o["kind"]?.stringValue else { return nil }
        self.init(
            seq: o["seq"]?.intValue ?? 0,
            ts: o["ts"]?.stringValue.flatMap(Timestamp.init(string:)),
            hostID: o["host_id"]?.stringValue,
            kind: kind,
            payload: o)
    }
}
