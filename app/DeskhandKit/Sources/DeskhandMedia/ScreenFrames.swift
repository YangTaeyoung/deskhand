import CoreGraphics
import Foundation

/// Frame kinds of a `screen` stream connection (local-api.md §4, protocol.md §4–§5).
public enum ScreenFrameKind {
    /// Daemon → app: an H.264 access unit (`AUFrame`).
    public static let accessUnit: UInt8 = 0x01
    /// Daemon → app: JSON `StreamInfo`.
    public static let info: UInt8 = 0x02
    /// Daemon → app: JSON `StreamStats`, once a second.
    public static let stats: UInt8 = 0x03
    /// Daemon → app: JSON `{reason}`; capture stopped until frames resume.
    public static let paused: UInt8 = 0x04
    /// Daemon → app: `CursorShape`.
    public static let cursorShape: UInt8 = 0x10
    /// Daemon → app: `CursorPosition`.
    public static let cursorPosition: UInt8 = 0x11
    /// Daemon → app: JSON `StreamNotice`.
    public static let notice: UInt8 = 0x30
    /// App → daemon: JSON `ScreenControl`.
    public static let control: UInt8 = 0x40
    /// App → daemon: JSON input event (protocol.md §6).
    public static let input: UInt8 = 0x41
}

/// A cursor image (`0x10`): BGRA pixels with straight alpha and the hot spot, in remote pixels.
public struct CursorShape: Sendable, Equatable {
    public static let headerLength = 12

    public var id: UInt32
    public var width: Int
    public var height: Int
    public var hotX: Int
    public var hotY: Int
    /// `width × height × 4` bytes, row by row.
    public var bgra: Data

    public init(id: UInt32, width: Int, height: Int, hotX: Int, hotY: Int, bgra: Data) {
        self.id = id
        self.width = width
        self.height = height
        self.hotX = hotX
        self.hotY = hotY
        self.bgra = bgra
    }

    public init(payload: Data) throws {
        guard payload.count >= Self.headerLength else {
            throw MediaError.truncated(kind: ScreenFrameKind.cursorShape, length: payload.count)
        }
        var reader = BigEndianReader(payload)
        id = reader.u32()
        width = Int(reader.u16())
        height = Int(reader.u16())
        hotX = Int(reader.u16())
        hotY = Int(reader.u16())
        guard reader.remaining == width * height * 4 else {
            throw MediaError.truncated(kind: ScreenFrameKind.cursorShape, length: payload.count)
        }
        bgra = reader.rest()
    }

    public var payload: Data {
        var out = Data(capacity: Self.headerLength + bgra.count)
        out.appendBigEndian(id)
        out.appendBigEndian(UInt16(width))
        out.appendBigEndian(UInt16(height))
        out.appendBigEndian(UInt16(hotX))
        out.appendBigEndian(UInt16(hotY))
        out.append(bgra)
        return out
    }

    /// The pixels as an sRGB image, or nil for an empty shape.
    public func makeImage() -> CGImage? {
        guard width > 0, height > 0, let provider = CGDataProvider(data: bgra as CFData),
              let space = CGColorSpace(name: CGColorSpace.sRGB) else { return nil }
        let info = CGBitmapInfo(rawValue: CGBitmapInfo.byteOrder32Little.rawValue | CGImageAlphaInfo.first.rawValue)
        return CGImage(width: width, height: height, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: width * 4,
                       space: space, bitmapInfo: info, provider: provider, decode: nil, shouldInterpolate: true,
                       intent: .defaultIntent)
    }
}

/// Where the remote cursor is (`0x11`), in the captured display's pixels.
public struct CursorPosition: Sendable, Equatable {
    public static let length = 13

    public var x: Int32
    public var y: Int32
    public var visible: Bool
    public var shapeID: UInt32

    public init(x: Int32, y: Int32, visible: Bool, shapeID: UInt32) {
        self.x = x
        self.y = y
        self.visible = visible
        self.shapeID = shapeID
    }

    public init(payload: Data) throws {
        guard payload.count >= Self.length else {
            throw MediaError.truncated(kind: ScreenFrameKind.cursorPosition, length: payload.count)
        }
        var reader = BigEndianReader(payload)
        x = Int32(bitPattern: reader.u32())
        y = Int32(bitPattern: reader.u32())
        visible = reader.u8() != 0
        shapeID = reader.u32()
    }

    public var payload: Data {
        var out = Data(capacity: Self.length)
        out.appendBigEndian(UInt32(bitPattern: x))
        out.appendBigEndian(UInt32(bitPattern: y))
        out.append(visible ? 1 : 0)
        out.appendBigEndian(shapeID)
        return out
    }
}

/// The encoder's description of the stream (`0x02`).
public struct StreamInfo: Codable, Sendable, Equatable {
    public var codec: String?
    /// Encoded size.
    public var width: Int
    public var height: Int
    /// Size of the captured display before scaling.
    public var srcWidth: Int?
    public var srcHeight: Int?
    public var fps: Double?
    /// Target bits per second.
    public var bitrate: Double?
    /// Media Foundation transform name.
    public var encoder: String?
    public var hardware: Bool?
    /// `dd` (Desktop Duplication) or `gdi`.
    public var capture: String?
    public var display: Int?

