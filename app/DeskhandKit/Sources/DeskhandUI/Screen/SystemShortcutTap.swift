import AppKit
import Foundation

extension KeyMap {
    /// F1–F20 key codes.
    static let functionKeyCodes: Set<UInt16> = [
        0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F, 0x69, 0x6B, 0x71, 0x6A, 0x40, 0x4F, 0x50, 0x5A,
    ]
}

/// A session event tap that hands ⌘Tab key events to `handler` and swallows the ones it
/// consumes, while started. It starts only when the app already has the Accessibility
/// permission (`isPermitted`) and never asks for it.
@MainActor
final class SystemShortcutTap {
    private let handler: (NSEvent) -> Bool
    private var port: CFMachPort?
    private var source: CFRunLoopSource?

    init(handler: @escaping (NSEvent) -> Bool) {
        self.handler = handler
    }

    static var isPermitted: Bool { AXIsProcessTrusted() }

    var isRunning: Bool { port != nil }

    func start() {
        guard port == nil, Self.isPermitted else { return }
        let mask = CGEventMask(1 << CGEventType.keyDown.rawValue) | CGEventMask(1 << CGEventType.keyUp.rawValue)
        let context = Unmanaged.passUnretained(self).toOpaque()
        guard let port = CGEvent.tapCreate(tap: .cgSessionEventTap, place: .headInsertEventTap, options: .defaultTap,
                                           eventsOfInterest: mask, callback: { _, type, event, context in
            guard let context else { return Unmanaged.passUnretained(event) }
            let tap = Unmanaged<SystemShortcutTap>.fromOpaque(context).takeUnretainedValue()
            let consumed = MainActor.assumeIsolated { tap.handle(type, event) }
            return consumed ? nil : Unmanaged.passUnretained(event)
        }, userInfo: context) else { return }
        let source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, port, 0)
        CFRunLoopAddSource(CFRunLoopGetMain(), source, .commonModes)
        CGEvent.tapEnable(tap: port, enable: true)
        self.port = port
        self.source = source
    }

    func stop() {
        guard let port else { return }
        CGEvent.tapEnable(tap: port, enable: false)
        if let source { CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .commonModes) }
        CFMachPortInvalidate(port)
        self.port = nil
        source = nil
    }

    /// Re-enables the tap after the system disabled it, and passes ⌘Tab to the handler.
    private func handle(_ type: CGEventType, _ event: CGEvent) -> Bool {
        switch type {
        case .tapDisabledByTimeout, .tapDisabledByUserInput:
            if let port { CGEvent.tapEnable(tap: port, enable: true) }
            return false
        case .keyDown, .keyUp:
            guard event.getIntegerValueField(.keyboardEventKeycode) == 0x30, event.flags.contains(.maskCommand),
                  let nsEvent = NSEvent(cgEvent: event) else { return false }
            return handler(nsEvent)
        default:
            return false
        }
    }
}
