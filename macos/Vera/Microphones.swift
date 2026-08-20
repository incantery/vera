import AVFoundation
import CoreAudio
import Foundation

// Which microphone.
//
// The system default input is whatever was plugged in or installed
// last, and on a developer's Mac that is often a virtual device —
// BlackHole, Loopback, a meeting app's driver — carrying perfect
// silence. So the app chooses: the person's pick from Settings, else
// the first physical microphone, and only then the default.

struct Microphone: Identifiable, Hashable, Sendable {
    let id: AudioDeviceID
    let uid: String
    let name: String
    let virtualDevice: Bool
    let builtIn: Bool
}

enum Microphones {
    /// Every device with input channels, built-in first.
    static func all() -> [Microphone] {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioHardwarePropertyDevices,
            mScope: kAudioObjectPropertyScopeGlobal,
            mElement: kAudioObjectPropertyElementMain
        )
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size) == noErr else { return [] }
        var ids = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &ids) == noErr else { return [] }

        return ids.compactMap { id -> Microphone? in
            guard inputChannels(id) > 0, let name = string(id, kAudioObjectPropertyName), let uid = string(id, kAudioDevicePropertyDeviceUID) else { return nil }
            let kind = transport(id)
            // CoreAudio's private aggregate around the default device
            // is not a microphone anyone would choose; it is the default
            // wearing a different name.
            if kind == kAudioDeviceTransportTypeAggregate || name.hasPrefix("CADefaultDeviceAggregate") { return nil }
            return Microphone(
                id: id, uid: uid, name: name,
                virtualDevice: kind == kAudioDeviceTransportTypeVirtual,
                builtIn: kind == kAudioDeviceTransportTypeBuiltIn
            )
        }
        .sorted { a, b in
            if a.builtIn != b.builtIn { return a.builtIn }
            if a.virtualDevice != b.virtualDevice { return !a.virtualDevice }
            return a.name < b.name
        }
    }

    static func systemDefault() -> AudioDeviceID? {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioHardwarePropertyDefaultInputDevice,
            mScope: kAudioObjectPropertyScopeGlobal,
            mElement: kAudioObjectPropertyElementMain
        )
        var id = AudioDeviceID(0)
        var size = UInt32(MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &id) == noErr, id != 0 else { return nil }
        return id
    }

    /// The one to use: the chosen uid if it is still present, else the
    /// first physical microphone, else whatever the system says.
    static func choose(preferring uid: String?) -> Microphone? {
        let mics = all()
        if let uid, let pick = mics.first(where: { $0.uid == uid }) { return pick }
        if let physical = mics.first(where: { !$0.virtualDevice }) { return physical }
        if let def = systemDefault() { return mics.first { $0.id == def } }
        return mics.first
    }

    /// Points an engine's input node at a device. Must happen before the
    /// engine starts.
    static func use(_ mic: Microphone, on engine: AVAudioEngine) throws {
        guard let unit = engine.inputNode.audioUnit else { throw MicError.noInputUnit }
        var id = mic.id
        let status = AudioUnitSetProperty(unit, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0, &id, UInt32(MemoryLayout<AudioDeviceID>.size))
        guard status == noErr else { throw MicError.cannotSelect(mic.name, status) }
    }

    enum MicError: LocalizedError {
        case noInputUnit
        case cannotSelect(String, OSStatus)
        var errorDescription: String? {
            switch self {
            case .noInputUnit: "The audio engine has no input."
            case .cannotSelect(let name, let status): "Couldn't use \(name) as the microphone (\(status))."
            }
        }
    }

    // MARK: - CoreAudio plumbing

    private static func inputChannels(_ id: AudioDeviceID) -> Int {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioDevicePropertyStreamConfiguration,
            mScope: kAudioDevicePropertyScopeInput,
            mElement: kAudioObjectPropertyElementMain
        )
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(id, &address, 0, nil, &size) == noErr, size > 0 else { return 0 }
        let list = UnsafeMutablePointer<AudioBufferList>.allocate(capacity: Int(size))
        defer { list.deallocate() }
        guard AudioObjectGetPropertyData(id, &address, 0, nil, &size, list) == noErr else { return 0 }
        return UnsafeMutableAudioBufferListPointer(list).reduce(0) { $0 + Int($1.mNumberChannels) }
    }

    private static func transport(_ id: AudioDeviceID) -> UInt32 {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioDevicePropertyTransportType,
            mScope: kAudioObjectPropertyScopeGlobal,
            mElement: kAudioObjectPropertyElementMain
        )
        var value: UInt32 = 0
        var size = UInt32(MemoryLayout<UInt32>.size)
        guard AudioObjectGetPropertyData(id, &address, 0, nil, &size, &value) == noErr else { return 0 }
        return value
    }

    private static func string(_ id: AudioDeviceID, _ selector: AudioObjectPropertySelector) -> String? {
        var address = AudioObjectPropertyAddress(mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
        var value: Unmanaged<CFString>?
        var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
        guard AudioObjectGetPropertyData(id, &address, 0, nil, &size, &value) == noErr, let value else { return nil }
        return value.takeRetainedValue() as String
    }
}
