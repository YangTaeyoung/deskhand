import CoreMedia
import Foundation
import XCTest
@testable import DeskhandMedia

final class AnnexBTests: XCTestCase {
    func testSplitMixedStartCodes() {
        let stream = Data([
            0x00, 0x00, 0x00, 0x01, 0x09, 0x10,
            0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x2A,
            0x00, 0x00, 0x00, 0x01, 0x68, 0xEE, 0x3C, 0x80,
            0x00, 0x00, 0x01, 0x65, 0xB8, 0x04, 0x00, 0x03, 0x01,
        ])
        let nals = AnnexB.split(stream)
        XCTAssertEqual(nals, [
            Data([0x09, 0x10]),
            Data([0x67, 0x64, 0x00, 0x2A]),
            Data([0x68, 0xEE, 0x3C, 0x80]),
            Data([0x65, 0xB8, 0x04, 0x00, 0x03, 0x01]),
        ])
        XCTAssertEqual(nals.map(AnnexB.type(of:)), [.accessUnitDelimiter, .sps, .pps, .idr])
    }

    func testSplitAcceptsSlicedDataAndIgnoresLeadingGarbage() {
        let backing = Data([0xFF, 0xFF, 0x12, 0x00, 0x00, 0x01, 0x41, 0x9A, 0x00, 0x00, 0x00, 0x01, 0x41, 0x9B])
        let slice = backing[2...]
        XCTAssertEqual(AnnexB.split(slice), [Data([0x41, 0x9A]), Data([0x41, 0x9B])])
        XCTAssertEqual(AnnexB.split(Data()), [])
        XCTAssertEqual(AnnexB.split(Data([0x00, 0x00, 0x01])), [])
    }

    func testToAVCCLengthPrefix() {
        let avcc = AnnexB.toAVCC([Data([0x65, 0x88, 0x84]), Data([0x41, 0x9A])])
        XCTAssertEqual(avcc, Data([
            0x00, 0x00, 0x00, 0x03, 0x65, 0x88, 0x84,
            0x00, 0x00, 0x00, 0x02, 0x41, 0x9A,
        ]))
    }

    func testAccessUnitsSplitAtDelimiters() throws {
        let fixture = try Fixture.data()
        let units = AnnexB.accessUnits(fixture)
        XCTAssertEqual(units.count, Fixture.accessUnitCount)
        XCTAssertEqual(units.reduce(0) { $0 + $1.count }, fixture.count)
        let firstTypes = AnnexB.split(units[0]).compactMap(AnnexB.type(of:))
        XCTAssertEqual(firstTypes, [.accessUnitDelimiter, .sps, .pps, .idr])
        XCTAssertEqual(AnnexB.split(units[1]).compactMap(AnnexB.type(of:)), [.accessUnitDelimiter, .slice])
    }

    func testAUFrameRoundTripsProtocolLayout() throws {
        var payload = Data([0b11])
        payload.append(contentsOf: [0x00, 0x00, 0x07, 0x80])
        payload.append(contentsOf: [0x00, 0x00, 0x04, 0x38])
        payload.append(contentsOf: [0, 0, 0, 0, 0, 0, 0x01, 0x02])
        payload.append(contentsOf: [0, 0, 0, 0x01, 0, 0, 0, 0x10])
        payload.append(contentsOf: [0, 0, 0, 0x01, 0, 0, 0, 0x20])
        payload.append(contentsOf: [0x00, 0x00, 0x00, 0x01, 0x09, 0x10])
        let frame = try AUFrame(payload: payload)
        XCTAssertTrue(frame.key)
        XCTAssertTrue(frame.hasConfig)
        XCTAssertEqual(frame.width, 1920)
        XCTAssertEqual(frame.height, 1080)
        XCTAssertEqual(frame.seq, 0x0102)
        XCTAssertEqual(frame.captureMonoUS, 0x1_0000_0010)
        XCTAssertEqual(frame.encodeDoneMonoUS, 0x1_0000_0020)
        XCTAssertEqual(frame.data, Data([0x00, 0x00, 0x00, 0x01, 0x09, 0x10]))
        XCTAssertEqual(frame.payload, payload)
        XCTAssertThrowsError(try AUFrame(payload: payload.prefix(AUFrame.headerLength - 1)))
    }

