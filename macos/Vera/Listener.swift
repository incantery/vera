import AVFoundation
import Foundation
import Observation
import Speech

// Hearing.
//
// The same arrangement as the phone (ios/Vera2/Speech.swift): the
// microphone feeds Apple's recogniser, on-device when macOS offers it,
// and only the words leave this process. Vera Core never sees audio.
//
// `Transcriber` is the seam. It is narrow on purpose: start, stop, what
// was heard. A different engine — Whisper on the Mac, a server — would
// implement it and nothing else here would know.

@MainActor
protocol Transcriber: AnyObject {
    var heard: String { get }
    var isListening: Bool { get }
    var problem: String? { get }
    var onDevice: Bool { get }
    var authorized: Bool { get }
    var micAuthorized: Bool { get }
    var speechAuthorized: Bool { get }
    /// Called when listening ends without `stop()` being asked for —
    /// the recogniser finished, timed out, or failed. Carries what was
    /// heard, if anything.
    var onEnded: (String?) -> Void { get set }
    /// The CoreAudio uid of the microphone to use; empty for the first
    /// physical one. Read when listening starts.
    var preferredMicrophone: String { get set }
    /// The microphone actually in use, once listening has started.
    var microphone: String? { get }
    func authorize() async -> Bool
    func start()
    /// Stops the microphone at once and returns what was said once the
    /// recogniser has finished with the audio it already has. Releasing
    /// the key is instant; the wait, if any, is here.
    func finish() async -> String?
    /// Stops without waiting; for tearing down, not for submitting.
    @discardableResult func stop() -> String?
}

@Observable
@MainActor
final class Listener: Transcriber {
    private(set) var heard = ""
    private(set) var isListening = false
    private(set) var problem: String?
    /// Whether recognition stays on this machine. Read by Health.
    private(set) var onDevice = false
    private(set) var authorized = false
    private(set) var micAuthorized = false
    private(set) var speechAuthorized = false
    @ObservationIgnored var onEnded: (String?) -> Void = { _ in }
    @ObservationIgnored var preferredMicrophone = ""
    private(set) var microphone: String?

    @ObservationIgnored private let recognizer = SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
    @ObservationIgnored private let engine = AVAudioEngine()
    @ObservationIgnored private var request: SFSpeechAudioBufferRecognitionRequest?
    @ObservationIgnored private var task: SFSpeechRecognitionTask?

    init() {
        onDevice = recognizer?.supportsOnDeviceRecognition ?? false
        micAuthorized = AVCaptureDevice.authorizationStatus(for: .audio) == .authorized
        speechAuthorized = SFSpeechRecognizer.authorizationStatus() == .authorized
        authorized = micAuthorized && speechAuthorized
    }

    /// Both permissions, because being granted one and denied the other
    /// is a state with no useful behaviour. Asked once; the system
    /// remembers after that.
    func authorize() async -> Bool {
        let speech = await withCheckedContinuation { done in
            SFSpeechRecognizer.requestAuthorization { @Sendable status in
                done.resume(returning: status)
            }
        }
        speechAuthorized = speech == .authorized
        guard speechAuthorized else {
            problem = "Vera can't use speech recognition. System Settings → Privacy & Security → Speech Recognition."
            return false
        }
        micAuthorized = await AVCaptureDevice.requestAccess(for: .audio)
        if !micAuthorized { problem = "Vera can't hear the microphone. System Settings → Privacy & Security → Microphone." }
        authorized = micAuthorized
        return authorized
    }

    func start() {
        guard !isListening else { return }
        guard let recognizer, recognizer.isAvailable else {
            problem = "Speech recognition isn't available right now."
            return
        }
        heard = ""
        problem = nil

        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        if recognizer.supportsOnDeviceRecognition {
            request.requiresOnDeviceRecognition = true
        }
        self.request = request

        let input = engine.inputNode
        if let mic = Microphones.choose(preferring: preferredMicrophone.isEmpty ? nil : preferredMicrophone) {
            do {
                try Microphones.use(mic, on: engine)
                microphone = mic.name
            } catch {
                problem = error.localizedDescription
                return
            }
        }
        let format = input.outputFormat(forBus: 0)
        guard format.sampleRate > 0, format.channelCount > 0 else {
            problem = "No microphone input is available."
            return
        }
        let sink = BufferSink(request: request)
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { @Sendable buffer, _ in
            sink.append(buffer)
        }

        engine.prepare()
        do {
            try engine.start()
        } catch {
            problem = "Couldn't start listening: \(error.localizedDescription)"
            teardown()
            return
        }

        isListening = true
        task = recognizer.recognitionTask(with: request) { @Sendable [weak self] result, error in
            let text = result?.bestTranscription.formattedString
            let finished = result?.isFinal ?? false
            // A cancelled task reports an error; that is us hanging up,
            // not a failure. Anything else is worth seeing.
            let failure = error.map { $0 as NSError }.flatMap { $0.code == 301 || $0.code == 216 ? nil : $0.localizedDescription }
            Task { @MainActor [weak self] in
                guard let self else { return }
                if let text { heard = text }
                if let failure, isListening { problem = failure }
                // The recogniser ending the session — silence, its
                // one-minute cap, or an error — is an ending the
                // station has to hear about, or the surface says
                // "Listening…" over a microphone that is off.
                if (finished || failure != nil), isListening {
                    let said = stop()
                    onEnded(said)
                }
            }
        }
    }

    /// SFSpeechRecognizer reports a final result only after endAudio();
    /// give it a moment for that before answering.
    func finish() async -> String? {
        guard isListening else { return nil }
        engine.inputNode.removeTap(onBus: 0)
        if engine.isRunning { engine.stop() }
        request?.endAudio()
        let before = heard
        for _ in 0..<15 where isListening {
            try? await Task.sleep(for: .milliseconds(100))
            if heard != before { break }
        }
        return stop() ?? (before.isEmpty ? nil : before)
    }

    /// Stops and hands back what was said, or nil if nothing was.
    @discardableResult
    func stop() -> String? {
        guard isListening else { return nil }
        isListening = false
        teardown()
        let said = heard.trimmingCharacters(in: .whitespacesAndNewlines)
        return said.isEmpty ? nil : said
    }

    private func teardown() {
        engine.inputNode.removeTap(onBus: 0)
        if engine.isRunning { engine.stop() }
        request?.endAudio()
        task?.cancel()
        request = nil
        task = nil
    }
}

/// Carries audio buffers from the render thread to the recogniser.
/// `append` is documented safe to call from the audio callback; Swift
/// cannot read that from a header, so it is asserted here.
private struct BufferSink: @unchecked Sendable {
    let request: SFSpeechAudioBufferRecognitionRequest
    func append(_ buffer: AVAudioPCMBuffer) { request.append(buffer) }
}
