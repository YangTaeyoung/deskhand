import AppKit
import XCTest
@testable import DeskhandUI

@MainActor
final class InputCaptureTests: XCTestCase {
    private var window: NSWindow!
    private var capture: InputCapture!
    private var events: [InputEvent] = []
    private var takeoverRequests = 0
    private var captureChanges: [Bool] = []
    private var now: TimeInterval = 100

    override func setUp() async throws {
        _ = NSApplication.shared
        window = NSWindow(contentRect: CGRect(x: -20_000, y: -20_000, width: 1000, height: 800),
                          styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        capture = InputCapture(frame: CGRect(x: 0, y: 0, width: 1000, height: 800))
        window.contentView = capture
        capture.mapper = PointMapper(remote: CGSize(width: 3840, height: 2160), view: capture.bounds.size, backingScale: 2)
        capture.owner = .human
        capture.clock = { [unowned self] in now }
        capture.onEvent = { [unowned self] in events.append($0) }
        capture.onTakeoverRequest = { [unowned self] in takeoverRequests += 1 }
        capture.onCaptureChange = { [unowned self] in captureChanges.append($0) }
    }

    override func tearDown() async throws {
        capture.endCapture()
        window.contentView = nil
        window.close()
    }

    // MARK: Event helpers

    /// A mouse event at `point` in the capture view's top-left coordinates.
    private func mouse(_ type: NSEvent.EventType, at point: CGPoint, modifiers: NSEvent.ModifierFlags = []) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: CGPoint(x: point.x, y: 800 - point.y), modifierFlags: modifiers,
                           timestamp: now, windowNumber: window.windowNumber, context: nil, eventNumber: 0,
                           clickCount: 1, pressure: type == .leftMouseUp ? 0 : 1)!
    }

    private func key(_ type: NSEvent.EventType, _ keyCode: UInt16, _ characters: String = "",
                     modifiers: NSEvent.ModifierFlags = []) -> NSEvent {
        NSEvent.keyEvent(with: type, location: .zero, modifierFlags: modifiers, timestamp: now,
                         windowNumber: window.windowNumber, context: nil, characters: characters,
                         charactersIgnoringModifiers: characters, isARepeat: false, keyCode: keyCode)!
    }

    /// Device-dependent bits for the left and right modifier keys (IOLLEvent.h).
    private enum Device {
        static let leftControl = NSEvent.ModifierFlags(rawValue: 0x0000_0001)
        static let leftCommand = NSEvent.ModifierFlags(rawValue: 0x0000_0008)
        static let leftOption = NSEvent.ModifierFlags(rawValue: 0x0000_0020)
    }

    private func captureByClicking() {
        capture.mouseDown(with: mouse(.leftMouseDown, at: CGPoint(x: 500, y: 400)))
        capture.mouseUp(with: mouse(.leftMouseUp, at: CGPoint(x: 500, y: 400)))
        events.removeAll()
    }

    // MARK: Ownership

    func testHumanInputBlockedWhileAIOwns() {
        capture.owner = .ai
        capture.mouseDown(with: mouse(.leftMouseDown, at: CGPoint(x: 500, y: 400)))
        capture.mouseUp(with: mouse(.leftMouseUp, at: CGPoint(x: 500, y: 400)))
        XCTAssertEqual(events, [])
        XCTAssertEqual(takeoverRequests, 1)
        XCTAssertFalse(capture.isCaptured)
        XCTAssertFalse(capture.handleKeyEvent(key(.keyDown, 0x00, "a")))
        XCTAssertEqual(events, [])
    }

    func testAccessibilityPressAsksForTakeoverThenCapturesOnceHumanOwns() {
        capture.owner = .ai
        XCTAssertTrue(capture.accessibilityPerformPress())
        XCTAssertEqual(takeoverRequests, 1)
        XCTAssertFalse(capture.isCaptured)
        capture.owner = .human
        XCTAssertTrue(capture.isCaptured)
        XCTAssertEqual(events, [])
    }

    func testAccessibilityPressCapturesWithoutClicking() {
        XCTAssertTrue(capture.accessibilityPerformPress())
        XCTAssertTrue(capture.isCaptured)
        XCTAssertEqual(events, [])
        XCTAssertEqual(takeoverRequests, 0)
    }

    func testOwnerTurningAIEndsCaptureWithoutSending() {
        captureByClicking()
        XCTAssertTrue(capture.isCaptured)
        capture.owner = .ai
        XCTAssertFalse(capture.isCaptured)
        XCTAssertEqual(events, [])
        XCTAssertEqual(captureChanges, [true, false])
    }

    // MARK: Mouse

    func testClickCapturesAndSendsPositionThenButton() {
        capture.mouseDown(with: mouse(.leftMouseDown, at: CGPoint(x: 500, y: 400)))
        XCTAssertTrue(capture.isCaptured)
        XCTAssertTrue(window.firstResponder === capture)
        capture.mouseUp(with: mouse(.leftMouseUp, at: CGPoint(x: 500, y: 400)))
        XCTAssertEqual(events, [
            .move(x: 1920, y: 1080, display: 0),
            .button(.left, down: true),
            .button(.left, down: false),
        ])
        XCTAssertEqual(takeoverRequests, 0)
    }

    func testClickInLetterboxCapturesWithoutSending() {
        capture.mouseDown(with: mouse(.leftMouseDown, at: CGPoint(x: 500, y: 40)))
        capture.mouseUp(with: mouse(.leftMouseUp, at: CGPoint(x: 500, y: 40)))
        XCTAssertTrue(capture.isCaptured)
        XCTAssertEqual(events, [])
    }

    func testMouseMovesCoalesceTo120Hz() {
        captureByClicking()
        now += 1
        capture.mouseMoved(with: mouse(.mouseMoved, at: CGPoint(x: 100, y: 200)))
        now += 0.002
        capture.mouseMoved(with: mouse(.mouseMoved, at: CGPoint(x: 101, y: 200)))
        now += 0.002
        capture.mouseMoved(with: mouse(.mouseMoved, at: CGPoint(x: 102, y: 200)))
        XCTAssertEqual(events, [.move(x: 384, y: 312, display: 0)])
        now += 0.005
        capture.flushPendingMove()
        XCTAssertEqual(events.last, .move(x: 391, y: 312, display: 0))
        XCTAssertEqual(events.count, 2)
    }

    func testButtonFlushesPendingMoveFirst() {
        captureByClicking()
        now += 1
        capture.mouseMoved(with: mouse(.mouseMoved, at: CGPoint(x: 100, y: 200)))
        now += 0.001
        capture.mouseMoved(with: mouse(.mouseMoved, at: CGPoint(x: 250, y: 300)))
        capture.rightMouseDown(with: mouse(.rightMouseDown, at: CGPoint(x: 250, y: 300)))
        XCTAssertEqual(events, [
            .move(x: 384, y: 312, display: 0),
            .move(x: 960, y: 696, display: 0),
            .button(.right, down: true),
        ])
    }

    func testDragOutsideTheVideoClampsToTheEdge() {
        capture.mouseDown(with: mouse(.leftMouseDown, at: CGPoint(x: 500, y: 400)))
        events.removeAll()
        now += 1
        capture.mouseDragged(with: mouse(.leftMouseDragged, at: CGPoint(x: 1200, y: -30)))
        XCTAssertEqual(events, [.move(x: 3839, y: 0, display: 0)])
    }

    func testWheelScalesToWheelDelta() {
        var wheel = WheelAccumulator()
        XCTAssertEqual(wheel.add(dx: 0, dy: 1, precise: false), WheelAccumulator.Step(dx: 0, dy: 120))
        XCTAssertEqual(wheel.add(dx: 0, dy: 20, precise: true), WheelAccumulator.Step(dx: 0, dy: 60))
        XCTAssertNil(wheel.add(dx: 0, dy: 0.1, precise: true))
        XCTAssertEqual(wheel.add(dx: 0, dy: 0.3, precise: true), WheelAccumulator.Step(dx: 0, dy: 1))
        XCTAssertEqual(wheel.add(dx: 4, dy: 0, precise: true), WheelAccumulator.Step(dx: -12, dy: 0))
    }

    // MARK: Keyboard

    func testCommandShortcutSendsControlCombination() {
        captureByClicking()
        let command = NSEvent.ModifierFlags.command.union(Device.leftCommand)
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x37, modifiers: command)))
        XCTAssertEqual(events, [])
        XCTAssertTrue(capture.handleKeyEvent(key(.keyDown, 0x08, "c", modifiers: command)))
        XCTAssertTrue(capture.handleKeyEvent(key(.keyUp, 0x08, "c", modifiers: command)))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x37)))
        XCTAssertEqual(events, [
            .key(.scancode(0x1D, extended: false), down: true),
            .key(.scancode(0x2E, extended: false), down: true),
            .key(.scancode(0x2E, extended: false), down: false),
            .key(.scancode(0x1D, extended: false), down: false),
        ])
    }

    func testModifierTapIsSentOnRelease() {
        captureByClicking()
        let control = NSEvent.ModifierFlags.control.union(Device.leftControl)
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3B, modifiers: control)))
        XCTAssertEqual(events, [])
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3B)))
        XCTAssertEqual(events, [
            .key(.scancode(0x5B, extended: true), down: true),
            .key(.scancode(0x5B, extended: true), down: false),
        ])
    }

    func testRightOptionSendsHangul() {
        captureByClicking()
        let rightOption = NSEvent.ModifierFlags.option.union(NSEvent.ModifierFlags(rawValue: 0x40))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3D, modifiers: rightOption)))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3D)))
        XCTAssertEqual(events, [
            .key(.virtualKey(KeyMap.vkHangul), down: true),
            .key(.virtualKey(KeyMap.vkHangul), down: false),
        ])
    }

    func testEscapeChordReleasesCapture() {
        captureByClicking()
        let control = NSEvent.ModifierFlags.control.union(Device.leftControl)
        let both = control.union(.option).union(Device.leftOption)
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3B, modifiers: control)))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3A, modifiers: both)))
        XCTAssertTrue(capture.isCaptured)
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3A, modifiers: control)))
        XCTAssertFalse(capture.isCaptured)
        XCTAssertEqual(events, [.releaseAll])
        XCTAssertEqual(captureChanges, [true, false])
        XCTAssertFalse(capture.handleKeyEvent(key(.flagsChanged, 0x3B)))
        XCTAssertEqual(events, [.releaseAll])
    }

    func testControlOptionWithAKeyIsSentInsteadOfEscaping() {
        captureByClicking()
        capture.keyMap = KeyMap(swapCommandControl: false)
        let control = NSEvent.ModifierFlags.control.union(Device.leftControl)
        let both = control.union(.option).union(Device.leftOption)
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3B, modifiers: control)))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3A, modifiers: both)))
        XCTAssertTrue(capture.handleKeyEvent(key(.keyDown, 0x75, "\u{F728}", modifiers: both)))
        XCTAssertTrue(capture.handleKeyEvent(key(.keyUp, 0x75, "\u{F728}", modifiers: both)))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3A, modifiers: control)))
        XCTAssertTrue(capture.handleKeyEvent(key(.flagsChanged, 0x3B)))
        XCTAssertTrue(capture.isCaptured)
        XCTAssertEqual(events, [
            .key(.scancode(0x38, extended: false), down: true),
            .key(.scancode(0x1D, extended: false), down: true),
            .key(.scancode(0x53, extended: true), down: true),
            .key(.scancode(0x53, extended: true), down: false),
            .key(.scancode(0x38, extended: false), down: false),
            .key(.scancode(0x1D, extended: false), down: false),
        ])
    }

    func testLocalShortcutsStayLocal() {
        captureByClicking()
        let local: [(UInt16, String, NSEvent.ModifierFlags)] = [
            (0x2F, ".", [.command, .shift]),
            (0x12, "1", [.command]),
            (0x1A, "7", [.command]),
            (0x1D, "0", [.command, .option]),
            (0x28, "k", [.command]),
            (0x2B, ",", [.command]),
        ]
        for (code, characters, modifiers) in local {
            XCTAssertFalse(capture.handleKeyEvent(key(.keyDown, code, characters, modifiers: modifiers)), characters)
        }
        XCTAssertEqual(events, [])
        XCTAssertTrue(capture.handleKeyEvent(key(.keyDown, 0x0C, "q", modifiers: .command)))
        XCTAssertEqual(events.last, .key(.scancode(0x10, extended: false), down: true))
    }

    func testFocusLossReleasesAll() {
        captureByClicking()
        XCTAssertTrue(capture.handleKeyEvent(key(.keyDown, 0x00, "a")))
        XCTAssertTrue(window.makeFirstResponder(nil))
        XCTAssertFalse(capture.isCaptured)
        XCTAssertEqual(events, [.key(.scancode(0x1E, extended: false), down: true), .releaseAll])
    }

    func testKeysAreIgnoredUntilCaptured() {
        XCTAssertFalse(capture.handleKeyEvent(key(.keyDown, 0x00, "a")))
        XCTAssertEqual(events, [])
    }

    // MARK: Mac input method

    func testMarkedTextNotSent() {
        capture.keyboardMode = .macInput
        captureByClicking()
        capture.setMarkedText("ㅎ", selectedRange: NSRange(location: 1, length: 0), replacementRange: NSRange(location: NSNotFound, length: 0))
        capture.setMarkedText("하", selectedRange: NSRange(location: 1, length: 0), replacementRange: NSRange(location: NSNotFound, length: 0))
        XCTAssertTrue(capture.hasMarkedText())
        XCTAssertEqual(capture.markedRange(), NSRange(location: 0, length: 1))
        XCTAssertEqual(capture.attributedSubstring(forProposedRange: NSRange(location: 0, length: 1), actualRange: nil)?.string, "하")
        XCTAssertEqual(events, [])
        capture.insertText("한", replacementRange: NSRange(location: NSNotFound, length: 0))
        XCTAssertFalse(capture.hasMarkedText())
        XCTAssertEqual(events, [.text("한")])
        capture.setMarkedText(NSAttributedString(string: "ㄱ"), selectedRange: NSRange(location: 1, length: 0),
                              replacementRange: NSRange(location: NSNotFound, length: 0))
        capture.unmarkText()
        XCTAssertEqual(events, [.text("한")])
    }

    func testMacInputModeSendsNavigationKeysAsScancodes() {
        capture.keyboardMode = .macInput
        captureByClicking()
        XCTAssertTrue(capture.handleKeyEvent(key(.keyDown, 0x7B, "\u{F702}")))
        XCTAssertTrue(capture.handleKeyEvent(key(.keyUp, 0x7B, "\u{F702}")))
        XCTAssertEqual(events, [
            .key(.scancode(0x4B, extended: true), down: true),
            .key(.scancode(0x4B, extended: true), down: false),
        ])
    }

    // MARK: Wire format

    func testEventsEncodeProtocolJSON() throws {
        func object(_ event: InputEvent) throws -> NSDictionary {
            try XCTUnwrap(JSONSerialization.jsonObject(with: event.json(seq: 3, gen: 9)) as? NSDictionary)
        }
        XCTAssertEqual(try object(.move(x: 1920, y: 1080, display: 1)),
                       ["seq": 3, "gen": 9, "k": "move", "x": 1920, "y": 1080, "display": 1])
        XCTAssertEqual(try object(.button(.x1, down: true)), ["seq": 3, "gen": 9, "k": "button", "b": "x1", "down": true])
        XCTAssertEqual(try object(.wheel(dx: 0, dy: -120)), ["seq": 3, "gen": 9, "k": "wheel", "dx": 0, "dy": -120])
        XCTAssertEqual(try object(.key(.scancode(0x4B, extended: true), down: false)),
                       ["seq": 3, "gen": 9, "k": "key", "sc": 0x4B, "ext": true, "down": false])
        XCTAssertEqual(try object(.key(.virtualKey(0x15), down: true)), ["seq": 3, "gen": 9, "k": "key", "vk": 0x15, "down": true])
        XCTAssertEqual(try object(.text("한글")), ["seq": 3, "gen": 9, "k": "text", "text": "한글", "method": "unicode"])
        XCTAssertEqual(try object(.releaseAll), ["seq": 3, "gen": 9, "k": "release_all"])
    }
}
