/// A key as the Windows agent injects it: a set 1 scancode (with the E0 extended prefix) or a
/// virtual-key code.
public enum KeyTarget: Equatable, Hashable, Sendable {
    case scancode(UInt16, extended: Bool)
    case virtualKey(UInt16)
}

/// macOS virtual key codes (`kVK_*`) to Windows keys, with the modifier and Korean key settings
/// applied.
public struct KeyMap: Equatable, Sendable {
    /// `VK_HANGUL` (한/영).
    public static let vkHangul: UInt16 = 0x15
    /// `VK_HANJA` (한자).
    public static let vkHanja: UInt16 = 0x19

    /// Command keys send Control and Control keys send the Windows key.
    public var swapCommandControl: Bool
    /// Right Option sends 한/영 (`VK_HANGUL`) instead of right Alt.
    public var rightOptionHangul: Bool
    /// Right Command sends 한자 (`VK_HANJA`).
    public var rightCommandHanja: Bool

    public init(swapCommandControl: Bool = true, rightOptionHangul: Bool = true, rightCommandHanja: Bool = false) {
        self.swapCommandControl = swapCommandControl
        self.rightOptionHangul = rightOptionHangul
        self.rightCommandHanja = rightCommandHanja
    }

    /// The scancode of `keyCode` with the default settings, nil for keys that send a
    /// virtual-key code or nothing.
    public static func scancode(for keyCode: UInt16) -> (sc: UInt16, ext: Bool)? {
        KeyMap().scancode(for: keyCode)
    }

    public func scancode(for keyCode: UInt16) -> (sc: UInt16, ext: Bool)? {
        guard case .scancode(let sc, let ext) = key(for: keyCode) else { return nil }
        return (sc, ext)
    }

    /// What `keyCode` sends, or nil for keys Windows has no equivalent for (fn).
    public func key(for keyCode: UInt16) -> KeyTarget? {
        switch keyCode {
        case Mac.command: swapCommandControl ? .scancode(0x1D, extended: false) : .scancode(0x5B, extended: true)
        case Mac.rightCommand:
            if rightCommandHanja { .virtualKey(Self.vkHanja) }
            else { swapCommandControl ? .scancode(0x1D, extended: true) : .scancode(0x5C, extended: true) }
        case Mac.control: swapCommandControl ? .scancode(0x5B, extended: true) : .scancode(0x1D, extended: false)
        case Mac.rightControl: swapCommandControl ? .scancode(0x5C, extended: true) : .scancode(0x1D, extended: true)
        case Mac.rightOption: rightOptionHangul ? .virtualKey(Self.vkHangul) : .scancode(0x38, extended: true)
        case Mac.lang1: .virtualKey(Self.vkHangul)
        case Mac.lang2: .virtualKey(Self.vkHanja)
        default: Self.table[keyCode].map { .scancode($0.sc, extended: $0.ext) }
        }
    }

    /// True for the modifier keys that arrive as `flagsChanged` events.
    public static func isModifier(_ keyCode: UInt16) -> Bool {
        modifierKeyCodes.contains(keyCode)
    }

    static let modifierKeyCodes: Set<UInt16> = [
        Mac.command, Mac.rightCommand, Mac.shift, Mac.rightShift, Mac.option, Mac.rightOption,
        Mac.control, Mac.rightControl, Mac.capsLock, Mac.function,
    ]

    /// macOS virtual key codes used by name.
    enum Mac {
        static let `return`: UInt16 = 0x24
        static let tab: UInt16 = 0x30
        static let delete: UInt16 = 0x33
        static let escape: UInt16 = 0x35
        static let rightCommand: UInt16 = 0x36
        static let command: UInt16 = 0x37
        static let shift: UInt16 = 0x38
        static let capsLock: UInt16 = 0x39
        static let option: UInt16 = 0x3A
        static let control: UInt16 = 0x3B
        static let rightShift: UInt16 = 0x3C
        static let rightOption: UInt16 = 0x3D
        static let rightControl: UInt16 = 0x3E
        static let function: UInt16 = 0x3F
        static let lang2: UInt16 = 0x66
        static let lang1: UInt16 = 0x68
    }

