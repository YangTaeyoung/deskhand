import CoreImage
import CoreMedia
import DeskhandMedia
import Foundation
import Synchronization
import VideoToolbox

/// The first frame of Tests/Fixtures/sample.h264, decoded with VideoToolbox; screen scenes draw
/// it in place of the video.
enum FixtureFrame {
    static let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .appendingPathComponent("../../Tests/Fixtures/sample.h264")
        .standardizedFileURL

    static let first: CGImage? = decodeFirst()

    private static func decodeFirst() -> CGImage? {
        guard let data = try? Data(contentsOf: url), let unit = AnnexB.accessUnits(data).first else { return nil }
        var stream = H264Stream()
        let frame = AUFrame(key: true, hasConfig: true, width: 1920, height: 1080, seq: 0, captureMonoUS: 0,
                            encodeDoneMonoUS: 0, data: unit)
        guard case .sample(let buffer, _)? = try? stream.feed(frame), let format = stream.format else { return nil }
        var session: VTDecompressionSession?
        let attributes: [CFString: Any] = [kCVPixelBufferPixelFormatTypeKey: kCVPixelFormatType_32BGRA]
        guard VTDecompressionSessionCreate(allocator: nil, formatDescription: format, decoderSpecification: nil,
                                           imageBufferAttributes: attributes as CFDictionary, outputCallback: nil,
                                           decompressionSessionOut: &session) == noErr, let session else { return nil }
        defer { VTDecompressionSessionInvalidate(session) }
        let decoded = Mutex<CGImage?>(nil)
        let context = CIContext()
        VTDecompressionSessionDecodeFrame(session, sampleBuffer: buffer, flags: [], infoFlagsOut: nil) { status, _, image, _, _ in
            guard status == noErr, let image else { return }
            let ci = CIImage(cvPixelBuffer: image)
            let cg = context.createCGImage(ci, from: ci.extent)
            decoded.withLock { $0 = cg }
        }
        VTDecompressionSessionWaitForAsynchronousFrames(session)
        return decoded.withLock { $0 }
    }
}