    func testSampleBufferDisplaysImmediatelyAndMarksNonKeyFrames() throws {
        let units = AnnexB.accessUnits(try Fixture.data())
        var stream = H264Stream()
        guard case .sample(let key, let formatChanged) = try stream.feed(Fixture.frame(units[0], seq: 0, key: true)) else {
            return XCTFail("first access unit did not produce a sample")
        }
        XCTAssertTrue(formatChanged)
        let format = try XCTUnwrap(CMSampleBufferGetFormatDescription(key))
        XCTAssertEqual(CMVideoFormatDescriptionGetDimensions(format).width, 1920)
        XCTAssertEqual(CMVideoFormatDescriptionGetDimensions(format).height, 1080)
        XCTAssertEqual(Fixture.attachment(key, kCMSampleAttachmentKey_DisplayImmediately), true)
        XCTAssertNotEqual(Fixture.attachment(key, kCMSampleAttachmentKey_NotSync), true)

        guard case .sample(let delta, let changed) = try stream.feed(Fixture.frame(units[1], seq: 1, key: false)) else {
            return XCTFail("second access unit did not produce a sample")
        }
        XCTAssertFalse(changed)
        XCTAssertEqual(Fixture.attachment(delta, kCMSampleAttachmentKey_DisplayImmediately), true)
        XCTAssertEqual(Fixture.attachment(delta, kCMSampleAttachmentKey_NotSync), true)
    }

    func testStreamWaitsForKeyframeAfterReset() throws {
        let units = AnnexB.accessUnits(try Fixture.data())
        var stream = H264Stream()
        XCTAssertEqual(try stream.feed(Fixture.frame(units[1], seq: 1, key: false)), .needsKeyframe)
        guard case .sample = try stream.feed(Fixture.frame(units[0], seq: 2, key: true)) else {
            return XCTFail("keyframe did not produce a sample")
        }
        stream.reset()
        XCTAssertEqual(try stream.feed(Fixture.frame(units[2], seq: 3, key: false)), .needsKeyframe)
        guard case .sample(_, let changed) = try stream.feed(Fixture.frame(units[0], seq: 4, key: true)) else {
            return XCTFail("keyframe after reset did not produce a sample")
        }
        XCTAssertFalse(changed)
    }

    func testCursorFramesParse() throws {
        var shape = Data([0, 0, 0, 7, 0, 2, 0, 1, 0, 1, 0, 0])
        shape.append(contentsOf: [0xFF, 0x00, 0x00, 0x80, 0x00, 0x00, 0xFF, 0xFF])
        let cursor = try CursorShape(payload: shape)
        XCTAssertEqual(cursor.id, 7)
        XCTAssertEqual(cursor.width, 2)
        XCTAssertEqual(cursor.height, 1)
        XCTAssertEqual(cursor.hotX, 1)
        XCTAssertEqual(cursor.hotY, 0)
        XCTAssertNotNil(cursor.makeImage())
        XCTAssertThrowsError(try CursorShape(payload: shape.dropLast()))

        let position = try CursorPosition(payload: Data([0xFF, 0xFF, 0xFF, 0xF6, 0, 0, 0x04, 0x38, 1, 0, 0, 0, 7]))
        XCTAssertEqual(position, CursorPosition(x: -10, y: 1080, visible: true, shapeID: 7))
    }
}

/// The trimmed NVIDIA H.264 capture in Tests/Fixtures.
enum Fixture {
    static let accessUnitCount = 71

    static var url: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .appendingPathComponent("../Fixtures/sample.h264")
            .standardizedFileURL
    }

    static func data() throws -> Data {
        try Data(contentsOf: url)
    }

    static func frame(_ unit: Data, seq: UInt64, key: Bool) -> AUFrame {
        AUFrame(key: key, hasConfig: key, width: 1920, height: 1080, seq: seq, captureMonoUS: seq * 16_667,
                encodeDoneMonoUS: seq * 16_667 + 3_000, data: unit)
    }

    static func attachment(_ buffer: CMSampleBuffer, _ key: CFString) -> Bool? {
        guard let array = CMSampleBufferGetSampleAttachmentsArray(buffer, createIfNecessary: false) as? [[CFString: Any]],
              let first = array.first else { return nil }
        return first[key] as? Bool
    }
}
