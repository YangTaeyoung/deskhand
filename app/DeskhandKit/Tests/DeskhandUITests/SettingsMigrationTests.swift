import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class SettingsMigrationTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    func testMovesEveryOldKeyIntoTheNamespace() {
        for (index, rename) in SettingsMigration.renamedKeys.enumerated() {
            defaults.set("value-\(index)", forKey: rename.old)
        }

        SettingsMigration.migrate(defaults)

        for (index, rename) in SettingsMigration.renamedKeys.enumerated() {
            XCTAssertEqual(defaults.string(forKey: rename.new), "value-\(index)", rename.new)
            XCTAssertNil(defaults.object(forKey: rename.old), rename.old)
        }
    }

    func testValueAlreadyInTheNamespaceWins() {
        defaults.set("sharp", forKey: "screenPreset")
        defaults.set("saver", forKey: SettingsKey.Screen.preset)

        SettingsMigration.migrate(defaults)

        XCTAssertEqual(defaults.string(forKey: SettingsKey.Screen.preset), "saver")
        XCTAssertNil(defaults.object(forKey: "screenPreset"))
    }

    func testMovesHostAndTableKeysByPrefix() {
        let launches = Data("[]".utf8)
        defaults.set(launches, forKey: "debugger.recentLaunches.windows-dev")
        defaults.set(212.0, forKey: "DataTable.processes.firstColumnWidth")

        SettingsMigration.migrate(defaults)

        XCTAssertEqual(defaults.data(forKey: SettingsKey.Debugger.recentLaunches("windows-dev")), launches)
        XCTAssertEqual(defaults.double(forKey: SettingsKey.Table.firstColumnWidth("processes")), 212)
        XCTAssertNil(defaults.object(forKey: "debugger.recentLaunches.windows-dev"))
        XCTAssertNil(defaults.object(forKey: "DataTable.processes.firstColumnWidth"))
    }

    func testRunsOncePerDefaultsDomain() {
        SettingsMigration.migrate(defaults)
        XCTAssertEqual(defaults.integer(forKey: SettingsKey.version), SettingsMigration.currentVersion)

        defaults.set("terminal", forKey: "workspaceTab")
        SettingsMigration.migrate(defaults)

        XCTAssertEqual(defaults.string(forKey: "workspaceTab"), "terminal")
        XCTAssertNil(defaults.object(forKey: SettingsKey.Workspace.tab))
    }

    func testLeavesKeysOutsideTheTableAlone() {
        defaults.set("kept", forKey: "somethingElse")

        SettingsMigration.migrate(defaults)

        XCTAssertEqual(defaults.string(forKey: "somethingElse"), "kept")
    }

    func testEveryRenamedKeyLandsInTheNamespace() {
        for rename in SettingsMigration.renamedKeys {
            XCTAssertTrue(rename.new.hasPrefix(SettingsKey.prefix), rename.new)
            XCTAssertFalse(rename.old.hasPrefix(SettingsKey.prefix), rename.old)
        }
        for rename in SettingsMigration.renamedPrefixes {
            XCTAssertTrue(rename.new.hasPrefix(SettingsKey.prefix), rename.new)
        }
        XCTAssertEqual(Set(SettingsMigration.renamedKeys.map(\.new)).count, SettingsMigration.renamedKeys.count)
    }

    func testStoreReadsPreferencesFromOldKeys() {
        defaults.set("saver", forKey: "screenPreset")
        defaults.set(true, forKey: "terminalOptionAsMeta")
        defaults.set(false, forKey: "filesConfirmMoves")
        defaults.set("logs", forKey: "workspaceTab")
        defaults.set(PreviewFixtures.qaLaptopID, forKey: "selectedHostID")

        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)

        XCTAssertEqual(store.screenPreferences.preset, .saver)
        XCTAssertTrue(store.terminalPreferences.optionAsMeta)
        XCTAssertFalse(store.filesPreferences.confirmMoves)
        XCTAssertEqual(store.layout.tab, .logs)
        XCTAssertEqual(store.selectedHostID, PreviewFixtures.qaLaptopID)
        XCTAssertEqual(defaults.string(forKey: SettingsKey.Screen.preset), "saver")
    }

    func testPreferencesWriteIntoTheNamespace() {
        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)

        store.screenPreferences.showsLocalCursor = true
        store.terminalPreferences.allowsClipboardWrite = true
        store.filesPreferences.finishedTransferRetention = .day
        store.layout.tab = .qa

        let written = defaults.persistentDomain(forName: suiteName) ?? [:]
        let outside = written.keys.filter { !$0.hasPrefix(SettingsKey.prefix) }
        XCTAssertEqual(outside, [])
        XCTAssertEqual(defaults.object(forKey: SettingsKey.Screen.showsLocalCursor) as? Bool, true)
        XCTAssertEqual(defaults.object(forKey: SettingsKey.Terminal.allowsClipboardWrite) as? Bool, true)
    }
}
