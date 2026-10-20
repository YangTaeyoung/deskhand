import DeskhandClient
import XCTest
@testable import DeskhandUI

final class ProcessListTests: XCTestCase {
    private let base = Date(timeIntervalSince1970: 1_792_000_000)

    private func proc(_ pid: Int, _ name: String, ppid: Int = 1000, cpu: Double? = 0, memKb: Int = 1_000, user: String? = "taeyoung",
                      startedAt minutes: Double? = 10, path: String? = "", deskhand: Bool = false) -> RemoteProcess {
        RemoteProcess(pid: pid, ppid: ppid, name: name, path: path == "" ? "C:\\Program Files\\\(name)" : path, cmd: name, memKb: memKb,
                      cpuPct: cpu, user: user, started: minutes.map { Timestamp(date: base.addingTimeInterval($0 * 60)) },
                      startedByDeskhand: deskhand)
    }

    // MARK: Sorting

    func testStandardSortIsCPUDescendingThenNameThenPID() {
        let list = [
            proc(30, "b.exe", cpu: 1.5), proc(20, "a.exe", cpu: 1.5), proc(10, "a.exe", cpu: 1.5),
            proc(40, "z.exe", cpu: 22), proc(50, "idle.exe", cpu: 0),
        ]
        let rows = ProcessList.rows(list, query: "", sort: .standard, tree: false, collapsed: [])
        XCTAssertEqual(rows.map(\.id), [40, 10, 20, 30, 50])
        XCTAssertEqual(ProcessSort.standard, ProcessSort(column: .cpu, ascending: false))
    }

    func testEachColumnSortsBothWaysWithMissingValuesLast() {
        let list = [
            proc(3, "Code.exe", cpu: 4, memKb: 300, user: "taeyoung", startedAt: 30, path: "C:\\b"),
            proc(1, "chrome.exe", cpu: nil, memKb: 900, user: nil, startedAt: nil, path: "C:\\a"),
            proc(2, "dotnet.exe", cpu: 9, memKb: 100, user: "SYSTEM", startedAt: 5, path: nil),
        ]
        func order(_ column: ProcessColumn, _ ascending: Bool) -> [Int] {
            ProcessList.rows(list, query: "", sort: ProcessSort(column: column, ascending: ascending), tree: false, collapsed: []).map(\.id)
        }
        XCTAssertEqual(order(.name, true), [1, 3, 2])
        XCTAssertEqual(order(.name, false), [2, 3, 1])
        XCTAssertEqual(order(.pid, true), [1, 2, 3])
        XCTAssertEqual(order(.pid, false), [3, 2, 1])
        XCTAssertEqual(order(.cpu, false), [2, 3, 1])
        XCTAssertEqual(order(.cpu, true), [3, 2, 1])
        XCTAssertEqual(order(.memory, false), [1, 3, 2])
        XCTAssertEqual(order(.memory, true), [2, 3, 1])
        XCTAssertEqual(order(.user, true), [2, 3, 1])
        XCTAssertEqual(order(.user, false), [3, 2, 1])
        XCTAssertEqual(order(.started, true), [2, 3, 1])
        XCTAssertEqual(order(.started, false), [3, 2, 1])
        XCTAssertEqual(order(.path, true), [1, 3, 2])
        XCTAssertEqual(order(.path, false), [3, 1, 2])
    }

    func testSystemIdleProcessSortsLastByCPUInBothDirections() {
        let list = [proc(0, "System Idle Process", ppid: 0, cpu: 78), proc(7, "Shop.exe", cpu: 3.2), proc(8, "dwm.exe", cpu: 0.4)]
        XCTAssertEqual(ProcessList.rows(list, query: "", sort: .standard, tree: false, collapsed: []).map(\.id), [7, 8, 0])
        XCTAssertEqual(ProcessList.rows(list, query: "", sort: ProcessSort(column: .cpu, ascending: true), tree: false, collapsed: []).map(\.id),
                       [8, 7, 0])
        XCTAssertTrue(ProcessList.isIdle(list[0]))
    }

    // MARK: Search

