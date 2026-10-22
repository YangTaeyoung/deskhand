import Darwin
import Foundation

// Immutable function table. The library stays loaded while any worker retains it.
// dlopen's raw handle is never exposed or mutated after initialization.
final class RDPEngine: @unchecked Sendable {
    typealias New = @convention(c) (UnsafePointer<CChar>, UInt16, UnsafePointer<CChar>, UnsafePointer<CChar>, UnsafePointer<CChar>, UnsafePointer<CChar>, UInt32, UInt32) -> UnsafeMutableRawPointer?
    typealias Resize = @convention(c) (UnsafeMutableRawPointer, UInt32, UInt32, UInt32) -> Int32
    typealias Run = @convention(c) (UnsafeMutableRawPointer) -> Int32
    typealias Action = @convention(c) (UnsafeMutableRawPointer) -> Void
    typealias State = @convention(c) (UnsafeMutableRawPointer) -> Int32
    typealias Number = @convention(c) (UnsafeMutableRawPointer) -> UInt32
    typealias CopyFrame = @convention(c) (UnsafeMutableRawPointer, UnsafeMutablePointer<UInt8>?, Int, UnsafeMutablePointer<UInt32>, UnsafeMutablePointer<UInt32>, UnsafeMutablePointer<UInt32>, UnsafeMutablePointer<UInt64>) -> Int32

    private let library: UnsafeMutableRawPointer
    let new: New
    let resize: Resize
    let run: Run
    let stop: Action
    let free: Action
    let state: State
    let error: Number
    let sessionID: Number
    let copyFrame: CopyFrame

    init(url: URL?) throws {
        guard let url = url ?? Bundle.main.privateFrameworksURL?.appendingPathComponent("libDeskhandRDPEngine.dylib"),
              url.isFileURL, !url.path.utf8.contains(0),
              let library = dlopen(url.path, RTLD_NOW | RTLD_LOCAL)
        else { throw RDPFailure.engineUnavailable }
        do {
            func symbol<T>(_ name: String, as type: T.Type) throws -> T {
                guard let address = dlsym(library, name) else { throw RDPFailure.engineUnavailable }
                return unsafeBitCast(address, to: type)
            }
            new = try symbol("dh_rdp_new", as: New.self)
            resize = try symbol("dh_rdp_resize", as: Resize.self)
            run = try symbol("dh_rdp_run", as: Run.self)
            stop = try symbol("dh_rdp_stop", as: Action.self)
            free = try symbol("dh_rdp_free", as: Action.self)
            state = try symbol("dh_rdp_state", as: State.self)
            error = try symbol("dh_rdp_error", as: Number.self)
            sessionID = try symbol("dh_rdp_session_id", as: Number.self)
            copyFrame = try symbol("dh_rdp_copy_frame", as: CopyFrame.self)
            self.library = library
        } catch {
            dlclose(library)
            throw RDPFailure.engineUnavailable
        }
    }

    deinit { dlclose(library) }
}
