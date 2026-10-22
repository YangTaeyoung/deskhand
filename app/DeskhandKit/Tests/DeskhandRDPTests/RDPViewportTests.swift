import Foundation
import Testing
@testable import DeskhandRDP

struct RDPViewportTests {
    @Test func retinaDefaultsToWindowPointsWithoutSupersampling() throws {
        let layout = try #require(RDPViewport(width: 1200, height: 800, backingScale: 2))
        #expect(layout.width == 1200 && layout.height == 800)
        #expect(layout.desktopScale == 100)
    }
    @Test func externalDisplayAndEvenWidth() throws {
        let layout = try #require(RDPViewport(width: 1201, height: 801, backingScale: 1))
        #expect(layout.width == 1200 && layout.height == 801)
        #expect(layout.desktopScale == 100)
    }
    @Test func movingBetweenDisplaysDoesNotMultiplyResolution() throws {
        let normal = try #require(RDPViewport(width: 3268, height: 1634, backingScale: 1))
        for scale: Double in [1.5, 2, 3] {
            let retina = try #require(RDPViewport(width: 3268, height: 1634, backingScale: scale))
            #expect(retina == normal)
        }
        #expect(normal.width == 3268 && normal.height == 1634)
        #expect(normal.desktopScale == 100)
    }
    @Test func invalidGeometryIsIgnored() {
        for value: Double in [0, -1, .nan, .infinity] {
            #expect(RDPViewport(width: value, height: 800, backingScale: 2) == nil)
            #expect(RDPViewport(width: 1200, height: value, backingScale: 2) == nil)
            #expect(RDPViewport(width: 1200, height: 800, backingScale: value) == nil)
        }
    }
    @Test func hugeWindowPreservesAspectWithinBudget() throws {
        let layout = try #require(RDPViewport(width: 10000, height: 5000, backingScale: 2))
        #expect(layout.width <= 3840 && layout.height <= 3840)
        #expect(UInt64(layout.width) * UInt64(layout.height) <= 8_294_400)
        #expect(abs(Double(layout.width) / Double(layout.height) - 2) < 0.01)
    }
}
