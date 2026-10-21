@testable import DeskhandUI
import XCTest

final class CrumbLayoutTests: XCTestCase {
    func testCandidatesFoldOneMoreLeadingCrumbEachDownToTheLastAlone() {
        let crumbs = ["C:", "Users", "taeyoung", "Builds"].map { PathCrumb(title: $0, path: "/" + $0) }
        let candidates = CrumbLayout.candidates(crumbs)
        XCTAssertEqual(candidates.map { $0.folded.map(\.title) }, [[], ["C:"], ["C:", "Users"], ["C:", "Users", "taeyoung"]])
        XCTAssertEqual(candidates.map { $0.shown.map(\.title) },
                       [["C:", "Users", "taeyoung", "Builds"], ["Users", "taeyoung", "Builds"], ["taeyoung", "Builds"], ["Builds"]])
        XCTAssertEqual(CrumbLayout.candidates([]).count, 1)
    }
}
