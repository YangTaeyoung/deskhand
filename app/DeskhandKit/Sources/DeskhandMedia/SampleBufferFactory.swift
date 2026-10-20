import CoreMedia
import Foundation

/// One `0x01` access unit frame (protocol.md §4): flags, encoded size, sequence number, agent
/// monotonic timestamps and Annex-B bytes. All integers are big-endian.
public struct AUFrame: Sendable, Equatable {
    /// Bytes before the Annex-B data: flags (1), width (4), height (4), seq (8), capture (8), encode done (8).
    public static let headerLength = 33

    /// Flags bit 0: the access unit is a keyframe.
    public var key: Bool
    /// Flags bit 1: the access unit carries SPS and PPS.
    public var hasConfig: Bool
    public var width: UInt32
    public var height: UInt32
    public var seq: UInt64
    public var captureMonoUS: UInt64
    public var encodeDoneMonoUS: UInt64
    /// Annex-B bytes of the access unit.
    public var data: Data

    public init(key: Bool, hasConfig: Bool, width: UInt32, height: UInt32, seq: UInt64, captureMonoUS: UInt64,
                encodeDoneMonoUS: UInt64, data: Data) {
        self.key = key
        self.hasConfig = hasConfig
        self.width = width
        self.height = height
        self.seq = seq
        self.captureMonoUS = captureMonoUS
        self.encodeDoneMonoUS = encodeDoneMonoUS
        self.data = data
    }

    /// Parses a frame payload (the bytes after the kind byte).
    public init(payload: Data) throws {
        guard payload.count >= Self.headerLength else {
            throw MediaError.truncated(kind: ScreenFrameKind.accessUnit, length: payload.count)
        }
        var reader = BigEndianReader(payload)
        let flags = reader.u8()
        key = flags & 0b01 != 0
        hasConfig = flags & 0b10 != 0
        width = reader.u32()
        height = reader.u32()
        seq = reader.u64()
        captureMonoUS = reader.u64()
        encodeDoneMonoUS = reader.u64()
        data = reader.rest()
    }

    /// The frame payload in protocol layout.
    public var payload: Data {
        var out = Data(capacity: Self.headerLength + data.count)
        out.append((key ? 0b01 : 0) | (hasConfig ? 0b10 : 0))
        out.appendBigEndian(width)
        out.appendBigEndian(height)
        out.appendBigEndian(seq)
        out.appendBigEndian(captureMonoUS)
        out.appendBigEndian(encodeDoneMonoUS)
        out.append(data)
        return out
    }
}

/// Builds sample buffers for `AVSampleBufferDisplayLayer` from access units.
public enum SampleBufferFactory {
    /// A sample buffer holding the access unit's slices in AVCC form, without timing and marked
    /// to display immediately. Access units that are not keyframes are marked not-sync.
    public static func make(au: AUFrame, format: CMVideoFormatDescription) throws -> CMSampleBuffer {
        let nals = AnnexB.split(au.data)
        let key = au.key || nals.contains { AnnexB.type(of: $0) == .idr }
        return try make(au: au, nals: nals, key: key, format: format)
    }

    static func make(au: AUFrame, nals: [Data], key: Bool, format: CMVideoFormatDescription) throws -> CMSampleBuffer {
        let slices = nals.filter {
            switch AnnexB.type(of: $0) {
            case .sps, .pps, .accessUnitDelimiter, .endOfSequence, .endOfStream, .filler: false
            default: true
            }
        }
        guard !slices.isEmpty else { throw MediaError.emptyAccessUnit }
        let avcc = AnnexB.toAVCC(slices)

        var block: CMBlockBuffer?
        var status = CMBlockBufferCreateWithMemoryBlock(
            allocator: kCFAllocatorDefault, memoryBlock: nil, blockLength: avcc.count, blockAllocator: kCFAllocatorDefault,
            customBlockSource: nil, offsetToData: 0, dataLength: avcc.count, flags: kCMBlockBufferAssureMemoryNowFlag,
            blockBufferOut: &block)
        guard status == kCMBlockBufferNoErr, let block else { throw MediaError.blockBuffer(status) }
        status = avcc.withUnsafeBytes { bytes in
            CMBlockBufferReplaceDataBytes(with: bytes.baseAddress!, blockBuffer: block, offsetIntoDestination: 0,
                                          dataLength: avcc.count)
        }
        guard status == kCMBlockBufferNoErr else { throw MediaError.blockBuffer(status) }

        var sample: CMSampleBuffer?
        var size = avcc.count
        status = CMSampleBufferCreateReady(
            allocator: kCFAllocatorDefault, dataBuffer: block, formatDescription: format, sampleCount: 1,
            sampleTimingEntryCount: 0, sampleTimingArray: nil, sampleSizeEntryCount: 1, sampleSizeArray: &size,
            sampleBufferOut: &sample)
        guard status == noErr, let sample else { throw MediaError.sampleBuffer(status) }

        if let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true),
           CFArrayGetCount(attachments) > 0 {
            let dictionary = unsafeBitCast(CFArrayGetValueAtIndex(attachments, 0), to: CFMutableDictionary.self)
            CFDictionarySetValue(dictionary, Unmanaged.passUnretained(kCMSampleAttachmentKey_DisplayImmediately).toOpaque(),
                                 Unmanaged.passUnretained(kCFBooleanTrue).toOpaque())
            if !key {
                CFDictionarySetValue(dictionary, Unmanaged.passUnretained(kCMSampleAttachmentKey_NotSync).toOpaque(),
                                     Unmanaged.passUnretained(kCFBooleanTrue).toOpaque())
                CFDictionarySetValue(dictionary, Unmanaged.passUnretained(kCMSampleAttachmentKey_DependsOnOthers).toOpaque(),
                                     Unmanaged.passUnretained(kCFBooleanTrue).toOpaque())
            }
        }
        return sample
    }
}

/// Sequential big-endian reads from a payload whose length was already checked.
struct BigEndianReader {
    private let data: Data
    private var offset: Int

    init(_ data: Data) {
        self.data = data
        offset = data.startIndex
    }

    var remaining: Int { data.endIndex - offset }

    mutating func u8() -> UInt8 {
        defer { offset += 1 }
        return data[offset]
    }

    mutating func u16() -> UInt16 {
        UInt16(u8()) << 8 | UInt16(u8())
    }

    mutating func u32() -> UInt32 {
        UInt32(u16()) << 16 | UInt32(u16())
    }

    mutating func u64() -> UInt64 {
        UInt64(u32()) << 32 | UInt64(u32())
    }

    mutating func rest() -> Data {
        defer { offset = data.endIndex }
        return data.subdata(in: offset..<data.endIndex)
    }
}

extension Data {
    mutating func appendBigEndian<T: FixedWidthInteger>(_ value: T) {
        var big = value.bigEndian
        Swift.withUnsafeBytes(of: &big) { append(contentsOf: $0) }
    }
}
