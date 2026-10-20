import CoreMedia
import Foundation

/// Failures turning screen stream bytes into sample buffers.
public enum MediaError: Error, Equatable, Sendable {
    /// A frame payload is shorter than its fixed header.
    case truncated(kind: UInt8, length: Int)
    /// An access unit carries no slice data.
    case emptyAccessUnit
    case formatDescription(OSStatus)
    case blockBuffer(OSStatus)
    case sampleBuffer(OSStatus)
}

/// Format descriptions for H.264 parameter sets.
public enum H264Format {
    /// A video format description for one SPS and one PPS, with 4-byte NAL length headers.
    public static func make(sps: Data, pps: Data) throws -> CMVideoFormatDescription {
        var format: CMVideoFormatDescription?
        let status = sps.withUnsafeBytes { spsBytes in
            pps.withUnsafeBytes { ppsBytes in
                guard let spsBase = spsBytes.bindMemory(to: UInt8.self).baseAddress,
                      let ppsBase = ppsBytes.bindMemory(to: UInt8.self).baseAddress else {
                    return kCMFormatDescriptionError_InvalidParameter
                }
                let pointers = [spsBase, ppsBase]
                let sizes = [sps.count, pps.count]
                return CMVideoFormatDescriptionCreateFromH264ParameterSets(
                    allocator: kCFAllocatorDefault, parameterSetCount: 2, parameterSetPointers: pointers,
                    parameterSetSizes: sizes, nalUnitHeaderLength: 4, formatDescriptionOut: &format)
            }
        }
        guard status == noErr, let format else { throw MediaError.formatDescription(status) }
        return format
    }
}

/// Turns the access units of one screen stream into sample buffers. Keeps the format
/// description of the latest SPS and PPS, replacing it when they change, and drops access
/// units until a keyframe arrives after the start or a `reset()`.
public struct H264Stream {
    public enum Output: Equatable {
        /// A sample buffer to display; `formatChanged` when it carries new parameter sets.
        case sample(CMSampleBuffer, formatChanged: Bool)
        /// The access unit cannot be decoded without a keyframe first.
        case needsKeyframe
    }

    public private(set) var format: CMVideoFormatDescription?
    private var sps: Data?
    private var pps: Data?
    private var waitingForKeyframe = true

    public init() {}

    /// True until a keyframe has been accepted since the start or the last `reset()`.
    public var needsKeyframe: Bool { waitingForKeyframe }

    public mutating func feed(_ au: AUFrame) throws -> Output {
        let nals = AnnexB.split(au.data)
        var newSPS: Data?
        var newPPS: Data?
        var hasIDR = false
        for nal in nals {
            switch AnnexB.type(of: nal) {
            case .sps: newSPS = nal
            case .pps: newPPS = nal
            case .idr: hasIDR = true
            default: break
            }
        }
        var formatChanged = false
        if let newSPS, let newPPS, newSPS != sps || newPPS != pps || format == nil {
            format = try H264Format.make(sps: newSPS, pps: newPPS)
            sps = newSPS
            pps = newPPS
            formatChanged = true
        }
        let key = au.key || hasIDR
        guard let format, !(waitingForKeyframe && !key) else { return .needsKeyframe }
        let buffer = try SampleBufferFactory.make(au: au, nals: nals, key: key, format: format)
        waitingForKeyframe = false
        return .sample(buffer, formatChanged: formatChanged)
    }

    /// Drops access units until the next keyframe.
    public mutating func reset() {
        waitingForKeyframe = true
    }
}
