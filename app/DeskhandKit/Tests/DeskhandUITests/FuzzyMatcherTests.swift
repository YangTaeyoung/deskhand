import XCTest
@testable import DeskhandUI

final class FuzzyMatcherTests: XCTestCase {
    private func score(_ query: String, _ candidate: String) -> Int? {
        FuzzyMatcher.match(query, in: candidate)?.score
    }

    func testInitialConsonantsFindHangulTitle() throws {
        let match = try XCTUnwrap(FuzzyMatcher.match("ㅌㅁㄴ", in: "터미널"))
        XCTAssertEqual(match.indices, [0, 1, 2])
    }

    func testDoubleConsonantTheInputMethodJoinedStillMatchesInitials() throws {
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("ㄳ", in: "기술 지원")).indices, [0, 1])
        XCTAssertNotNil(FuzzyMatcher.match("ㅂㅄ", in: "보내기 버튼 상태"))
    }

    func testDecomposedHangulMatchesLikeComposedHangul() throws {
        let name = "주문 확인 화면 오류 재현 절차.docx".decomposedStringWithCanonicalMapping
        XCTAssertNotEqual(name.unicodeScalars.count, "주문 확인 화면 오류 재현 절차.docx".unicodeScalars.count)
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("주문", in: name)).indices, [0, 1])
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("ㅈㅁ", in: name)).indices, [0, 1])
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("ㅎㅁ", in: name)).indices, [6, 7])
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("화면".decomposedStringWithCanonicalMapping, in: "주문 확인 화면")).indices, [6, 7])
        XCTAssertEqual(FuzzyMatcher.match("주문", in: name)?.score, FuzzyMatcher.match("주문", in: "주문 확인 화면 오류 재현 절차.docx")?.score)
    }

    func testPreparedCandidateMatchesLikeItsText() {
        let candidate = FuzzyCandidate("새 터미널")
        XCTAssertEqual(FuzzyMatcher.match("ㅌㅁㄴ", in: candidate), FuzzyMatcher.match("ㅌㅁㄴ", in: "새 터미널"))
    }

    func testInitialConsonantsDoNotMatchFinalConsonants() {
        XCTAssertNil(FuzzyMatcher.match("ㄴ", in: "산"))
        XCTAssertNotNil(FuzzyMatcher.match("ㅅ", in: "산"))
    }

    func testSyllableBeingComposedStillMatches() throws {
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("텀", in: "터미널")).indices, [0, 1])
        XCTAssertEqual(try XCTUnwrap(FuzzyMatcher.match("터민", in: "터미널")).indices, [0, 1, 2])
        XCTAssertNotNil(FuzzyMatcher.match("고", in: "과일"))
        XCTAssertNotNil(FuzzyMatcher.match("ㅌ미", in: "터미널"))
    }

    func testFullSyllablesMustMatchTheirVowels() {
        XCTAssertNil(FuzzyMatcher.match("타", in: "터미널"))
        XCTAssertNil(FuzzyMatcher.match("xyz", in: "터미널"))
    }

    func testLatinLettersIgnoreCaseAndMarkWordStarts() throws {
        let match = try XCTUnwrap(FuzzyMatcher.match("nt", in: "New Terminal"))
        XCTAssertEqual(match.indices, [0, 4])
        XCTAssertGreaterThan(try XCTUnwrap(score("nt", "New Terminal")), try XCTUnwrap(score("nt", "Event list")))
        XCTAssertNotNil(FuzzyMatcher.match("QA", in: "qa"))
    }

    func testExactBeatsPrefixBeatsLaterWord() throws {
        let exact = try XCTUnwrap(score("파일", "파일"))
        let prefix = try XCTUnwrap(score("파일", "파일 전송 큐 보기"))
        let later = try XCTUnwrap(score("파일", "원격 파일 보기"))
        let scattered = try XCTUnwrap(score("파일", "파란 일기"))
        XCTAssertGreaterThan(exact, prefix)
        XCTAssertGreaterThan(prefix, later)
        XCTAssertGreaterThan(later, scattered)
    }

    func testShorterTitleWinsAnEqualMatch() throws {
        XCTAssertGreaterThan(try XCTUnwrap(score("ㅌㅁㄴ", "터미널")), try XCTUnwrap(score("ㅌㅁㄴ", "터미널 탭 닫기")))
    }

    func testConsecutiveMatchBeatsScatteredMatch() throws {
        XCTAssertGreaterThan(try XCTUnwrap(score("ㄱㅂ", "개발 PC")), try XCTUnwrap(score("ㄱㅂ", "긴급 정지 보내기")))
    }

    func testBestAlignmentIsChosen() throws {
        let match = try XCTUnwrap(FuzzyMatcher.match("pc", in: "Proc PC"))
        XCTAssertEqual(match.indices, [5, 6])
    }

    func testEmptyQueryMatchesWithoutHighlights() {
        XCTAssertEqual(FuzzyMatcher.match("", in: "터미널"), FuzzyMatcher.Match(score: 0, indices: []))
        XCTAssertEqual(FuzzyMatcher.match("   ", in: "터미널"), FuzzyMatcher.Match(score: 0, indices: []))
    }

    func testSpacesInTheQueryAreIgnoredInsideOneField() throws {
        XCTAssertNotNil(FuzzyMatcher.match("긴급 정지", in: "긴급 정지"))
        XCTAssertNotNil(FuzzyMatcher.match("긴급정지", in: "긴급 정지"))
    }
}
