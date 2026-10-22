import CoreGraphics
import Foundation
import Testing
@testable import DeskhandRDP

struct RDPFrameTests {
    @Test func imagePreservesBGRAColorsAndOwnsPixels() throws {
        let frame = RDPFrame(pixels: Data([11, 22, 33, 255, 44, 55, 66, 255]), width: 2, height: 1, stride: 8, sequence: 42)
        let image = try #require(frame.image)
        #expect(image.width == 2)
        #expect(image.height == 1)
        #expect(image.bytesPerRow == 8)
        #expect(image.bitsPerPixel == 32)
        #expect(image.bitmapInfo.contains(.byteOrder32Little))
        #expect(image.alphaInfo == .noneSkipFirst)
        let bytes = try #require(image.dataProvider?.data)
        #expect(bytes as Data == frame.pixels)
        #expect(frame.sequence == 42)
        var rgba = Data(count: 8)
        try rgba.withUnsafeMutableBytes { buffer in
            let context = try #require(CGContext(
                data: buffer.baseAddress, width: 2, height: 1, bitsPerComponent: 8,
                bytesPerRow: 8, space: CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGBitmapInfo.byteOrder32Big.rawValue | CGImageAlphaInfo.premultipliedLast.rawValue
            ))
            context.draw(image, in: CGRect(x: 0, y: 0, width: 2, height: 1))
        }
        #expect(rgba == Data([33, 22, 11, 255, 66, 55, 44, 255]))
    }

    @Test func ignoresUnusedAlphaWithoutRewritingFrameOrPadding() throws {
        let frame = RDPFrame(pixels: Data([11, 22, 33, 0, 99, 98, 97, 96]), width: 1, height: 1, stride: 8, sequence: 1)
        #expect(frame.pixels == Data([11, 22, 33, 0, 99, 98, 97, 96]))
        let image = try #require(frame.image)
        var pixel = Data(count: 4)
        try pixel.withUnsafeMutableBytes { bytes in
            let context = try #require(CGContext(data: bytes.baseAddress, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGBitmapInfo.byteOrder32Big.rawValue | CGImageAlphaInfo.premultipliedLast.rawValue))
            context.draw(image, in: CGRect(x: 0, y: 0, width: 1, height: 1))
        }
        #expect(pixel == Data([33, 22, 11, 255]))
        #expect(frame.image != nil)
    }

    @Test func rejectsUnsafeImageLayouts() {
        let layouts: [(Int, Int, Int, Int)] = [
            (0, 1, 4, 4), (-1, 1, 4, 4), (1, 0, 4, 4), (1, -1, 4, 4),
            (8193, 1, 4, 4), (1, 8193, 4, 4), (2, 1, 4, 4),
            (1, 1, 0, 4), (1, 1, -4, 4), (1, 2, 4, 7),
            (1, 2, .max, 4), (.max, 1, .max, 4), (1, .max, 4, 4),
            (1, 1, 268_435_457, 4), (1, 1, 4, 5),
        ]
        for (width, height, stride, count) in layouts {
            let frame = RDPFrame(pixels: Data(repeating: 255, count: count), width: width, height: height, stride: stride, sequence: 0)
            #expect(frame.image == nil)
        }
    }

    @Test func paddedRowsRemainValid() {
        let frame = RDPFrame(pixels: Data(repeating: 255, count: 24), width: 2, height: 2, stride: 12, sequence: 0)
        #expect(frame.image?.bytesPerRow == 12)
    }

    @Test func acceptsExactByteLimitWithoutAllocatingIt() {
        #expect(RDPFrame.byteCount(width: 8192, height: 8192, stride: 32768) == 268_435_456)
        #expect(RDPFrame.byteCount(width: 8192, height: 8192, stride: 32769) == nil)
        #expect(RDPFrame.byteCount(width: 1, height: 8192, stride: .max) == nil)
    }
}