    func testSearchMatchesNamePathOrPIDPrefixIgnoringCase() {
        let list = [
            proc(7712, "node.exe", path: "C:\\Program Files\\nodejs\\node.exe"),
            proc(812, "Code.exe", path: "C:\\Users\\taeyoung\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe"),
            proc(9216, "powershell.exe", path: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe"),
        ]
        func found(_ query: String) -> Set<Int> {
            Set(ProcessList.rows(list, query: query, sort: .standard, tree: false, collapsed: []).map(\.id))
        }
        XCTAssertEqual(found("NODE"), [7712])
        XCTAssertEqual(found("vs code"), [812])
        XCTAssertEqual(found("77"), [7712])
        XCTAssertEqual(found("92"), [9216])
        XCTAssertEqual(found("  system32 "), [9216])
        XCTAssertEqual(found(""), [7712, 812, 9216])
        XCTAssertEqual(found("zzz"), [])
    }

    // MARK: Tree

    func testTreeNestsChildrenAndSortsSiblings() {
        let list = [
            proc(5520, "explorer.exe", ppid: 5400, cpu: 0.4, startedAt: 1),
            proc(6000, "chrome.exe", ppid: 5520, cpu: 3, startedAt: 2),
            proc(6010, "chrome.exe", ppid: 6000, cpu: 9, startedAt: 3),
            proc(6020, "chrome.exe", ppid: 6000, cpu: 1, startedAt: 3),
            proc(7000, "Code.exe", ppid: 5520, cpu: 12, startedAt: 4),
            proc(4, "System", ppid: 0, cpu: 0.1, user: "SYSTEM", startedAt: 0),
        ]
        let rows = ProcessList.rows(list, query: "", sort: .standard, tree: true, collapsed: [])
        XCTAssertEqual(rows.map(\.id), [5520, 7000, 6000, 6010, 6020, 4])
        XCTAssertEqual(rows.map(\.depth), [0, 1, 1, 2, 2, 0])
        XCTAssertEqual(rows.map(\.hasChildren), [true, false, true, false, false, false])
        XCTAssertTrue(rows.allSatisfy { !$0.isContext })
    }

    func testTreeIgnoresParentsStartedAfterTheChildSelfParentsAndCycles() {
        let list = [
            proc(100, "reused-parent.exe", ppid: 1, startedAt: 50),
            proc(200, "orphan.exe", ppid: 100, startedAt: 20),
            proc(300, "self.exe", ppid: 300, startedAt: 5),
            proc(400, "loop-a.exe", ppid: 500, startedAt: nil),
            proc(500, "loop-b.exe", ppid: 400, startedAt: nil),
        ]
        let rows = ProcessList.rows(list, query: "", sort: ProcessSort(column: .pid, ascending: true), tree: true, collapsed: [])
        XCTAssertEqual(Set(rows.map(\.id)), [100, 200, 300, 400, 500])
        XCTAssertEqual(rows.first { $0.id == 200 }?.depth, 0)
        XCTAssertEqual(rows.first { $0.id == 300 }?.depth, 0)
        XCTAssertFalse(rows.first { $0.id == 100 }?.hasChildren ?? true)
        XCTAssertEqual(rows.count, 5)
    }

    func testProcessesWhoseParentIsPIDZeroAreRoots() {
        let list = [proc(0, "System Idle Process", ppid: 0, startedAt: nil), proc(4, "System", ppid: 0, startedAt: 0),
                    proc(528, "smss.exe", ppid: 4, startedAt: 1)]
        let rows = ProcessList.rows(list, query: "", sort: ProcessSort(column: .pid, ascending: true), tree: true, collapsed: [])
        XCTAssertEqual(rows.map(\.id), [0, 4, 528])
        XCTAssertEqual(rows.map(\.depth), [0, 0, 1])
        XCTAssertNil(ProcessForest(list).parent(of: 4))
    }

    func testCollapsedParentHidesItsDescendants() {
        let list = [
            proc(10, "parent.exe", ppid: 1, startedAt: 1), proc(11, "child.exe", ppid: 10, startedAt: 2),
            proc(12, "grandchild.exe", ppid: 11, startedAt: 3), proc(20, "other.exe", ppid: 1, startedAt: 1),
        ]
        let rows = ProcessList.rows(list, query: "", sort: ProcessSort(column: .pid, ascending: true), tree: true, collapsed: [10])
        XCTAssertEqual(rows.map(\.id), [10, 20])
        XCTAssertEqual(rows.first?.isExpanded, false)
        XCTAssertEqual(rows.first?.hasChildren, true)
    }

    func testTreeSearchShowsMatchesUnderTheirAncestorsAsContext() {
        let list = [
            proc(10, "explorer.exe", ppid: 1, startedAt: 1), proc(11, "WindowsTerminal.exe", ppid: 10, startedAt: 2),
            proc(12, "pwsh.exe", ppid: 11, startedAt: 3), proc(13, "node.exe", ppid: 12, startedAt: 4),
            proc(20, "svchost.exe", ppid: 1, startedAt: 1),
        ]
        let rows = ProcessList.rows(list, query: "node", sort: .standard, tree: true, collapsed: [10, 11])
        XCTAssertEqual(rows.map(\.id), [10, 11, 12, 13])
        XCTAssertEqual(rows.map(\.isContext), [true, true, true, false])
        XCTAssertEqual(rows.map(\.depth), [0, 1, 2, 3])
    }

    func testDescendantsListEveryLevelOnce() {
        let list = [
            proc(10, "parent.exe", ppid: 1, startedAt: 1), proc(11, "child.exe", ppid: 10, startedAt: 2),
            proc(12, "grandchild.exe", ppid: 11, startedAt: 3), proc(13, "child2.exe", ppid: 10, startedAt: 2),
            proc(20, "other.exe", ppid: 1, startedAt: 1),
        ]
        let forest = ProcessForest(list)
        XCTAssertEqual(Set(forest.descendants(of: 10).map(\.pid)), [11, 12, 13])
        XCTAssertEqual(forest.descendants(of: 12), [])
        XCTAssertEqual(forest.parent(of: 12)?.pid, 11)
        XCTAssertNil(forest.parent(of: 10))
    }

    // MARK: Formatting

    func testMemoryHasOneDecimalAndASpacedUnit() {
        XCTAssertEqual(ProcessFormat.memory(kb: 302 * 1024), "302.0 MB")
        XCTAssertEqual(ProcessFormat.memory(kb: 1_677_722), "1.6 GB")
        XCTAssertEqual(ProcessFormat.memory(kb: 144), "144.0 KB")
        XCTAssertEqual(ProcessFormat.memory(kb: 420_045), "410.2 MB")
    }

    func testStructuredEventMessagesUseMonospace() {
        XCTAssertTrue(EventMessage.isStructured("응용 프로그램: Shop.exe\nCoreCLR 버전: 9.0\n설명: 처리되지 않은 예외\n   위치: A.B()\n   위치: C.D()"))
        XCTAssertTrue(EventMessage.isStructured("오류 버킷 1859, 유형 4\n이벤트 이름: APPCRASH\n\n문제 서명:\nP1: Shop.exe\nP2: 1.18"))
        XCTAssertFalse(EventMessage.isStructured("Windows Update 서비스가 실행 중 상태로 전환되었습니다."))
        XCTAssertFalse(EventMessage.isStructured("시스템이 정상적으로 종료되기 전에 다시 부팅되었습니다. 이 오류는 시스템이 응답을 멈추었을 때 발생할 수 있습니다."))
    }

    // MARK: Protection

    func testCriticalSystemProcessesAndTheAgentCannotBeKilled() {
        for name in ["System", "smss.exe", "CSRSS.EXE", "wininit.exe", "services.exe", "lsass.exe", "winlogon.exe"] {
            XCTAssertEqual(ProcessGuard.block(for: proc(900, name)), .critical, name)
        }
        XCTAssertEqual(ProcessGuard.block(for: proc(0, "System Idle Process", ppid: 0)), .critical)
        XCTAssertEqual(ProcessGuard.block(for: proc(4, "System", ppid: 0)), .critical)
        XCTAssertEqual(ProcessGuard.block(for: proc(3120, "deskhand-agent.exe", path: "C:\\Program Files\\Deskhand\\deskhand-agent.exe")), .agent)
        XCTAssertEqual(ProcessGuard.block(for: proc(3120, "Deskhand-Agent.EXE")), .agent)
        XCTAssertNil(ProcessGuard.block(for: proc(7712, "node.exe")))
        XCTAssertNil(ProcessGuard.block(for: proc(9216, "powershell.exe", deskhand: true)))
        XCTAssertNil(ProcessGuard.block(for: proc(5520, "explorer.exe")))
        XCTAssertFalse(KillBlock.critical.reason.isEmpty)
        XCTAssertFalse(KillBlock.agent.reason.isEmpty)
    }

    func testTheAgentIsRecognizedBySelfBeforeItsName() {
        var renamed = proc(4400, "agent-renamed.exe")
        renamed.isSelf = true
        XCTAssertEqual(ProcessGuard.block(for: renamed), .agent)
        XCTAssertEqual(ProcessGuard.block(for: proc(3120, "deskhand-agent.exe")), .agent)
    }

    func testTreeKillIsBlockedWhenADescendantIsProtected() {
        let parent = proc(700, "svchost.exe", ppid: 1, startedAt: 1)
        let agent = proc(3120, "deskhand-agent.exe", ppid: 700, startedAt: 2)
        let other = proc(3200, "taskhostw.exe", ppid: 700, startedAt: 2)
        XCTAssertEqual(ProcessGuard.treeBlock(for: parent, descendants: [other, agent]), .protectedDescendant(name: "deskhand-agent.exe"))
        XCTAssertNil(ProcessGuard.treeBlock(for: parent, descendants: [other]))
        XCTAssertEqual(ProcessGuard.treeBlock(for: proc(5, "wininit.exe"), descendants: []), .critical)
    }
}