    /// Set 1 scancodes of the remaining keys: the ANSI and ISO keyboard, function keys,
    /// navigation keys (extended), the keypad and the JIS extras.
    static let table: [UInt16: (sc: UInt16, ext: Bool)] = [
        0x00: (0x1E, false), // A
        0x01: (0x1F, false), // S
        0x02: (0x20, false), // D
        0x03: (0x21, false), // F
        0x04: (0x23, false), // H
        0x05: (0x22, false), // G
        0x06: (0x2C, false), // Z
        0x07: (0x2D, false), // X
        0x08: (0x2E, false), // C
        0x09: (0x2F, false), // V
        0x0A: (0x56, false), // ISO section (102nd key)
        0x0B: (0x30, false), // B
        0x0C: (0x10, false), // Q
        0x0D: (0x11, false), // W
        0x0E: (0x12, false), // E
        0x0F: (0x13, false), // R
        0x10: (0x15, false), // Y
        0x11: (0x14, false), // T
        0x12: (0x02, false), // 1
        0x13: (0x03, false), // 2
        0x14: (0x04, false), // 3
        0x15: (0x05, false), // 4
        0x16: (0x07, false), // 6
        0x17: (0x06, false), // 5
        0x18: (0x0D, false), // =
        0x19: (0x0A, false), // 9
        0x1A: (0x08, false), // 7
        0x1B: (0x0C, false), // -
        0x1C: (0x09, false), // 8
        0x1D: (0x0B, false), // 0
        0x1E: (0x1B, false), // ]
        0x1F: (0x18, false), // O
        0x20: (0x16, false), // U
        0x21: (0x1A, false), // [
        0x22: (0x17, false), // I
        0x23: (0x19, false), // P
        0x24: (0x1C, false), // Return
        0x25: (0x26, false), // L
        0x26: (0x24, false), // J
        0x27: (0x28, false), // '
        0x28: (0x25, false), // K
        0x29: (0x27, false), // ;
        0x2A: (0x2B, false), // backslash
        0x2B: (0x33, false), // ,
        0x2C: (0x35, false), // /
        0x2D: (0x31, false), // N
        0x2E: (0x32, false), // M
        0x2F: (0x34, false), // .
        0x30: (0x0F, false), // Tab
        0x31: (0x39, false), // Space
        0x32: (0x29, false), // `
        0x33: (0x0E, false), // Delete → Backspace
        0x35: (0x01, false), // Escape
        0x38: (0x2A, false), // Shift
        0x39: (0x3A, false), // Caps Lock
        0x3A: (0x38, false), // Option → Alt
        0x3C: (0x36, false), // Right Shift
        0x40: (0x68, false), // F17
        0x41: (0x53, false), // Keypad .
        0x43: (0x37, false), // Keypad *
        0x45: (0x4E, false), // Keypad +
        0x47: (0x45, false), // Keypad Clear → Num Lock
        0x48: (0x30, true), // Volume Up
        0x49: (0x2E, true), // Volume Down
        0x4A: (0x20, true), // Mute
        0x4B: (0x35, true), // Keypad /
        0x4C: (0x1C, true), // Keypad Enter
        0x4E: (0x4A, false), // Keypad -
        0x4F: (0x69, false), // F18
        0x50: (0x6A, false), // F19
        0x51: (0x59, false), // Keypad =
        0x52: (0x52, false), // Keypad 0
        0x53: (0x4F, false), // Keypad 1
        0x54: (0x50, false), // Keypad 2
        0x55: (0x51, false), // Keypad 3
        0x56: (0x4B, false), // Keypad 4
        0x57: (0x4C, false), // Keypad 5
        0x58: (0x4D, false), // Keypad 6
        0x59: (0x47, false), // Keypad 7
        0x5A: (0x6B, false), // F20
        0x5B: (0x48, false), // Keypad 8
        0x5C: (0x49, false), // Keypad 9
        0x5D: (0x7D, false), // JIS Yen
        0x5E: (0x73, false), // JIS Underscore (Ro)
        0x5F: (0x7E, false), // JIS Keypad ,
        0x60: (0x3F, false), // F5
        0x61: (0x40, false), // F6
        0x62: (0x41, false), // F7
        0x63: (0x3D, false), // F3
        0x64: (0x42, false), // F8
        0x65: (0x43, false), // F9
        0x67: (0x57, false), // F11
        0x69: (0x64, false), // F13
        0x6A: (0x67, false), // F16
        0x6B: (0x65, false), // F14
        0x6D: (0x44, false), // F10
        0x6E: (0x5D, true), // Context menu → Application
        0x6F: (0x58, false), // F12
        0x71: (0x66, false), // F15
        0x72: (0x52, true), // Help → Insert
        0x73: (0x47, true), // Home
        0x74: (0x49, true), // Page Up
        0x75: (0x53, true), // Forward Delete
        0x76: (0x3E, false), // F4
        0x77: (0x4F, true), // End
        0x78: (0x3C, false), // F2
        0x79: (0x51, true), // Page Down
        0x7A: (0x3B, false), // F1
        0x7B: (0x4B, true), // Left
        0x7C: (0x4D, true), // Right
        0x7D: (0x50, true), // Down
        0x7E: (0x48, true), // Up
    ]
}
