import AppKit
import Foundation

/// A mouse button as protocol.md §6 names it.
public enum MouseButton: String, Sendable, Hashable {
    case left
    case right
    case middle
    case x1
    case x2
}

/// One human input event for the agent (protocol.md §6). Coordinates are remote display pixels;
/// wheel deltas are `WHEEL_DELTA` units (120 per notch, positive `dy` scrolls up, positive `dx`
/// scrolls right).
public enum InputEvent: Equatable, Sendable {
    case move(x: Int, y: Int, display: Int)
    case button(MouseButton, down: Bool)
    case wheel(dx: Int, dy: Int)
    case key(KeyTarget, down: Bool)
    /// Committed text, injected as Unicode characters.
    case text(String)
    /// Releases every key and button the agent holds down.
    case releaseAll

    /// The event as the JSON object of a `0x41` frame.
    public func json(seq: Int, gen: Int) -> Data {
        var object: [String: Any] = ["seq": seq, "gen": gen]
        switch self {
        case .move(let x, let y, let display):
            object["k"] = "move"
            object["x"] = x
            object["y"] = y
            object["display"] = display
        case .button(let button, let down):
            object["k"] = "button"
            object["b"] = button.rawValue
            object["down"] = down
        case .wheel(let dx, let dy):
            object["k"] = "wheel"
            object["dx"] = dx
            object["dy"] = dy
        case .key(let target, let down):
            object["k"] = "key"
            switch target {
            case .scancode(let sc, let ext):
                object["sc"] = Int(sc)
                object["ext"] = ext
            case .virtualKey(let vk):
                object["vk"] = Int(vk)
            }
            object["down"] = down
        case .text(let text):
            object["k"] = "text"
            object["text"] = text
            object["method"] = "unicode"
        case .releaseAll:
            object["k"] = "release_all"
        }
        return (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
    }
}

/// How typed keys reach the remote PC.
public enum KeyboardMode: String, CaseIterable, Identifiable, Sendable {
    /// Keys go as scancodes and the Windows input method composes text.
    case scancode
    /// The Mac input method composes text and only committed text is sent.
    case macInput

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .scancode: .ui("keyboard.mode.scancode", "Windows 입력기 사용")
        case .macInput: .ui("keyboard.mode.macInput", "Mac 입력기 사용")
        }
    }

    public var detail: String {
        switch self {
        case .scancode: .ui("keyboard.mode.scancode.detail", "키를 그대로 보내고 한글 조합은 Windows가 합니다")
        case .macInput: .ui("keyboard.mode.macInput.detail", "이 Mac에서 조합을 끝낸 글자만 보냅니다")
        }
    }
}

/// Wheel deltas in `WHEEL_DELTA` units: a line-based notch is 120, a precise (trackpad) delta is
/// 120 per `pointsPerNotch` points. Fractions carry over to the next event.
struct WheelAccumulator {
    struct Step: Equatable {
        let dx: Int
        let dy: Int
    }

    static let pointsPerNotch = 40.0
    private var x = 0.0
    private var y = 0.0

    /// Adds a macOS scroll delta; positive `dy` reveals content above, positive `dx` content on the left.
    mutating func add(dx: Double, dy: Double, precise: Bool) -> Step? {
        let factor = precise ? 120 / Self.pointsPerNotch : 120
        x -= dx * factor
        y += dy * factor
        let stepX = Int(x.rounded(.towardZero))
        let stepY = Int(y.rounded(.towardZero))
        x -= Double(stepX)
        y -= Double(stepY)
        return stepX == 0 && stepY == 0 ? nil : Step(dx: stepX, dy: stepY)
    }

    mutating func reset() {
        x = 0
        y = 0
    }
}
