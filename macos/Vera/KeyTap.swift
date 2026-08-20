import AppKit
import ApplicationServices
import Carbon.HIToolbox

// The Fn key, and the one combination that opens Vera.
//
// Fn has no key code a Carbon hot key can register, so it is watched
// through a CGEvent tap — the same mechanism every dictation tool on the
// Mac uses. A tap needs the Accessibility permission, which this app
// also needs to put words at the cursor, so the two arrive together.
//
// The tap sees every key the person presses. It keeps nothing, logs
// nothing, and swallows exactly one combination (Fn + the open key).
// Everything else passes through untouched, including Fn+arrows, which
// is why "Fn held" is tracked from the Fn key's own flag change rather
// than inferred from the flags on other keys — arrow keys carry the
// Fn flag whether or not Fn is down.

private struct EventBox: @unchecked Sendable {
    let event: CGEvent
}

@MainActor
final class KeyTap {
    enum Event {
        case fnDown
        case fnUp
        /// A key pressed while Fn was held. Return true to swallow it.
        case fnCombo(keyCode: Int64)
    }

    var onEvent: (Event) -> Bool = { _ in false }
    private(set) var running = false
    private(set) var problem: String?

    private var port: CFMachPort?
    private var source: CFRunLoopSource?
    private var fnHeld = false

    static var trusted: Bool { AXIsProcessTrusted() }

    /// Asks macOS to show the Accessibility prompt. The answer arrives
    /// by the person flipping a switch in System Settings, so the
    /// caller polls `trusted` rather than waiting on this.
    static func requestTrust() {
        // The constant is "kAXTrustedCheckOptionPrompt"; spelled out to
        // avoid touching the C global from strict-concurrency Swift.
        let options = ["AXTrustedCheckOptionPrompt": true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
    }

    func start() {
        guard !running else { return }
        problem = nil
        guard Self.trusted else {
            problem = "Vera needs Accessibility to watch the Fn key and type at the cursor."
            return
        }
        let mask: CGEventMask = (1 << CGEventType.flagsChanged.rawValue) | (1 << CGEventType.keyDown.rawValue)
        let me = Unmanaged.passUnretained(self).toOpaque()
        guard let port = CGEvent.tapCreate(
            tap: .cgSessionEventTap,
            place: .headInsertEventTap,
            options: .defaultTap,
            eventsOfInterest: mask,
            callback: { proxy, type, event, userInfo in
                guard let userInfo else { return Unmanaged.passUnretained(event) }
                let tap = Unmanaged<KeyTap>.fromOpaque(userInfo).takeUnretainedValue()
                // The run loop source lives on the main run loop, so
                // this is the main thread; say so for the compiler.
                // CGEvent is not Sendable, and nothing here sends it: the
                // box lets the main-actor method read it in place.
                let box = EventBox(event: event)
                let swallow = MainActor.assumeIsolated { tap.handle(type: type, event: box.event) }
                return swallow ? nil : Unmanaged.passUnretained(event)
            },
            userInfo: me
        ) else {
            problem = "macOS refused the key tap — Accessibility may need to be re-granted after a rebuild."
            return
        }
        self.port = port
        let source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, port, 0)
        self.source = source
        CFRunLoopAddSource(CFRunLoopGetMain(), source, .commonModes)
        CGEvent.tapEnable(tap: port, enable: true)
        running = true
    }

    func stop() {
        if let source { CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .commonModes) }
        if let port { CGEvent.tapEnable(tap: port, enable: false) }
        source = nil
        port = nil
        running = false
        fnHeld = false
    }

    /// Returns true to swallow the event.
    private func handle(type: CGEventType, event: CGEvent) -> Bool {
        switch type {
        case .tapDisabledByTimeout, .tapDisabledByUserInput:
            // macOS switches a slow tap off; switch it back on.
            if let port { CGEvent.tapEnable(tap: port, enable: true) }
            return false
        case .flagsChanged:
            let keyCode = event.getIntegerValueField(.keyboardEventKeycode)
            guard keyCode == kVK_Function else { return false }
            let held = event.flags.contains(.maskSecondaryFn)
            guard held != fnHeld else { return false }
            fnHeld = held
            _ = onEvent(held ? .fnDown : .fnUp)
            return false
        case .keyDown:
            guard fnHeld else { return false }
            let keyCode = event.getIntegerValueField(.keyboardEventKeycode)
            return onEvent(.fnCombo(keyCode: keyCode))
        default:
            return false
        }
    }
}
