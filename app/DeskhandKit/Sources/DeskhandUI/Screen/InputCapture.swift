import AppKit
import Foundation

/// The view over the remote screen that turns mouse, wheel, keyboard and text input into
/// `InputEvent`s while it captures input.
///
/// A click in the view captures input (the view becomes first responder) unless an AI client
/// holds control; then the click only calls `onTakeoverRequest`. While captured, every key goes
/// to the remote PC except the local shortcuts (⇧⌘., ⌘1–⌘7, ⌥⌘0, ⌘K, ⌘, and, unless
/// `forwardsSystemShortcuts`, ⌘Q, ⌘H and ⌘Tab). Pressing ⌃ and either ⌥ together and releasing
/// them with nothing else ends the capture. Mouse moves are sent at most every `moveInterval`;
/// buttons, keys and wheel flush the pending move first. Modifier keys (and 한/영, 한자) are held
/// back until another key or a button needs them; one pressed and released alone is sent as a
/// tap, except ⌃ and keys that send the Windows key. Ending the capture (focus loss, window
/// deactivation, the escape chord) sends `release_all`.
@MainActor
public final class InputCapture: NSView, @MainActor NSTextInputClient {
    public static let moveInterval: TimeInterval = 1.0 / 120

    /// Who holds control. Turning to `.ai` ends the capture without sending anything.
    public var owner: ControlOwner = .none {
        didSet {
            guard owner != oldValue else { return }
            if owner == .ai {
                endCapture(sendRelease: false)
            } else if owner == .human, let requested = takeoverRequestedAt, clock() - requested < 60 {
                takeoverRequestedAt = nil
                beginCapture()
            }
        }
    }

    /// False while the screen is not live; disabling ends the capture.
    public var isInputEnabled = true {
        didSet { if !isInputEnabled { endCapture() } }
    }

    /// Geometry of the remote display in this view.
    public var mapper: PointMapper? {
        didSet { updateCursor() }
    }

    /// Index of the remote display, sent with every move.
    public var display = 0
    public var keyMap = KeyMap()
    public var keyboardMode: KeyboardMode = .scancode {
        didSet { if keyboardMode != oldValue { clearMarkedText(discardingInput: true) } }
    }

    /// ⌘Q, ⌘H and ⌘Tab go to the remote PC too. ⌘Tab reaches the view only through
    /// `SystemShortcutTap`, which needs the Accessibility permission.
    public var forwardsSystemShortcuts = false {
        didSet { updateSystemShortcutTap() }
    }

    /// True while the remote cursor is drawn locally; the system cursor is then hidden over the
    /// video while captured, unless `showsLocalCursor` is on.
    public var drawsRemoteCursor = false {
        didSet { if drawsRemoteCursor != oldValue { updateCursor() } }
    }

    /// Keeps the system cursor visible over the video while captured.
    public var showsLocalCursor = false {
        didSet { if showsLocalCursor != oldValue { updateCursor() } }
    }

    public var onEvent: (InputEvent) -> Void = { _ in }
    public var onTakeoverRequest: () -> Void = {}
    public var onCaptureChange: (Bool) -> Void = { _ in }
    /// The local pointer in view coordinates while captured over the video; nil otherwise.
    public var onPointer: (CGPoint?) -> Void = { _ in }
    /// Text the Mac input method is composing, or nil.
    public var onMarkedText: (String?) -> Void = { _ in }

    /// Monotonic seconds, for the move rate limit.
    var clock: () -> TimeInterval = { ProcessInfo.processInfo.systemUptime }

    public private(set) var isCaptured = false

    private enum HeldModifier {
        case deferred(KeyTarget)
        case sent(KeyTarget)
    }

    private var lastMove: CGPoint?
    private var lastMoveTime = -TimeInterval.infinity
    private var pendingMove: CGPoint?
    private var flushScheduled = false
    private var pressedButtons: Set<MouseButton> = []
    /// Keys sent down, by macOS key code, with what was sent.
    private var pressedKeys: [UInt16: KeyTarget] = [:]
    private var heldModifiers: [UInt16: HeldModifier] = [:]
    /// ⌃ and ⌥ are down and nothing else was pressed since.
    private var escapeChordArmed = false
    private var wheel = WheelAccumulator()
    private var markedText: String?
    /// Selection inside `markedText` that the input method set.
    private var markedSelection = NSRange(location: 0, length: 0)
    private var currentKeyEvent: NSEvent?
    private var keyMonitor: Any?
    private var takeoverRequestedAt: TimeInterval?
    private var trackingArea: NSTrackingArea?
    private var hidesCursor = false
    private var windowObserver: NSObjectProtocol?
    private var resigning = false
    private lazy var systemShortcutTap = SystemShortcutTap { [weak self] event in
        self?.handleKeyEvent(event) ?? false
    }

