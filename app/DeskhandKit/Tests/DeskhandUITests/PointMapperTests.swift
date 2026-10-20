import CoreGraphics
import XCTest
@testable import DeskhandUI

final class PointMapperTests: XCTestCase {
    func testPointMappingWithLetterbox() {
        let mapper = PointMapper(remote: CGSize(width: 3840, height: 2160), view: CGSize(width: 1000, height: 800), backingScale: 2)
        XCTAssertEqual(mapper.videoRect, CGRect(x: 0, y: 118.5, width: 1000, height: 562.5))
        XCTAssertEqual(mapper.remotePoint(from: CGPoint(x: 500, y: 400)), CGPoint(x: 1920, y: 1080))
        XCTAssertNil(mapper.remotePoint(from: CGPoint(x: 500, y: 50)))
        XCTAssertNil(mapper.remotePoint(from: CGPoint(x: 500, y: 790)))
        XCTAssertEqual(mapper.remotePoint(from: CGPoint(x: 0, y: 118.75)), CGPoint(x: 0, y: 0))
        XCTAssertEqual(mapper.remotePoint(from: CGPoint(x: 999.99, y: 680.99)), CGPoint(x: 3839, y: 2159))
    }

    func testPointMappingWithPillarbox() {
        let mapper = PointMapper(remote: CGSize(width: 1080, height: 1920), view: CGSize(width: 1000, height: 800), backingScale: 2)
        XCTAssertEqual(mapper.videoRect, CGRect(x: 275, y: 0, width: 450, height: 800))
        XCTAssertNil(mapper.remotePoint(from: CGPoint(x: 100, y: 400)))
        XCTAssertEqual(mapper.remotePoint(from: CGPoint(x: 500, y: 400)), CGPoint(x: 540, y: 960))
    }

    func testClampedPointStaysOnTheRemoteDisplay() {
        let mapper = PointMapper(remote: CGSize(width: 3840, height: 2160), view: CGSize(width: 1000, height: 800), backingScale: 2)
        XCTAssertEqual(mapper.clampedRemotePoint(from: CGPoint(x: -40, y: 20)), CGPoint(x: 0, y: 0))
        XCTAssertEqual(mapper.clampedRemotePoint(from: CGPoint(x: 1400, y: 900)), CGPoint(x: 3839, y: 2159))
    }

    func testRemotePointMapsBackToTheView() {
        let mapper = PointMapper(remote: CGSize(width: 3840, height: 2160), view: CGSize(width: 1000, height: 800), backingScale: 2)
        XCTAssertEqual(mapper.viewPoint(fromRemote: CGPoint(x: 1920, y: 1080)), CGPoint(x: 500, y: 399.75))
        XCTAssertEqual(mapper.viewPoint(fromRemote: CGPoint(x: 0, y: 0)), CGPoint(x: 0, y: 118.5))
    }

    func testVideoRectSitsOnDevicePixels() {
        for scale in [1.0, 2.0] {
            let mapper = PointMapper(remote: CGSize(width: 2560, height: 1440), view: CGSize(width: 1001, height: 777), backingScale: scale)
            let rect = mapper.videoRect
            for value in [rect.minX, rect.minY, rect.width, rect.height] {
                XCTAssertEqual((value * scale).rounded(), value * scale, "\(value) at scale \(scale)")
            }
            XCTAssertLessThanOrEqual(rect.maxX, 1001)
            XCTAssertLessThanOrEqual(rect.maxY, 777)
        }
    }

    func testCursorKeepsItsDevicePixelSize() {
        let mapper = PointMapper(remote: CGSize(width: 3840, height: 2160), view: CGSize(width: 1000, height: 800), backingScale: 2)
        XCTAssertEqual(mapper.cursorSize(pixels: CGSize(width: 48, height: 48)), CGSize(width: 24, height: 24))
        let standard = PointMapper(remote: CGSize(width: 1920, height: 1080), view: CGSize(width: 1000, height: 800), backingScale: 1)
        XCTAssertEqual(standard.cursorSize(pixels: CGSize(width: 32, height: 32)), CGSize(width: 32, height: 32))
    }

    func testEmptyRemoteMapsNothing() {
        let mapper = PointMapper(remote: .zero, view: CGSize(width: 1000, height: 800), backingScale: 2)
        XCTAssertEqual(mapper.videoRect, .zero)
        XCTAssertNil(mapper.remotePoint(from: CGPoint(x: 500, y: 400)))
    }
}
