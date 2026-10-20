import Foundation
import XCTest
@testable import DeskhandUI

final class StringCatalogTests: XCTestCase {
    private static let sources = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .appendingPathComponent("../../Sources/DeskhandUI")
        .standardizedFileURL

    /// Keys and default values of every `.ui("key", "value")` call in DeskhandUI.
    private func uiCalls() throws -> [(key: String, value: String)] {
        let pattern = try NSRegularExpression(pattern: #"\.ui\(\s*"([^"]+)"\s*,\s*"((?:[^"\\]|\\.)*)""#)
        var calls: [(String, String)] = []
        let files = FileManager.default.enumerator(at: Self.sources, includingPropertiesForKeys: nil)
        while let url = files?.nextObject() as? URL {
            guard url.pathExtension == "swift" else { continue }
            let text = try String(contentsOf: url, encoding: .utf8)
            let range = NSRange(text.startIndex..., in: text)
            for match in pattern.matches(in: text, range: range) {
                calls.append((String(text[Range(match.range(at: 1), in: text)!]),
                              String(text[Range(match.range(at: 2), in: text)!])))
            }
        }
        return calls
    }

    private func catalog() throws -> [String: String] {
        let url = Self.sources.appendingPathComponent("Resources/Localizable.xcstrings")
        let json = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any]
        let strings = try XCTUnwrap(json?["strings"] as? [String: [String: Any]])
        return strings.compactMapValues { entry in
            let ko = (entry["localizations"] as? [String: Any])?["ko"] as? [String: Any]
            return (ko?["stringUnit"] as? [String: Any])?["value"] as? String
        }
    }

    func testEveryUIKeyIsInCatalogWithMatchingArguments() throws {
        let calls = try uiCalls()
        XCTAssertGreaterThan(calls.count, 100)
        let catalog = try catalog()
        let specifier = try NSRegularExpression(pattern: #"%(\d+\$)?(@|lld)"#)
        for (key, value) in calls {
            guard let entry = catalog[key] else {
                XCTFail("missing catalog entry \(key)")
                continue
            }
            let interpolations = value.components(separatedBy: "\\(").count - 1
            let specifiers = specifier.numberOfMatches(in: entry, range: NSRange(entry.startIndex..., in: entry))
            XCTAssertEqual(interpolations, specifiers, key)
        }
    }

    func testFormattedStringsResolveFromCatalog() {
        let current = 3
        let total = 5
        XCTAssertEqual(String.ui("addHost.progress", "\(current)/\(total)단계 진행 중"), "3/5단계 진행 중")
        let title = DoctorStatus.fail.title
        XCTAssertEqual(String.ui("doctor.count", "\(title) \(current)"), "실패 3")
        XCTAssertEqual(InstallStep.initialize.title, "에이전트 초기화")
    }
}
