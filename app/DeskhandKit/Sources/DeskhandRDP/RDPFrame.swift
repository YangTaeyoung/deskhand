import CoreGraphics
import Foundation

public struct RDPFrame: Sendable {
    public let pixels: Data
    public let width: Int
    public let height: Int
    public let stride: Int
    public let sequence: UInt64

    static let maximumDimension = 8192
    static let maximumBytes = 256 * 1024 * 1024

    // The native bridge is the only producer. Keep malformed layouts representable
    // internally so the image boundary independently fails closed.
    init(pixels: Data, width: Int, height: Int, stride: Int, sequence: UInt64) {
        self.width = width
        self.height = height
        self.stride = stride
        self.sequence = sequence
        self.pixels = pixels
    }

    static func byteCount(width: Int, height: Int, stride: Int) -> Int? {
        guard (1...maximumDimension).contains(width),
              (1...maximumDimension).contains(height),
              stride >= width * 4, stride <= maximumBytes
        else { return nil }
        let (count, overflow) = stride.multipliedReportingOverflow(by: height)
        guard !overflow, count <= maximumBytes else { return nil }
        return count
    }

    public var image: CGImage? {
        guard let count = Self.byteCount(width: width, height: height, stride: stride),
              pixels.count == count,
              let provider = CGDataProvider(data: pixels as CFData)
        else { return nil }
        return CGImage(
            width: width, height: height, bitsPerComponent: 8, bitsPerPixel: 32,
            bytesPerRow: stride, space: CGColorSpaceCreateDeviceRGB(),
            bitmapInfo: CGBitmapInfo.byteOrder32Little.union(CGBitmapInfo(rawValue: CGImageAlphaInfo.noneSkipFirst.rawValue)),
            provider: provider, decode: nil, shouldInterpolate: false, intent: .defaultIntent
        )
    }
}
