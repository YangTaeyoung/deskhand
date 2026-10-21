import CoreMedia
import CoreVideo
import Foundation
import Synchronization
import VideoToolbox
import XCTest
@testable import DeskhandMedia

final class DecodeTests: XCTestCase {
    /// Decoded frames reported by the decompression session's output handler.
    private final class Decoded: Sendable {
        struct Frame: Sendable {
            let status: OSStatus
            let width: Int
            let height: Int
        }

        let frames = Mutex<[Frame]>([])
    }

    func testDecodeFixtureProducesFrames() throws {
        let units = AnnexB.accessUnits(try Fixture.data())
        XCTAssertEqual(units.count, Fixture.accessUnitCount)

        var stream = H264Stream()
        var session: VTDecompressionSession?
        let decoded = Decoded()
        defer { if let session { VTDecompressionSessionInvalidate(session) } }

        for (index, unit) in units.enumerated() {
            let key = AnnexB.split(unit).contains { AnnexB.type(of: $0) == .idr }
            let frame = Fixture.frame(unit, seq: UInt64(index), key: key)
            guard case .sample(let buffer, _) = try stream.feed(frame) else {
                return XCTFail("access unit \(index) did not produce a sample")
            }
            if session == nil {
                let format = try XCTUnwrap(stream.format)
                let attributes: [CFString: Any] = [kCVPixelBufferPixelFormatTypeKey: kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange]
                XCTAssertEqual(VTDecompressionSessionCreate(allocator: nil, formatDescription: format, decoderSpecification: nil,
                                                            imageBufferAttributes: attributes as CFDictionary, outputCallback: nil,
                                                            decompressionSessionOut: &session), noErr)
            }
            let status = VTDecompressionSessionDecodeFrame(try XCTUnwrap(session), sampleBuffer: buffer, flags: [], infoFlagsOut: nil) {
                status, _, image, _, _ in
                decoded.frames.withLock {
                    $0.append(.init(status: status,
                                    width: image.map(CVPixelBufferGetWidth) ?? 0,
                                    height: image.map(CVPixelBufferGetHeight) ?? 0))
                }
            }
            XCTAssertEqual(status, noErr, "access unit \(index)")
        }
        XCTAssertEqual(VTDecompressionSessionWaitForAsynchronousFrames(try XCTUnwrap(session)), noErr)

        let frames = decoded.frames.withLock { $0 }
        XCTAssertEqual(frames.count, units.count)
        XCTAssertTrue(frames.allSatisfy { $0.status == noErr }, "\(frames.filter { $0.status != noErr }.map(\.status))")
        XCTAssertTrue(frames.allSatisfy { $0.width == 1920 && $0.height == 1080 })
    }

    func testFormatDescriptionFromFixtureParameterSets() throws {
        let nals = AnnexB.split(AnnexB.accessUnits(try Fixture.data())[0])
        let sps = try XCTUnwrap(nals.first { AnnexB.type(of: $0) == .sps })
        let pps = try XCTUnwrap(nals.first { AnnexB.type(of: $0) == .pps })
        let format = try H264Format.make(sps: sps, pps: pps)
        let dimensions = CMVideoFormatDescriptionGetDimensions(format)
        XCTAssertEqual(dimensions.width, 1920)
        XCTAssertEqual(dimensions.height, 1080)
        XCTAssertEqual(CMFormatDescriptionGetMediaSubType(format), kCMVideoCodecType_H264)
        XCTAssertThrowsError(try H264Format.make(sps: Data([0x67]), pps: pps))
    }
}
