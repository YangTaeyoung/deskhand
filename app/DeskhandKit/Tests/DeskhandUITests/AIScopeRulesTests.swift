import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class AIScopeRulesTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    // MARK: Work roots

    func testWindowsRootIsNormalized() {
        XCTAssertEqual(WorkRoots.normalize("  c:/Work/Shop/  ", style: .windows), "C:\\Work\\Shop")
        XCTAssertEqual(WorkRoots.normalize("\"D:\\Builds\"", style: .windows), "D:\\Builds")
        XCTAssertEqual(WorkRoots.normalize("C:\\", style: .windows), "C:\\")
        XCTAssertEqual(WorkRoots.normalize("c:", style: .windows), "C:\\")
        XCTAssertEqual(WorkRoots.normalize("\\\\nas\\share\\qa\\", style: .windows), "\\\\nas\\share\\qa")
        XCTAssertEqual(WorkRoots.normalize("/srv/app/", style: .posix), "/srv/app")
        XCTAssertEqual(WorkRoots.normalize("/", style: .posix), "/")
    }

    func testRelativeOrEmptyRootsAreRefused() {
        XCTAssertEqual(WorkRoots.validate("", existing: [], style: .windows), .failure(.empty))
        XCTAssertEqual(WorkRoots.validate("Work\\Shop", existing: [], style: .windows), .failure(.notAbsolute))
        XCTAssertEqual(WorkRoots.validate("srv/app", existing: [], style: .posix), .failure(.notAbsolute))
    }

    func testDuplicateRootIsRefusedIgnoringCaseOnWindows() {
        XCTAssertEqual(WorkRoots.validate("c:\\work\\shop\\", existing: ["C:\\Work\\Shop"], style: .windows),
                       .failure(.duplicate("C:\\Work\\Shop")))
        XCTAssertEqual(WorkRoots.validate("/Srv/App", existing: ["/srv/app"], style: .posix), .success("/Srv/App"))
    }

    func testRootInsideAnotherIsRefusedAsCovered() {
        XCTAssertEqual(WorkRoots.validate("C:\\Work\\Shop\\logs", existing: ["C:\\Work\\Shop"], style: .windows),
                       .failure(.covered(by: "C:\\Work\\Shop")))
        XCTAssertEqual(WorkRoots.validate("C:\\Work\\Shopping", existing: ["C:\\Work\\Shop"], style: .windows),
                       .success("C:\\Work\\Shopping"))
    }

    func testContainmentStopsAtSeparators() {
        XCTAssertTrue(WorkRoots.contains("C:\\Work\\Shop", "c:\\work\\shop\\bin\\app.exe", style: .windows))
        XCTAssertTrue(WorkRoots.contains("C:\\Work\\Shop", "C:\\Work\\Shop", style: .windows))
        XCTAssertFalse(WorkRoots.contains("C:\\Work\\Shop", "C:\\Work\\Shopping\\a.txt", style: .windows))
        XCTAssertTrue(WorkRoots.contains("C:\\", "C:\\Users\\dev", style: .windows))
        XCTAssertFalse(WorkRoots.contains("C:\\Work", "D:\\Work\\a.txt", style: .windows))
        XCTAssertFalse(WorkRoots.contains("C:\\Work", "C:\\Work\\..\\Windows\\win.ini", style: .windows))
        XCTAssertTrue(WorkRoots.contains("/srv/app", "/srv/app/log/a", style: .posix))
        XCTAssertFalse(WorkRoots.contains("/srv/app", "/srv/App/log", style: .posix))
    }

    func testPreferencesKeepRootsPerHost() {
        let preferences = FilesPreferences(defaults: defaults)
        XCTAssertNil(preferences.addWorkRoot("c:/Work/Shop", for: "a", style: .windows))
        XCTAssertEqual(preferences.addWorkRoot("C:\\Work\\Shop", for: "a", style: .windows), .duplicate("C:\\Work\\Shop"))
        XCTAssertNil(preferences.addWorkRoot("D:\\Builds", for: "a", style: .windows))

        let reread = FilesPreferences(defaults: defaults)
        XCTAssertEqual(reread.workRoots(for: "a"), ["C:\\Work\\Shop", "D:\\Builds"])
        XCTAssertEqual(reread.workRoots(for: "b"), [])

        reread.removeWorkRoot("C:\\Work\\Shop", for: "a")
        XCTAssertEqual(FilesPreferences(defaults: defaults).workRoots(for: "a"), ["D:\\Builds"])
    }

    // MARK: AI scope

    func testDefaultScopeIsUnrestricted() {
        let preferences = PrivacyPreferences(defaults: defaults)
        XCTAssertEqual(preferences.scope(for: "a"), .unrestricted)
        XCTAssertTrue(AIScope.unrestricted.allowsScreen)
        XCTAssertTrue(AIScope.unrestricted.allowsClipboard)
        XCTAssertEqual(AIScope.unrestricted.files, .all)
    }

    func testFileAccessFollowsTheMode() {
        let roots = ["C:\\Work\\Shop"]
        var scope = AIScope.unrestricted
        XCTAssertTrue(scope.allowsFile("D:\\Secrets\\a.txt", roots: roots, style: .windows))

        scope.files = .workRoots
        XCTAssertTrue(scope.allowsFile("C:\\Work\\Shop\\data\\a.json", roots: roots, style: .windows))
        XCTAssertFalse(scope.allowsFile("D:\\Secrets\\a.txt", roots: roots, style: .windows))
        XCTAssertFalse(scope.allowsFile("C:\\Work\\Shop\\a.txt", roots: [], style: .windows))

        scope.files = .none
        XCTAssertFalse(scope.allowsFile("C:\\Work\\Shop\\a.txt", roots: roots, style: .windows))
    }

    func testWorkRootModeWithoutRootsWarns() {
        var scope = AIScope.unrestricted
        XCTAssertEqual(scope.problems(rootCount: 0), [])
        scope.files = .workRoots
        XCTAssertEqual(scope.problems(rootCount: 0), [.noWorkRoots])
        XCTAssertEqual(scope.problems(rootCount: 2), [])
    }

    func testRestrictedScopeIsSummarized() {
        XCTAssertEqual(AIScope.unrestricted.summary(rootCount: 0), "화면 · 모든 폴더 · 클립보드")
        let scope = AIScope(allowsScreen: false, files: .workRoots, allowsClipboard: false)
        XCTAssertEqual(scope.summary(rootCount: 2), "화면 차단 · 작업 루트 2곳 · 클립보드 차단")
        XCTAssertEqual(AIScope(allowsScreen: true, files: .none, allowsClipboard: true).summary(rootCount: 0),
                       "화면 · 파일 차단 · 클립보드")
        XCTAssertFalse(AIScope.unrestricted.isRestricted)
        XCTAssertTrue(scope.isRestricted)
    }

    func testScopesPersistPerHost() {
        let preferences = PrivacyPreferences(defaults: defaults)
        let scope = AIScope(allowsScreen: false, files: .workRoots, allowsClipboard: true)
        preferences.setScope(scope, for: "a")

        let reread = PrivacyPreferences(defaults: defaults)
        XCTAssertEqual(reread.scope(for: "a"), scope)
        XCTAssertEqual(reread.scope(for: "b"), .unrestricted)

        reread.setScope(.unrestricted, for: "a")
        XCTAssertEqual(PrivacyPreferences(defaults: defaults).scope(for: "a"), .unrestricted)
    }

    func testUnreadableStoredScopesFallBackToUnrestricted() {
        defaults.set(Data("not json".utf8), forKey: SettingsKey.Privacy.aiScopes)
        XCTAssertEqual(PrivacyPreferences(defaults: defaults).scope(for: "a"), .unrestricted)
    }
}