    private static let blankCursor = NSCursor(image: NSImage(size: NSSize(width: 1, height: 1)), hotSpot: .zero)

    public override init(frame: NSRect) {
        super.init(frame: frame)
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    public override var isFlipped: Bool { true }

    /// VoiceOver activation: asks to take control while an AI client holds it, otherwise
    /// starts capturing input without clicking the remote PC.
    public override func accessibilityPerformPress() -> Bool {
        if owner == .ai {
            takeoverRequestedAt = clock()
            onTakeoverRequest()
        } else {
            beginCapture()
        }
        return true
    }
    public override var acceptsFirstResponder: Bool { owner != .ai && isInputEnabled }
    public override func acceptsFirstMouse(for event: NSEvent?) -> Bool { false }

    // MARK: Capture

    /// Makes the view first responder, which starts capturing input.
    public func beginCapture() {
        guard !isCaptured, owner != .ai, isInputEnabled, let window else { return }
        window.makeFirstResponder(self)
    }

    /// Stops capturing: sends `release_all` when `sendRelease`, forgets held keys and buttons and
    /// gives up first responder.
    public func endCapture(sendRelease: Bool = true) {
        guard isCaptured else { return }
        isCaptured = false
        if let keyMonitor { NSEvent.removeMonitor(keyMonitor) }
        keyMonitor = nil
        NSObject.cancelPreviousPerformRequests(withTarget: self, selector: #selector(flushTimerFired), object: nil)
        flushScheduled = false
        pendingMove = nil
        pressedButtons = []
        pressedKeys = [:]
        heldModifiers = [:]
        escapeChordArmed = false
        wheel.reset()
        if sendRelease {
            commitMarkedText()
        } else {
            clearMarkedText(discardingInput: true)
        }
        lastMove = nil
        lastMoveTime = -.infinity
        systemShortcutTap.stop()
        if sendRelease { onEvent(.releaseAll) }
        if !resigning, window?.firstResponder === self { window?.makeFirstResponder(nil) }
        onPointer(nil)
        updateCursor()
        onCaptureChange(false)
    }

    public override func becomeFirstResponder() -> Bool {
        guard owner != .ai, isInputEnabled, super.becomeFirstResponder() else { return false }
        if !isCaptured {
            isCaptured = true
            lastMove = nil
            lastMoveTime = -.infinity
            installKeyMonitor()
            updateSystemShortcutTap()
            updateCursor()
            onCaptureChange(true)
        }
        return true
    }

    public override func resignFirstResponder() -> Bool {
        resigning = true
        endCapture()
        resigning = false
        return super.resignFirstResponder()
    }

    public override func viewWillMove(toWindow newWindow: NSWindow?) {
        if newWindow !== window { endCapture() }
        if let windowObserver { NotificationCenter.default.removeObserver(windowObserver) }
        windowObserver = nil
        super.viewWillMove(toWindow: newWindow)
    }

    public override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        guard let window else { return }
        windowObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.didResignKeyNotification, object: window, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.endCapture() }
        }
    }

    /// Routes key events of this window to `handleKeyEvent(_:)` before menu key equivalents
    /// are matched; consumed events do not reach the menus.
    private func installKeyMonitor() {
        guard keyMonitor == nil else { return }
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .keyUp, .flagsChanged]) { [weak self] event in
            let consumed = MainActor.assumeIsolated {
                guard let self, event.window === self.window, self.window?.firstResponder === self else { return false }
                return self.handleKeyEvent(event)
            }
            return consumed ? nil : event
        }
    }

    private func updateSystemShortcutTap() {
        if isCaptured, forwardsSystemShortcuts {
            systemShortcutTap.start()
        } else {
            systemShortcutTap.stop()
        }
    }

    // MARK: Cursor

    public override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let trackingArea { removeTrackingArea(trackingArea) }
        let area = NSTrackingArea(rect: .zero,
                                  options: [.mouseMoved, .mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect, .cursorUpdate],
                                  owner: self)
        addTrackingArea(area)
        trackingArea = area
    }

    public override func cursorUpdate(with event: NSEvent) {
        updateCursor(at: convert(event.locationInWindow, from: nil))
    }

    /// Hides the system cursor over the video while captured and the remote cursor is drawn
    /// locally, unless `showsLocalCursor` is on.
    private func updateCursor(at point: CGPoint? = nil) {
        guard let window else { return }
        let location = point ?? convert(window.mouseLocationOutsideOfEventStream, from: nil)
        let over = mapper?.remotePoint(from: location) != nil
        let hide = isCaptured && drawsRemoteCursor && over && !showsLocalCursor
        guard hide != hidesCursor || point != nil else { return }
        hidesCursor = hide
        (hide ? Self.blankCursor : NSCursor.arrow).set()
    }

    // MARK: Mouse

    public override func mouseDown(with event: NSEvent) { button(.left, down: true, event) }
    public override func mouseUp(with event: NSEvent) { button(.left, down: false, event) }
    public override func rightMouseDown(with event: NSEvent) { button(.right, down: true, event) }
    public override func rightMouseUp(with event: NSEvent) { button(.right, down: false, event) }
    public override func otherMouseDown(with event: NSEvent) { button(Self.otherButton(event), down: true, event) }
    public override func otherMouseUp(with event: NSEvent) { button(Self.otherButton(event), down: false, event) }
    public override func mouseMoved(with event: NSEvent) { moved(event, dragging: false) }
    public override func mouseDragged(with event: NSEvent) { moved(event, dragging: true) }
    public override func rightMouseDragged(with event: NSEvent) { moved(event, dragging: true) }
    public override func otherMouseDragged(with event: NSEvent) { moved(event, dragging: true) }

    public override func mouseExited(with event: NSEvent) {
        if pressedButtons.isEmpty { onPointer(nil) }
        updateCursor(at: convert(event.locationInWindow, from: nil))
    }

    private static func otherButton(_ event: NSEvent) -> MouseButton {
        switch event.buttonNumber {
        case 3: .x1
        case 4: .x2
        default: .middle
        }
    }

    private func button(_ button: MouseButton, down: Bool, _ event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        if down {
            if owner == .ai {
                takeoverRequestedAt = clock()
                onTakeoverRequest()
                return
            }
            guard isInputEnabled else { return }
            if !isCaptured { beginCapture() }
            guard isCaptured, let remote = mapper?.remotePoint(from: point) else { return }
            commitMarkedText()
            flushPendingMove()
            holdModifiers(in: event.modifierFlags)
            flushModifiers()
            sendMove(remote)
            pressedButtons.insert(button)
            onEvent(.button(button, down: true))
        } else {
            guard isCaptured, pressedButtons.remove(button) != nil, let mapper else { return }
            flushPendingMove()
            sendMove(mapper.clampedRemotePoint(from: point))
            onEvent(.button(button, down: false))
        }
    }

    private func moved(_ event: NSEvent, dragging: Bool) {
        let point = convert(event.locationInWindow, from: nil)
        updateCursor(at: point)
        guard isCaptured, let mapper else { return }
        let remote = pressedButtons.isEmpty ? mapper.remotePoint(from: point) : mapper.clampedRemotePoint(from: point)
        guard let remote else {
            onPointer(nil)
            return
        }
        let video = mapper.videoRect
        onPointer(CGPoint(x: min(max(point.x, video.minX), video.maxX), y: min(max(point.y, video.minY), video.maxY)))
        queueMove(remote)
    }

    public override func scrollWheel(with event: NSEvent) {
        guard isCaptured, mapper?.remotePoint(from: convert(event.locationInWindow, from: nil)) != nil else { return }
        guard let step = wheel.add(dx: event.scrollingDeltaX, dy: event.scrollingDeltaY,
                                   precise: event.hasPreciseScrollingDeltas) else { return }
        flushPendingMove()
        flushModifiers()
        onEvent(.wheel(dx: step.dx, dy: step.dy))
    }

    private func queueMove(_ remote: CGPoint) {
        let elapsed = clock() - lastMoveTime
        if pendingMove == nil, elapsed >= Self.moveInterval {
            sendMove(remote)
            return
        }
        pendingMove = remote
        guard !flushScheduled else { return }
        flushScheduled = true
        perform(#selector(flushTimerFired), with: nil, afterDelay: max(0, Self.moveInterval - elapsed),
                inModes: [.common])
    }

    @objc private func flushTimerFired() {
        flushScheduled = false
        flushPendingMove()
    }

    /// Sends the move held back by the rate limit, if any.
    func flushPendingMove() {
        guard let pending = pendingMove else { return }
        pendingMove = nil
        sendMove(pending)
    }

    private func sendMove(_ remote: CGPoint) {
        lastMoveTime = clock()
        guard remote != lastMove else { return }
        lastMove = remote
        onEvent(.move(x: Int(remote.x), y: Int(remote.y), display: display))
    }

    // MARK: Keyboard

    public override func keyDown(with event: NSEvent) {
        if !handleKeyEvent(event) { super.keyDown(with: event) }
    }

    public override func keyUp(with event: NSEvent) {
        if !handleKeyEvent(event) { super.keyUp(with: event) }
    }

    public override func flagsChanged(with event: NSEvent) {
        if !handleKeyEvent(event) { super.flagsChanged(with: event) }
    }

    public override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard window?.firstResponder === self else { return super.performKeyEquivalent(with: event) }
        return handleKeyEvent(event) || super.performKeyEquivalent(with: event)
    }

    /// Sends a key event to the remote PC while captured. Returns false for events left to the
    /// app: local shortcuts, and everything while not captured.
    func handleKeyEvent(_ event: NSEvent) -> Bool {
        guard isCaptured, isInputEnabled, owner != .ai else { return false }
        switch event.type {
        case .flagsChanged:
            modifierChanged(event)
            return true
        case .keyDown:
            if Self.isLocalShortcut(event, forwardingSystem: forwardsSystemShortcuts) { return false }
            escapeChordArmed = false
            if keyboardMode == .macInput {
                guard Self.bypassesInputMethod(event) else {
                    currentKeyEvent = event
                    interpretKeyEvents([event])
                    currentKeyEvent = nil
                    return true
                }
                commitMarkedText()
            }
            guard let target = pressedKeys[event.keyCode] ?? keyMap.key(for: event.keyCode) else { return true }
            flushPendingMove()
            flushModifiers()
            pressedKeys[event.keyCode] = target
            onEvent(.key(target, down: true))
            return true
        case .keyUp:
            if let target = pressedKeys.removeValue(forKey: event.keyCode) {
                onEvent(.key(target, down: false))
                return true
            }
            return !Self.isLocalShortcut(event, forwardingSystem: forwardsSystemShortcuts)
        default:
            return false
        }
    }

    /// ⇧⌘. (emergency stop), ⌘1–⌘7 (tabs), ⌥⌘0 (inspector), ⌘K (quick switch), ⌘, (settings),
    /// and ⌘Q, ⌘H and ⌘Tab unless `forwardingSystem`.
    static func isLocalShortcut(_ event: NSEvent, forwardingSystem: Bool = false) -> Bool {
        let flags = event.modifierFlags.intersection([.command, .shift, .option, .control])
        let characters = event.charactersIgnoringModifiers?.lowercased() ?? ""
        func key(_ character: String, _ keyCode: UInt16) -> Bool {
            characters == character || event.keyCode == keyCode
        }
        switch flags {
        case [.command, .shift]:
            return key(".", 0x2F) || characters == ">"
        case [.command, .option]:
            return key("0", 0x1D)
        case [.command]:
            let digits: [(String, UInt16)] = [("1", 0x12), ("2", 0x13), ("3", 0x14), ("4", 0x15), ("5", 0x17), ("6", 0x16), ("7", 0x1A)]
            if digits.contains(where: { key($0.0, $0.1) }) || key("k", 0x28) || key(",", 0x2B) { return true }
            return !forwardingSystem && (key("q", 0x0C) || key("h", 0x04) || event.keyCode == 0x30)
        default:
            return false
        }
    }

    /// Keys that skip the Mac input method in `macInput` mode: navigation, function and escape
    /// keys, and anything typed with ⌘ or ⌃.
    static func bypassesInputMethod(_ event: NSEvent) -> Bool {
        if !event.modifierFlags.intersection([.command, .control]).isEmpty { return true }
        switch event.keyCode {
        case 0x35, 0x72, 0x73, 0x74, 0x75, 0x77, 0x79, 0x7B, 0x7C, 0x7D, 0x7E: return true
        default: return KeyMap.functionKeyCodes.contains(event.keyCode)
        }
    }

    /// Device-dependent modifier bits for each modifier key (IOLLEvent.h `NX_DEVICE*KEYMASK`).
    private static let deviceMasks: [UInt16: UInt] = [
        KeyMap.Mac.control: 0x0001, KeyMap.Mac.rightControl: 0x2000,
        KeyMap.Mac.shift: 0x0002, KeyMap.Mac.rightShift: 0x0004,
        KeyMap.Mac.command: 0x0008, KeyMap.Mac.rightCommand: 0x0010,
        KeyMap.Mac.option: 0x0020, KeyMap.Mac.rightOption: 0x0040,
    ]

    private static func family(_ keyCode: UInt16) -> NSEvent.ModifierFlags {
        switch keyCode {
        case KeyMap.Mac.control, KeyMap.Mac.rightControl: .control
        case KeyMap.Mac.shift, KeyMap.Mac.rightShift: .shift
        case KeyMap.Mac.command, KeyMap.Mac.rightCommand: .command
        case KeyMap.Mac.option, KeyMap.Mac.rightOption: .option
        default: []
        }
    }

    /// Whether the modifier `keyCode` is down after `event`: from its device bit, or by toggling
    /// the held state when the event carries no device bits for its family.
    private func isDown(_ keyCode: UInt16, in event: NSEvent) -> Bool {
        let family = Self.family(keyCode)
        guard event.modifierFlags.contains(family) else { return false }
        let raw = event.modifierFlags.rawValue
        let familyMask = Self.deviceMasks.filter { Self.family($0.key) == family }.reduce(UInt(0)) { $0 | $1.value }
        if raw & familyMask != 0, let mask = Self.deviceMasks[keyCode] { return raw & mask != 0 }
        return heldModifiers[keyCode] == nil
    }

    private func modifierChanged(_ event: NSEvent) {
        let code = event.keyCode
        if code == KeyMap.Mac.capsLock, let target = keyMap.key(for: code) {
            flushModifiers()
            onEvent(.key(target, down: true))
            onEvent(.key(target, down: false))
            return
        }
        guard Self.deviceMasks[code] != nil else { return }
        if isDown(code, in: event) {
            guard heldModifiers[code] == nil, let target = keyMap.key(for: code) else { return }
            heldModifiers[code] = .deferred(target)
            let held = event.modifierFlags.intersection([.command, .shift, .option, .control])
            let allDeferred = heldModifiers.values.allSatisfy { if case .deferred = $0 { true } else { false } }
            escapeChordArmed = held == [.control, .option] && allDeferred && pressedKeys.isEmpty && pressedButtons.isEmpty
        } else {
            if escapeChordArmed, Self.family(code) == .control || Self.family(code) == .option {
                endCapture()
                return
            }
            escapeChordArmed = false
            guard let held = heldModifiers.removeValue(forKey: code) else { return }
            switch held {
            case .sent(let target):
                onEvent(.key(target, down: false))
            case .deferred(let target):
                guard sendsAloneTap(code, target) else { return }
                onEvent(.key(target, down: true))
                onEvent(.key(target, down: false))
            }
        }
    }

    /// Whether a key pressed and released alone is sent as a tap: 한/영 and 한자 always; never ⌃
    /// or a key that sends the Windows key; other modifiers in `scancode` mode only.
    private func sendsAloneTap(_ keyCode: UInt16, _ target: KeyTarget) -> Bool {
        switch target {
        case .virtualKey:
            return true
        case .scancode(let sc, let extended):
            if Self.family(keyCode) == .control { return false }
            if extended, sc == 0x5B || sc == 0x5C { return false }
            return keyboardMode == .scancode
        }
    }

    /// Records modifiers that `flags` shows down but that were not seen pressed, such as keys
    /// held before the capturing click.
    private func holdModifiers(in flags: NSEvent.ModifierFlags) {
        let raw = flags.rawValue
        for (code, mask) in Self.deviceMasks.sorted(by: { $0.key < $1.key }) where raw & mask != 0 && heldModifiers[code] == nil {
            if let target = keyMap.key(for: code) { heldModifiers[code] = .deferred(target) }
        }
        let leftKeys: [(NSEvent.ModifierFlags, UInt16)] = [
            (.control, KeyMap.Mac.control), (.shift, KeyMap.Mac.shift), (.command, KeyMap.Mac.command), (.option, KeyMap.Mac.option),
        ]
        for (family, code) in leftKeys where flags.contains(family) {
            let familyHeld = heldModifiers.keys.contains { Self.family($0) == family }
            if !familyHeld, let target = keyMap.key(for: code) { heldModifiers[code] = .deferred(target) }
        }
    }

    /// Sends the modifiers held back so far, before the key or button they modify.
    private func flushModifiers() {
        escapeChordArmed = false
        for (code, held) in heldModifiers.sorted(by: { $0.key < $1.key }) {
            guard case .deferred(let target) = held else { continue }
            heldModifiers[code] = .sent(target)
            onEvent(.key(target, down: true))
        }
    }

    // MARK: Mac input method (NSTextInputClient)

    public func insertText(_ string: Any, replacementRange: NSRange) {
        clearMarkedText(discardingInput: false)
        let text = (string as? NSAttributedString)?.string ?? (string as? String) ?? ""
        guard isCaptured, !text.isEmpty else { return }
        flushPendingMove()
        onEvent(.text(text))
    }

    public override func doCommand(by selector: Selector) {
        guard isCaptured, let event = currentKeyEvent, let target = keyMap.key(for: event.keyCode) else { return }
        flushPendingMove()
        flushModifiers()
        onEvent(.key(target, down: true))
        onEvent(.key(target, down: false))
    }

    public func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        let text = (string as? NSAttributedString)?.string ?? (string as? String) ?? ""
        markedText = text.isEmpty ? nil : text
        markedSelection = selectedRange
        onMarkedText(markedText)
    }

    public func unmarkText() {
        clearMarkedText(discardingInput: false)
    }

    /// Sends the composing text as committed text and tells the input method to drop it.
    private func commitMarkedText() {
        guard let text = markedText else { return }
        clearMarkedText(discardingInput: true)
        flushPendingMove()
        onEvent(.text(text))
    }

    /// Forgets the composing text; `discardingInput` also tells the input method to drop it.
    private func clearMarkedText(discardingInput: Bool) {
        guard markedText != nil else { return }
        markedText = nil
        markedSelection = NSRange(location: 0, length: 0)
        if discardingInput { inputContext?.discardMarkedText() }
        onMarkedText(nil)
    }

    /// The document of this client is the composing text alone.
    public func selectedRange() -> NSRange {
        markedText == nil ? NSRange(location: 0, length: 0) : markedSelection
    }

    public func markedRange() -> NSRange {
        guard let markedText else { return NSRange(location: NSNotFound, length: 0) }
        return NSRange(location: 0, length: markedText.utf16.count)
    }

    public func hasMarkedText() -> Bool {
        markedText != nil
    }

    /// The part of the composing text in `range`, which the input method reads back while composing.
    public func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? {
        guard let markedText else { return nil }
        let text = markedText as NSString
        let clipped = NSIntersectionRange(range, NSRange(location: 0, length: text.length))
        guard clipped.location != NSNotFound, clipped.location <= text.length else { return nil }
        actualRange?.pointee = clipped
        return NSAttributedString(string: text.substring(with: clipped))
    }

    public func validAttributesForMarkedText() -> [NSAttributedString.Key] {
        [.underlineStyle, .markedClauseSegment]
    }

    /// A rect at the remote cursor (or the bottom of the video) for the input method's candidate window.
    public func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        guard let window else { return .zero }
        let anchor = mapper.map { CGRect(x: $0.videoRect.minX + DS.Space.l, y: $0.videoRect.maxY - 40, width: 1, height: 20) }
            ?? CGRect(x: 0, y: 0, width: 1, height: 20)
        return window.convertToScreen(convert(anchor, to: nil))
    }

    public func characterIndex(for point: NSPoint) -> Int {
        NSNotFound
    }
}
