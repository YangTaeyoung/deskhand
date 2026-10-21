import Foundation

/// H.264 NAL unit types (ITU-T H.264 Table 7-1) the screen stream uses.
public enum NALType: UInt8, Sendable {
    case slice = 1
    case idr = 5
    case sei = 6
    case sps = 7
    case pps = 8
    case accessUnitDelimiter = 9
    case endOfSequence = 10
    case endOfStream = 11
    case filler = 12
}

/// Annex-B byte streams: NAL units separated by `00 00 01` or `00 00 00 01` start codes.
public enum AnnexB {
    /// The NAL units of `data` without their start codes. Zero bytes before a start code (the
    /// leading byte of a four-byte start code, or trailing zero padding) are not part of the
    /// preceding unit; bytes before the first start code are skipped.
    public static func split(_ data: Data) -> [Data] {
        data.withUnsafeBytes { raw -> [Data] in
            let bytes = raw.bindMemory(to: UInt8.self)
            let count = bytes.count
            var units: [Data] = []
            var start: Int?
            var i = 0
            while i + 2 < count {
                if bytes[i + 2] > 1 {
                    i += 3
                } else if bytes[i] == 0, bytes[i + 1] == 0, bytes[i + 2] == 1 {
                    if let start {
                        units.append(unit(bytes, from: start, to: i))
                    }
                    start = i + 3
                    i += 3
                } else {
                    i += 1
                }
            }
            if let start, start < count {
                units.append(unit(bytes, from: start, to: count))
            }
            return units.filter { !$0.isEmpty }
        }
    }

    /// Bytes `from..<to` with trailing zero bytes removed.
    private static func unit(_ bytes: UnsafeBufferPointer<UInt8>, from: Int, to: Int) -> Data {
        var end = to
        while end > from, bytes[end - 1] == 0 { end -= 1 }
        return Data(UnsafeBufferPointer(rebasing: bytes[from..<end]))
    }

    /// The units in AVCC form: each prefixed by its length as a 4-byte big-endian integer.
    public static func toAVCC(_ nals: [Data]) -> Data {
        var out = Data(capacity: nals.reduce(0) { $0 + 4 + $1.count })
        for nal in nals {
            var length = UInt32(nal.count).bigEndian
            withUnsafeBytes(of: &length) { out.append(contentsOf: $0) }
            out.append(nal)
        }
        return out
    }

    /// `nal_unit_type` of a unit, when it is one of `NALType`.
    public static func type(of nal: Data) -> NALType? {
        guard let first = nal.first else { return nil }
        return NALType(rawValue: first & 0x1F)
    }

    /// The access units of an Annex-B elementary stream, each starting at an access unit
    /// delimiter with its start code. A stream without delimiters is one access unit.
    public static func accessUnits(_ data: Data) -> [Data] {
        data.withUnsafeBytes { raw -> [Data] in
            let bytes = raw.bindMemory(to: UInt8.self)
            let count = bytes.count
            var boundaries: [Int] = []
            var i = 0
            while i + 3 < count {
                if bytes[i] == 0, bytes[i + 1] == 0, bytes[i + 2] == 1 {
                    if bytes[i + 3] & 0x1F == NALType.accessUnitDelimiter.rawValue {
                        boundaries.append(i > 0 && bytes[i - 1] == 0 ? i - 1 : i)
                    }
                    i += 3
                } else {
                    i += 1
                }
            }
            guard !boundaries.isEmpty else { return count == 0 ? [] : [Data(bytes)] }
            if boundaries[0] != 0 { boundaries.insert(0, at: 0) }
            boundaries.append(count)
            return zip(boundaries, boundaries.dropFirst()).map { Data(UnsafeBufferPointer(rebasing: bytes[$0..<$1])) }
        }
    }
}
