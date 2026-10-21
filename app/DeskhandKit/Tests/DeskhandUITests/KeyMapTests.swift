import XCTest
@testable import DeskhandUI

final class KeyMapTests: XCTestCase {
    func testCommonKeys() {
        XCTAssertTrue(KeyMap.scancode(for: 0x00)! == (0x1E, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x24)! == (0x1C, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x7B)! == (0x4B, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x7C)! == (0x4D, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x7E)! == (0x48, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x7D)! == (0x50, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x35)! == (0x01, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x33)! == (0x0E, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x30)! == (0x0F, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x31)! == (0x39, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x75)! == (0x53, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x4C)! == (0x1C, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x4B)! == (0x35, true))
        XCTAssertTrue(KeyMap.scancode(for: 0x7A)! == (0x3B, false))
        XCTAssertTrue(KeyMap.scancode(for: 0x6F)! == (0x58, false))
        XCTAssertNil(KeyMap.scancode(for: 0x3F))
    }

    func testEveryLetterAndDigitHasItsOwnScancode() {
        let letters: [UInt16] = [0x00, 0x0B, 0x08, 0x02, 0x0E, 0x03, 0x05, 0x04, 0x22, 0x26, 0x28, 0x25, 0x2E,
                                 0x2D, 0x1F, 0x23, 0x0C, 0x0F, 0x01, 0x11, 0x20, 0x09, 0x0D, 0x07, 0x10, 0x06]
        let expected: [UInt16] = [0x1E, 0x30, 0x2E, 0x20, 0x12, 0x21, 0x22, 0x23, 0x17, 0x24, 0x25, 0x26, 0x32,
                                  0x31, 0x18, 0x19, 0x10, 0x13, 0x1F, 0x14, 0x16, 0x2F, 0x11, 0x2D, 0x15, 0x2C]
        XCTAssertEqual(letters.map { KeyMap.scancode(for: $0)?.sc }, expected)
        let digits: [UInt16] = [0x1D, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19]
        XCTAssertEqual(digits.map { KeyMap.scancode(for: $0)?.sc }, [0x0B, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A])
    }

    func testCommandSendsControlByDefault() {
        let map = KeyMap()
        XCTAssertEqual(map.key(for: 0x37), .scancode(0x1D, extended: false))
        XCTAssertEqual(map.key(for: 0x36), .scancode(0x1D, extended: true))
        XCTAssertEqual(map.key(for: 0x3B), .scancode(0x5B, extended: true))
        XCTAssertEqual(map.key(for: 0x3E), .scancode(0x5C, extended: true))

        let unswapped = KeyMap(swapCommandControl: false)
        XCTAssertEqual(unswapped.key(for: 0x37), .scancode(0x5B, extended: true))
        XCTAssertEqual(unswapped.key(for: 0x3B), .scancode(0x1D, extended: false))
        XCTAssertEqual(unswapped.key(for: 0x3E), .scancode(0x1D, extended: true))
    }

    func testKoreanKeysFollowSettings() {
        XCTAssertEqual(KeyMap().key(for: 0x3D), .virtualKey(KeyMap.vkHangul))
        XCTAssertEqual(KeyMap().key(for: 0x3A), .scancode(0x38, extended: false))
        XCTAssertEqual(KeyMap(rightOptionHangul: false).key(for: 0x3D), .scancode(0x38, extended: true))
        XCTAssertEqual(KeyMap(rightCommandHanja: true).key(for: 0x36), .virtualKey(KeyMap.vkHanja))
        XCTAssertEqual(KeyMap().key(for: 0x68), .virtualKey(KeyMap.vkHangul))
        XCTAssertEqual(KeyMap().key(for: 0x66), .virtualKey(KeyMap.vkHanja))
    }
}