    public init(codec: String? = "h264", width: Int, height: Int, srcWidth: Int? = nil, srcHeight: Int? = nil,
                fps: Double? = nil, bitrate: Double? = nil, encoder: String? = nil, hardware: Bool? = nil,
                capture: String? = nil, display: Int? = nil) {
        self.codec = codec
        self.width = width
        self.height = height
        self.srcWidth = srcWidth
        self.srcHeight = srcHeight
        self.fps = fps
        self.bitrate = bitrate
        self.encoder = encoder
        self.hardware = hardware
        self.capture = capture
        self.display = display
    }
}

/// The agent's encoder statistics over the last second (`0x03`).
public struct StreamStats: Codable, Sendable, Equatable {
    public var fps: Double?
    public var bitrate: Double?
    /// Frames captured but not encoded.
    public var dropped: Int?
    public var encodeMsP50: Double?
    public var encodeMsP95: Double?
    public var captureMsP50: Double?

    public init(fps: Double? = nil, bitrate: Double? = nil, dropped: Int? = nil, encodeMsP50: Double? = nil,
                encodeMsP95: Double? = nil, captureMsP50: Double? = nil) {
        self.fps = fps
        self.bitrate = bitrate
        self.dropped = dropped
        self.encodeMsP50 = encodeMsP50
        self.encodeMsP95 = encodeMsP95
        self.captureMsP50 = captureMsP50
    }
}

/// Why the agent stopped sending frames (`0x04` `reason`).
public enum PauseReason: Sendable, Equatable, Hashable {
    case noViewer
    case displayUnavailable
    /// UAC, Ctrl+Alt+Del or the lock screen is on the secure desktop.
    case secureDesktop
    case sessionLocked
    /// The RDP client minimized its window, so Windows stopped drawing the session.
    case rdpOutputSuppressed
    case other(String)

    public init(rawValue: String) {
        switch rawValue {
        case "no_viewer": self = .noViewer
        case "display_unavailable": self = .displayUnavailable
        case "secure_desktop": self = .secureDesktop
        case "session_locked": self = .sessionLocked
        case "rdp_output_suppressed": self = .rdpOutputSuppressed
        default: self = .other(rawValue)
        }
    }

    public var rawValue: String {
        switch self {
        case .noViewer: "no_viewer"
        case .displayUnavailable: "display_unavailable"
        case .secureDesktop: "secure_desktop"
        case .sessionLocked: "session_locked"
        case .rdpOutputSuppressed: "rdp_output_suppressed"
        case .other(let value): value
        }
    }
}

/// A daemon notice on the screen stream (`0x30`). The notice name is read from `type`, or
/// `kind` when `type` is absent.
public enum StreamNotice: Sendable, Equatable {
    /// Human input was dropped because an AI client holds control.
    case controlDenied(owner: String?, ownerClient: String?)
    case ownerChanged(owner: String?, ownerClient: String?, gen: Int?)
    /// The daemon's connection to the agent changed; frames stop until it is `ready` again.
    case connectionState(String)
    /// Capture-to-receive latency measured by the daemon with its agent clock offset.
    case latency(captureToReceiveMS: Double)
    case other(String)

    public init?(json: Data) {
        guard let object = (try? JSONSerialization.jsonObject(with: json)) as? [String: Any],
              let name = (object["type"] ?? object["kind"]) as? String else { return nil }
        switch name {
        case "control_denied":
            self = .controlDenied(owner: object["owner"] as? String, ownerClient: object["owner_client"] as? String)
        case "owner_changed":
            self = .ownerChanged(owner: object["owner"] as? String, ownerClient: object["owner_client"] as? String,
                                 gen: (object["gen"] as? NSNumber)?.intValue)
        case "connection_state":
            guard let state = object["state"] as? String else { return nil }
            self = .connectionState(state)
        case "latency":
            guard let ms = (object["capture_to_receive_ms"] as? NSNumber)?.doubleValue else { return nil }
            self = .latency(captureToReceiveMS: ms)
        default:
            self = .other(name)
        }
    }
}

/// App → daemon stream control (`0x40`).
public enum ScreenControl: Sendable, Equatable {
    case keyframe
    /// `sharp`, `balanced` or `saver`.
    case preset(String)

    public var json: Data {
        let object: [String: String] = switch self {
        case .keyframe: ["op": "keyframe"]
        case .preset(let preset): ["op": "preset", "preset": preset]
        }
        return (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
    }
}

/// Decoding of the JSON frames (`0x02`, `0x03`, `0x04`).
public enum ScreenJSON {
    public static func decode<T: Decodable>(_ type: T.Type, from data: Data) -> T? {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try? decoder.decode(type, from: data)
    }

    public static func encode(_ value: some Encodable) -> Data {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        return (try? encoder.encode(value)) ?? Data()
    }

    /// The `reason` of a `0x04` payload.
    public static func pauseReason(from data: Data) -> PauseReason? {
        guard let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              let reason = object["reason"] as? String else { return nil }
        return PauseReason(rawValue: reason)
    }
}
