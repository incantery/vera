import AppKit
import Carbon.HIToolbox

// The global hotkey.
//
// Carbon's RegisterEventHotKey is the one way to get a system-wide key
// that (a) needs no Accessibility permission and (b) reports the RELEASE
// as well as the press, which is what makes hold-to-talk possible. It is
// old, it is still fully supported, and every launcher on the Mac uses
// it. NSEvent's global monitor would need Input Monitoring permission
// and a conversation with the person about why.

struct KeyBinding: Codable, Equatable, Sendable {
    var keyCode: UInt32
    var control: Bool
    var option: Bool
    var shift: Bool
    var command: Bool

    /// ⌃⌥Space. ⌘Space is Spotlight, ⌥Space is Alfred and Raycast; this
    /// one is nobody's.
    static let standard = KeyBinding(keyCode: UInt32(kVK_Space), control: true, option: true, shift: false, command: false)

    var carbonModifiers: UInt32 {
        var m: UInt32 = 0
        if control { m |= UInt32(controlKey) }
        if option { m |= UInt32(optionKey) }
        if shift { m |= UInt32(shiftKey) }
        if command { m |= UInt32(cmdKey) }
        return m
    }

    var hasModifier: Bool { control || option || shift || command }

    var display: String {
        var s = ""
        if control { s += "⌃" }
        if option { s += "⌥" }
        if shift { s += "⇧" }
        if command { s += "⌘" }
        return s + (KeyBinding.keys.first { $0.code == keyCode }?.name ?? "key \(keyCode)")
    }

    /// The keys the settings screen offers. Virtual key codes are ANSI
    /// layout positions; good enough for a first binding.
    struct Key: Identifiable, Hashable, Sendable {
        let name: String
        let code: UInt32
        var id: UInt32 { code }
    }

    static let keys: [Key] = [
        Key(name: "Space", code: UInt32(kVK_Space)),
        Key(name: "`", code: UInt32(kVK_ANSI_Grave)),
        Key(name: "F1", code: UInt32(kVK_F1)), Key(name: "F2", code: UInt32(kVK_F2)),
        Key(name: "F3", code: UInt32(kVK_F3)), Key(name: "F4", code: UInt32(kVK_F4)),
        Key(name: "F5", code: UInt32(kVK_F5)), Key(name: "F6", code: UInt32(kVK_F6)),
        Key(name: "F7", code: UInt32(kVK_F7)), Key(name: "F8", code: UInt32(kVK_F8)),
        Key(name: "F9", code: UInt32(kVK_F9)), Key(name: "F10", code: UInt32(kVK_F10)),
        Key(name: "F11", code: UInt32(kVK_F11)), Key(name: "F12", code: UInt32(kVK_F12)),
        Key(name: "V", code: UInt32(kVK_ANSI_V)), Key(name: "J", code: UInt32(kVK_ANSI_J)),
        Key(name: "K", code: UInt32(kVK_ANSI_K)), Key(name: "L", code: UInt32(kVK_ANSI_L)),
        Key(name: "Z", code: UInt32(kVK_ANSI_Z)), Key(name: "/", code: UInt32(kVK_ANSI_Slash)),
    ]
}

@MainActor
final class Hotkey {
    enum Event { case pressed, released }

    var onEvent: (Event) -> Void = { _ in }
    private(set) var registered: KeyBinding?
    private(set) var problem: String?

    private var hotKeyRef: EventHotKeyRef?
    private var handlerRef: EventHandlerRef?
    private static let signature: OSType = 0x5645_5241 // "VERA"

    /// Registers the binding, replacing whatever was registered before.
    /// Failure is a fact to show, not to throw past: a binding another
    /// app already owns fails here with no other symptom.
    func register(_ binding: KeyBinding) {
        unregister()
        problem = nil

        if handlerRef == nil {
            var specs = [
                EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed)),
                EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyReleased)),
            ]
            let me = Unmanaged.passUnretained(self).toOpaque()
            let status = InstallEventHandler(GetApplicationEventTarget(), { _, event, userData in
                guard let event, let userData else { return noErr }
                let kind = GetEventKind(event)
                let hotkey = Unmanaged<Hotkey>.fromOpaque(userData).takeUnretainedValue()
                // Carbon delivers on the main run loop; the assertion
                // makes the isolation explicit rather than assumed.
                MainActor.assumeIsolated {
                    hotkey.onEvent(kind == UInt32(kEventHotKeyPressed) ? .pressed : .released)
                }
                return noErr
            }, specs.count, &specs, me, &handlerRef)
            if status != noErr {
                problem = "Could not install the hotkey handler (\(status))."
                return
            }
        }

        var ref: EventHotKeyRef?
        let id = EventHotKeyID(signature: Self.signature, id: 1)
        let status = RegisterEventHotKey(binding.keyCode, binding.carbonModifiers, id, GetApplicationEventTarget(), 0, &ref)
        if status != noErr || ref == nil {
            problem = "\(binding.display) could not be registered — another app probably owns it."
            return
        }
        hotKeyRef = ref
        registered = binding
    }

    func unregister() {
        if let hotKeyRef {
            UnregisterEventHotKey(hotKeyRef)
            self.hotKeyRef = nil
        }
        registered = nil
    }

    // The handler lives as long as the process: this object is owned by
    // the station, which is owned by the app. Nothing to tear down.
}
