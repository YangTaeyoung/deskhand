import Foundation

/// JSON the daemon's own encoders wrote (`Tests/Fixtures/daemon`): method results, error objects
/// and event params of the phase-2 daemon, with temporary paths replaced by `/Users/me` (this Mac)
/// and `/home/pc` (the PC).
public enum DaemonFixture {
    public static func url(_ name: String) -> URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .appendingPathComponent("../Fixtures/daemon/\(name).json")
            .standardizedFileURL
    }

    public static func data(_ name: String) throws -> Data {
        try Data(contentsOf: url(name))
    }

    /// The fixture as one line of JSON, for `MockDaemon` replies and events.
    public static func line(_ name: String) throws -> String {
        let object = try JSONSerialization.jsonObject(with: data(name), options: [.fragmentsAllowed])
        let compact = try JSONSerialization.data(withJSONObject: object, options: [.withoutEscapingSlashes, .fragmentsAllowed])
        return String(decoding: compact, as: UTF8.self)
    }

    /// The elements of a fixture that holds a JSON array, each as one line of JSON.
    public static func lines(_ name: String) throws -> [String] {
        guard let array = try JSONSerialization.jsonObject(with: data(name)) as? [Any] else { return [] }
        return try array.map { element in
            let compact = try JSONSerialization.data(withJSONObject: element, options: [.withoutEscapingSlashes])
            return String(decoding: compact, as: UTF8.self)
        }
    }
}
