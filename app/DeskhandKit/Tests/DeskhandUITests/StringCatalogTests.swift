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

    /// The source literal with escapes resolved and each `\\(…)` replaced by `◆`.
    private func normalizedSource(_ literal: String) -> String {
        var out = ""
        var chars = Array(literal)[...]
        while let c = chars.popFirst() {
            guard c == "\\", let next = chars.popFirst() else {
                out.append(c)
                continue
            }
            switch next {
            case "n": out.append("\n")
            case "(":
                var depth = 1
                while depth > 0, let inner = chars.popFirst() {
                    if inner == "(" { depth += 1 } else if inner == ")" { depth -= 1 }
                }
                out.append("◆")
            default: out.append(next)
            }
        }
        return out
    }

    func testEveryUIKeyIsInCatalogWithSameText() throws {
        let calls = try uiCalls()
        XCTAssertGreaterThan(calls.count, 100)
        let catalog = try catalog()
        let specifier = try NSRegularExpression(pattern: #"%(\d+\$)?(@|lld)"#)
        for (key, value) in calls {
            guard let entry = catalog[key] else {
                XCTFail("missing catalog entry \(key)")
                continue
            }
            let normalizedEntry = specifier.stringByReplacingMatches(
                in: entry, range: NSRange(entry.startIndex..., in: entry), withTemplate: "◆")
            XCTAssertEqual(normalizedSource(value), normalizedEntry, key)
        }
    }

    func testCatalogHasNoParticlePlaceholders() throws {
        let placeholder = try NSRegularExpression(pattern: #"\((이|가|을|를|은|는|과|와|으|로|이나|나|이라|라|아|야)\)"#)
        let offending = try catalog().filter { _, value in
            placeholder.firstMatch(in: value, range: NSRange(value.startIndex..., in: value)) != nil
        }
        XCTAssertTrue(offending.isEmpty, offending.keys.sorted().map { "\($0): \(offending[$0] ?? "")" }.joined(separator: "\n"))
    }

    func testFormattedStringsResolveFromCatalog() {
        let current = 3
        let total = 5
        XCTAssertEqual(String.ui("addHost.progress", "\(current)/\(total)단계 진행 중"), "3/5단계 진행 중")
        let title = DoctorStatus.fail.title
        XCTAssertEqual(String.ui("doctor.count", "\(title) \(current)"), "실패 3")
        XCTAssertEqual(InstallStep.initialize.title, "에이전트 초기화")
        let fps = 60
        let latency = 18
        XCTAssertEqual(String.ui("quality.short", "\(fps) fps · \(latency) ms"), "60 fps · 18 ms")
        let tab = WorkspaceTab.files
        XCTAssertEqual(String.ui("tab.help", "\(tab.title) (⌘\(tab.number))"), "파일 (⌘3)")
    }
}
