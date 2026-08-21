import AVFoundation
import Foundation
import Observation

// Recording, and nothing else.
//
// The phone's one job in dictation is to capture audio and hand it over
// — the Mac does the recognising. AVAudioRecorder is the right tool for
// exactly that: it writes to a file until told to stop, with no session
// cap, no recognition, no thing to go wrong between chunks. This is the
// reliability the on-device recogniser could not give.

@Observable
@MainActor
final class Recorder {
    private(set) var recording = false
    private(set) var authorized = false
    private(set) var problem: String?
    /// 0…1, for a little life on the button while recording.
    private(set) var level: Float = 0

    @ObservationIgnored private var recorder: AVAudioRecorder?
    @ObservationIgnored private var meter: Task<Void, Never>?
    @ObservationIgnored private var url: URL?

    init() {
        authorized = AVAudioApplication.shared.recordPermission == .granted
    }

    func authorize() async -> Bool {
        let ok = await AVAudioApplication.requestRecordPermission()
        if !ok { problem = "Vera can't hear the microphone. Settings → Vera." }
        authorized = ok
        return ok
    }

    func start() {
        guard !recording else { return }
        problem = nil
        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.record, mode: .measurement, options: .duckOthers)
            try session.setActive(true, options: .notifyOthersOnDeactivation)
        } catch {
            problem = "The microphone is busy."
            return
        }
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("vera-\(UUID().uuidString).m4a")
        // AAC in an m4a: small on the wire, and ffmpeg on the Mac reads
        // it without a second thought. 16 kHz mono is all speech needs.
        let settings: [String: Any] = [
            AVFormatIDKey: kAudioFormatMPEG4AAC,
            AVSampleRateKey: 16000,
            AVNumberOfChannelsKey: 1,
            AVEncoderAudioQualityKey: AVAudioQuality.medium.rawValue,
        ]
        do {
            let recorder = try AVAudioRecorder(url: file, settings: settings)
            recorder.isMeteringEnabled = true
            guard recorder.record() else {
                problem = "Couldn't start recording."
                return
            }
            self.recorder = recorder
            self.url = file
            recording = true
            startMetering()
        } catch {
            problem = "Couldn't start recording: \(error.localizedDescription)"
        }
    }

    /// Stops and hands back the file, or nil if nothing was recorded.
    @discardableResult
    func stop() -> URL? {
        guard recording else { return nil }
        recording = false
        meter?.cancel()
        meter = nil
        level = 0
        recorder?.stop()
        recorder = nil
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        return url
    }

    private func startMetering() {
        meter = Task { [weak self] in
            while let self, recording {
                recorder?.updateMeters()
                let power = recorder?.averagePower(forChannel: 0) ?? -60
                // -60 dB (quiet) … 0 dB (loud) → 0…1.
                level = max(0, min(1, (power + 60) / 60))
                try? await Task.sleep(for: .milliseconds(80))
            }
        }
    }
}
