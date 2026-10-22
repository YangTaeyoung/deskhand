import Testing
@testable import DeskhandRDP

struct RDPPerformanceTests {
    @Test func separatesNativeReceiveAndSubmissionAndIgnoresDuplicates() throws {
        var meter = RDPPerformanceWindow(start: 10)
        meter.receive(sequence: 100, width: 800, height: 600, at: 10)
        meter.submit(sequence: 100, at: 10.01)
        meter.receive(sequence: 102, width: 800, height: 600, at: 10.04)
        meter.submit(sequence: 102, at: 10.05)
        meter.submit(sequence: 102, at: 10.06)
        let sampleValue = meter.sample(at: 11)
        let sample = try #require(sampleValue)
        #expect(sample.nativeUpdates == 3)
        #expect(sample.receivedFrames == 2 && sample.submittedFrames == 2)
        #expect(sample.receivedFPS == 2 && sample.submittedFPS == 2)
        #expect(abs(sample.submitP95MS - 40) < 0.001)
    }
    @Test func idleAndStallsAreCounted() throws {
        var meter = RDPPerformanceWindow(start: 0)
        meter.receive(sequence: 1, width: 800, height: 600, at: 0.1)
        meter.submit(sequence: 1, at: 0.1)
        meter.receive(sequence: 2, width: 800, height: 600, at: 0.4)
        meter.submit(sequence: 2, at: 0.4)
        let firstValue = meter.sample(at: 1)
        let first = try #require(firstValue)
        #expect(first.submitGapsOver100MS == 1)
        let idleValue = meter.sample(at: 2)
        let idle = try #require(idleValue)
        #expect(idle.receivedFPS == 0 && idle.submittedFPS == 0)
        #expect(idle.width == 800 && idle.height == 600)
    }
    @Test func resizeStartsNewWindowAndNeverMixesResolutions() throws {
        var meter = RDPPerformanceWindow(start: 0)
        meter.receive(sequence: 1, width: 800, height: 600, at: 0.1)
        meter.submit(sequence: 1, at: 0.1)
        meter.receive(sequence: 5, width: 1600, height: 900, at: 0.8)
        meter.submit(sequence: 5, at: 0.81)
        let early = meter.sample(at: 1)
        #expect(early == nil)
        let sampleValue = meter.sample(at: 1.8)
        let sample = try #require(sampleValue)
        #expect(sample.width == 1600 && sample.height == 900)
        #expect(sample.receivedFrames == 1 && sample.submittedFrames == 1)
        #expect(sample.submitP95MS == 0)
    }
}
