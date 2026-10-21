import AppKit
@testable import DeskhandUI
import XCTest

@MainActor
final class MenuCommandTests: XCTestCase {
    func testFindAndRefreshApplyToTheTabsThatHaveThem() {
        XCTAssertEqual(WorkspaceTab.allCases.filter(WorkspaceLayout.canFind), [.files, .processes, .logs, .debugger, .qa])
        XCTAssertEqual(WorkspaceTab.allCases.filter(WorkspaceLayout.canRefresh), [.files, .processes, .logs, .debugger])
    }

    func testEachFindRequestIsNewAndRecentForHalfASecond() {
        let layout = WorkspaceLayout(defaults: UserDefaults(suiteName: "menu-\(UUID())")!)
        let before = layout.findRequest
        XCTAssertFalse(before.isRecent())
        layout.requestFind()
        XCTAssertNotEqual(layout.findRequest, before)
        XCTAssertTrue(layout.findRequest.isRecent())
        XCTAssertFalse(layout.findRequest.isRecent(now: .now.addingTimeInterval(1)))
        let refresh = layout.refreshRequests
        layout.requestRefresh()
        XCTAssertEqual(layout.refreshRequests, refresh + 1)
    }

    private func findItem(_ selector: Selector, tag: Int) -> NSMenuItem {
        let item = NSMenuItem(title: "찾기…", action: selector, keyEquivalent: "f")
        item.tag = tag
        return item
    }

    func testTheStandardFindItemFocusesTheVisibleTabsSearch() {
        let store = AppStore.seeded(backend: PreviewBackend(stepDelay: .zero), defaults: UserDefaults(suiteName: "menu-\(UUID())")!,
                                    hosts: PreviewFixtures.hosts(), selectedHostID: PreviewFixtures.devPCID)
        let responder = FindCommandResponder(store: store)
        store.layout.tab = .processes
        for selector in [#selector(NSResponder.performTextFinderAction(_:)), #selector(NSTextView.performFindPanelAction(_:))] {
            let show = findItem(selector, tag: Int(NSTextFinder.Action.showFindInterface.rawValue))
            XCTAssertTrue(responder.validateMenuItem(show))
            let before = store.layout.findRequest
            _ = responder.perform(selector, with: show)
            XCTAssertNotEqual(store.layout.findRequest, before)
            XCTAssertFalse(responder.validateMenuItem(findItem(selector, tag: Int(NSTextFinder.Action.nextMatch.rawValue))),
                           "find next has no search to follow")
        }
        store.layout.tab = .screen
        XCTAssertFalse(responder.validateMenuItem(findItem(#selector(NSResponder.performTextFinderAction(_:)), tag: 1)))
    }

    func testTheResponderSitsRightAfterTheWindowAndKeepsTheRestOfTheChain() {
        let store = AppStore.seeded(backend: PreviewBackend(stepDelay: .zero), defaults: UserDefaults(suiteName: "menu-\(UUID())")!,
                                    hosts: PreviewFixtures.hosts(), selectedHostID: PreviewFixtures.devPCID)
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 200, height: 100), styleMask: [.titled], backing: .buffered, defer: true)
        let after = NSResponder()
        window.nextResponder = after
        let responder = FindCommandResponder(store: store)
        responder.install(in: window)
        responder.install(in: window)
        XCTAssertTrue(window.nextResponder === responder)
        XCTAssertTrue(responder.nextResponder === after)
    }
}
