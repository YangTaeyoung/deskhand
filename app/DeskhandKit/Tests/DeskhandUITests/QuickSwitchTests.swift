import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class QuickSwitchTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    private func item(_ id: String, _ kind: QuickSwitchItem.Kind, _ title: String, subtitle: String? = nil,
                      keywords: [String] = []) -> QuickSwitchItem {
        QuickSwitchItem(id: id, kind: kind, title: title, subtitle: subtitle, keywords: keywords, symbol: "circle",
                        action: .command(.openSettings))
    }

    private var items: [QuickSwitchItem] {
        [
            item("tab:screen", .tab, "화면", subtitle: "개발 PC", keywords: ["screen"]),
            item("tab:terminal", .tab, "터미널", subtitle: "개발 PC", keywords: ["terminal", "shell"]),
            item("command:newTerminal", .command, "새 터미널", keywords: ["new terminal"]),
            item("command:openSettings", .command, "설정 열기", keywords: ["settings"]),
            item("host:dev", .host, "개발 PC", subtitle: "연결됨"),
            item("host:meeting", .host, "회의실 PC", subtitle: "오프라인"),
            item("transfer:t1", .transfer, "shop-2026-10-19.log", subtitle: "개발 PC · 받는 중"),
        ]
    }

    private func ids(_ sections: [QuickSwitchSection]) -> [[String]] {
        sections.map { $0.rows.map(\.item.id) }
    }

    // MARK: Ranking

    func testEmptyQueryListsRecentsThenTabsCommandsHostsAndWork() {
        let sections = QuickSwitchRanking.sections(query: "", items: items, recents: ["host:meeting", "command:newTerminal", "host:gone"])
        XCTAssertEqual(sections.map(\.kind), [.recent, .tabs, .commands, .hosts, .work])
        XCTAssertEqual(ids(sections), [
            ["host:meeting", "command:newTerminal"],
            ["tab:screen", "tab:terminal"],
            ["command:openSettings"],
            ["host:dev"],
            ["transfer:t1"],
        ])
    }

    func testInitialConsonantQueryPutsTheTabFirst() throws {
        let sections = QuickSwitchRanking.sections(query: "ㅌㅁㄴ", items: items, recents: [])
        XCTAssertEqual(sections.map(\.kind), [.results])
        let rows = try XCTUnwrap(sections.first?.rows)
        XCTAssertEqual(rows.map(\.item.id), ["tab:terminal", "command:newTerminal"])
        XCTAssertEqual(rows[0].highlights, [0, 1, 2])
        XCTAssertEqual(rows[1].highlights, [2, 3, 4])
    }

    func testEnglishKeywordsFindKoreanTitles() throws {
        let rows = try XCTUnwrap(QuickSwitchRanking.sections(query: "term", items: items, recents: []).first?.rows)
        XCTAssertEqual(rows.first?.item.id, "tab:terminal")
        XCTAssertEqual(rows.first?.highlights, [])
        XCTAssertTrue(rows.map(\.item.id).contains("command:newTerminal"))
    }

    func testRecentItemRanksFirstAmongEqualMatches() throws {
        let plain = try XCTUnwrap(QuickSwitchRanking.sections(query: "pc", items: items, recents: []).first?.rows)
        XCTAssertEqual(plain.prefix(2).map(\.item.id), ["host:dev", "host:meeting"])

        let boosted = try XCTUnwrap(QuickSwitchRanking.sections(query: "pc", items: items, recents: ["host:meeting"]).first?.rows)
        XCTAssertEqual(boosted.prefix(2).map(\.item.id), ["host:meeting", "host:dev"])
    }

    func testWordsMayMatchTitleAndSubtitleSeparately() throws {
        let rows = try XCTUnwrap(QuickSwitchRanking.sections(query: "개발 터미널", items: items, recents: []).first?.rows)
        XCTAssertEqual(rows.first?.item.id, "tab:terminal")
    }

    func testScatteredMatchesAreLeftOut() throws {
        let items = [
            item("tab:screen", .tab, "화면", keywords: ["remote desktop"]),
            item("command:emergencyStop", .command, "긴급 정지", keywords: ["emergency stop"]),
        ]
        let rows = try XCTUnwrap(QuickSwitchRanking.sections(query: "stop", items: items, recents: []).first?.rows)
        XCTAssertEqual(rows.map(\.item.id), ["command:emergencyStop"])
    }

    func testUnmatchedQueryHasNoSections() {
        XCTAssertEqual(QuickSwitchRanking.sections(query: "zzz", items: items, recents: []), [])
    }

    // MARK: Keyboard

    func testArrowsMoveWithinTheRows() {
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .down, selection: 0, count: 3, queryIsEmpty: true), .select(1))
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .down, selection: 2, count: 3, queryIsEmpty: true), .none)
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .up, selection: 2, count: 3, queryIsEmpty: true), .select(1))
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .up, selection: 0, count: 3, queryIsEmpty: true), .none)
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .down, selection: 0, count: 0, queryIsEmpty: false), .none)
    }

    func testReturnAndCommandDigitsOpenRows() {
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .enter, selection: 1, count: 3, queryIsEmpty: false), .activate(1))
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .enter, selection: 0, count: 0, queryIsEmpty: false), .none)
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .commandDigit(3), selection: 0, count: 5, queryIsEmpty: true), .activate(2))
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .commandDigit(9), selection: 0, count: 5, queryIsEmpty: true), .none)
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .commandDigit(0), selection: 0, count: 5, queryIsEmpty: true), .none)
    }

    func testEscapeClearsTheQueryBeforeClosing() {
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .escape, selection: 0, count: 3, queryIsEmpty: false), .clearQuery)
        XCTAssertEqual(QuickSwitchNavigator.effect(for: .escape, selection: 0, count: 3, queryIsEmpty: true), .dismiss)
    }

    func testModelResetsSelectionWhenTheQueryChanges() {
        let model = QuickSwitchModel(defaults: defaults)
        model.present()
        model.selection = 3
        model.query = "ㅌ"
        XCTAssertEqual(model.selection, 0)
    }

    func testModelAppliesKeysToItsRows() {
        let model = QuickSwitchModel(defaults: defaults)
        model.present()
        let rows = QuickSwitchRanking.sections(query: "", items: items, recents: []).flatMap(\.rows)
        XCTAssertNil(model.handle(.down, rows: rows))
        XCTAssertEqual(model.selection, 1)
        XCTAssertEqual(model.handle(.enter, rows: rows)?.id, rows[1].item.id)
        model.query = "x"
        XCTAssertNil(model.handle(.escape, rows: rows))
        XCTAssertEqual(model.query, "")
        XCTAssertTrue(model.isPresented)
        XCTAssertNil(model.handle(.escape, rows: rows))
        XCTAssertFalse(model.isPresented)
    }

    func testRecentsKeepNewestFirstWithoutDuplicates() {
        let model = QuickSwitchModel(defaults: defaults)
        for id in ["a", "b", "a", "c", "d", "e", "f", "g", "h", "i"] { model.record(id) }
        XCTAssertEqual(model.recents, ["i", "h", "g", "f", "e", "d", "c", "a"])
        XCTAssertEqual(QuickSwitchModel(defaults: defaults).recents, model.recents)
    }

    func testPresentingStartsEmpty() {
        let model = QuickSwitchModel(defaults: defaults)
        model.present()
        model.query = "파일"
        model.selection = 2
        model.dismiss()
        model.present()
        XCTAssertEqual(model.query, "")
        XCTAssertEqual(model.selection, 0)
    }

    // MARK: Catalog and actions

    private func store(control: ControlState? = nil) -> AppStore {
        let hosts = PreviewFixtures.hosts()
        var controls = PreviewFixtures.controls(for: hosts)
        if let control { controls[PreviewFixtures.devPCID] = control }
        return AppStore.seeded(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults,
                               hosts: hosts, selectedHostID: PreviewFixtures.devPCID, controls: controls)
    }

    func testCatalogListsTheSelectedComputersTabsAndEveryComputer() {
        let store = store()
        let items = QuickSwitchCatalog.items(store: store)
        let ids = Set(items.map(\.id))
        for tab in WorkspaceTab.allCases { XCTAssertTrue(ids.contains("tab:\(tab.rawValue)"), tab.rawValue) }
        for host in store.hosts { XCTAssertTrue(ids.contains("host:\(host.hostID)"), host.hostID) }
        XCTAssertNil(items.first { $0.id == "tab:files" }?.shortcut)
        XCTAssertEqual(items.first { $0.id == "command:emergencyStop" }?.shortcut, "⇧⌘.")
        XCTAssertEqual(items.first { $0.id == "tab:files" }?.keywords.last, "개발 PC")
        XCTAssertTrue(ids.contains("command:emergencyStop"))
        XCTAssertTrue(ids.contains("command:openSettings"))
    }

    func testCatalogWithoutSelectionOffersOnlyComputersAndAppCommands() {
        let store = AppStore.seeded(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults,
                                    hosts: [], selectedHostID: nil)
        let ids = QuickSwitchCatalog.items(store: store).map(\.id)
        XCTAssertEqual(Set(ids), ["command:addHost", "command:openSettings"])
    }

    func testTakeControlIsOfferedOnlyWhenItApplies() {
        let observing = QuickSwitchCatalog.items(store: store(control: ControlState(owner: "none", gen: 3, aiPaused: false)))
        XCTAssertTrue(observing.contains { $0.id == "command:takeControl" })
        let human = QuickSwitchCatalog.items(store: store(control: ControlState(owner: "human", gen: 3, aiPaused: false)))
        XCTAssertFalse(human.contains { $0.id == "command:takeControl" })
    }

    func testActivatingItemsNavigatesAndRemembersThem() throws {
        let store = store()
        let model = store.quickSwitch
        model.present()
        let items = QuickSwitchCatalog.items(store: store)

        store.activate(try XCTUnwrap(items.first { $0.id == "tab:files" }), openSettings: {})
        XCTAssertEqual(store.layout.tab, .files)
        XCTAssertFalse(model.isPresented)

        store.activate(try XCTUnwrap(items.first { $0.id == "host:\(PreviewFixtures.qaLaptopID)" }), openSettings: {})
        XCTAssertEqual(store.selectedHostID, PreviewFixtures.qaLaptopID)

        store.filesPreferences.queueExpanded = false
        store.layout.tab = .screen
        store.activate(try XCTUnwrap(items.first { $0.id == "command:showTransferQueue" }), openSettings: {})
        XCTAssertEqual(store.layout.tab, .files)
        XCTAssertTrue(store.filesPreferences.queueExpanded)

        var opened = false
        store.activate(try XCTUnwrap(items.first { $0.id == "command:openSettings" }), openSettings: { opened = true })
        XCTAssertTrue(opened)

        XCTAssertEqual(model.recents, ["command:openSettings", "command:showTransferQueue", "host:\(PreviewFixtures.qaLaptopID)", "tab:files"])
    }
}
