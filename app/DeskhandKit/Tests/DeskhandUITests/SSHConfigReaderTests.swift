import Foundation
import XCTest
@testable import DeskhandUI

final class SSHConfigReaderTests: XCTestCase {
    private var directory: URL!

    override func setUpWithError() throws {
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("ssh-config-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
    }

    private func write(_ text: String, to relativePath: String) throws -> URL {
        let url = directory.appendingPathComponent(relativePath)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try text.write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    func testParsesAliasesSkippingWildcards() throws {
        let config = try write("""
            # personal machines
            Host windows win-alt
                HostName 192.168.0.24
                User taeyoung

            Host *
                ServerAliveInterval 30
            Host *.corp lab?
            host !bastion prod   # comment after the aliases
            Host=build-01
            Host = spaced-equals
            Host "quoted name"
            Match host build-01
                User ci
            HostName not-an-alias
            Host windows
            """, to: "config")

        XCTAssertEqual(SSHConfigReader.hosts(from: config), ["windows", "win-alt", "prod", "build-01", "spaced-equals", "quoted name"])
    }

    func testFollowsInclude() throws {
        _ = try write("Host from-glob-a\n", to: "config.d/a.conf")
        _ = try write("Host from-glob-b\nInclude nested/inner\n", to: "config.d/b.conf")
        _ = try write("Host ignored-by-glob\n", to: "config.d/c.txt")
        _ = try write("Host nested-relative-to-base\n", to: "nested/inner")
        let absolute = try write("Host from-absolute\nInclude config\n", to: "elsewhere/absolute.conf")
        let config = try write("""
            Host first
            Include config.d/*.conf
            Include \(absolute.path) missing/never.conf
            Host last
            """, to: "config")

        XCTAssertEqual(SSHConfigReader.hosts(from: config), [
            "first", "from-glob-a", "from-glob-b", "nested-relative-to-base", "from-absolute", "last",
        ])
    }

    func testMissingFileYieldsNoAliases() {
        XCTAssertEqual(SSHConfigReader.hosts(from: directory.appendingPathComponent("absent")), [])
    }
}
