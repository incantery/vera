import AVFoundation
import Foundation
import Observation
import Speech

// Hearing.
//
// On-device recognition when the phone offers it — it is faster, it
// works on a plane, and it does not post what you said to a third
// party on the way to your own Mac, which would be a strange thing for
// this product to do.
//
// Every callback in here is marked @Sendable ON PURPOSE. This class is
// @MainActor, so a closure written inside it inherits main-actor
// isolation, and Swift 6 checks that isolation at runtime. Speech and
// AVAudioEngine call back from whatever thread they please — the audio
// tap comes off the render thread — so an inherited @MainActor closure
// is not a warning, it is dispatch_assert_queue_fail and a dead
// process the first time a buffer arrives.

@Observable
@MainActor
final class Listener {

    /// What has been heard so far, updated as you speak.
    private(set) var heard = ""
    private(set) var isListening = false
    private(set) var problem: String?

    /// Asked for once, remembered after. Requesting on every tap would
    /// put an await between the tap and the recording, which is the one
    /// place in this app latency is unaffordable.
    private(set) var authorized = false

    @ObservationIgnored private let recognizer = SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
    @ObservationIgnored private let engine = AVAudioEngine()
    @ObservationIgnored private var request: SFSpeechAudioBufferRecognitionRequest?
    @ObservationIgnored private var task: SFSpeechRecognitionTask?

    /// Both permissions, asked for together, because being granted one
    /// and denied the other is a state with no useful behaviour.
    func authorize() async -> Bool {
        let speech = await withCheckedContinuation { done in
            SFSpeechRecognizer.requestAuthorization { @Sendable status in
                done.resume(returning: status)
            }
        }
        guard speech == .authorized else {
            problem = "Vera can't use speech recognition. Settings → Vera2."
            return false
        }
        let mic = await AVAudioApplication.requestRecordPermission()
        if !mic { problem = "Vera can't hear the microphone. Settings → Vera2." }
        authorized = mic
        return mic
    }

    func start() {
        guard !isListening else { return }
        guard let recognizer, recognizer.isAvailable else {
            problem = "Speech recognition isn't available right now."
            return
        }

        heard = ""
        problem = nil

        do {
            let session = AVAudioSession.sharedInstance()
            // .measurement turns off the processing meant to make calls
            // sound nice, which is processing a recognizer does not want.
            try session.setCategory(.record, mode: .measurement, options: .duckOthers)
            try session.setActive(true, options: .notifyOthersOnDeactivation)
        } catch {
            problem = "The microphone is busy."
            return
        }

        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        if recognizer.supportsOnDeviceRecognition {
            request.requiresOnDeviceRecognition = true
        }
        self.request = request

        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        // The request is not Sendable and the tap is not main-actor, so
        // the box states the contract the audio API already has: append
        // is safe to call from the render thread, and nothing else
        // touches the request while the engine is running.
        let sink = BufferSink(request: request)
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { @Sendable buffer, _ in
            sink.append(buffer)
        }

        engine.prepare()
        do {
            try engine.start()
        } catch {
            problem = "Couldn't start listening."
            teardown()
            return
        }

        isListening = true
        task = recognizer.recognitionTask(with: request) { @Sendable [weak self] result, error in
            // The recognizer answers off the main actor and hands back
            // a non-Sendable result, so only the string crosses.
            let text = result?.bestTranscription.formattedString
            let finished = result?.isFinal ?? false
            let failed = error != nil
            Task { @MainActor [weak self] in
                guard let self else { return }
                if let text { heard = text }
                if finished || failed { stop() }
            }
        }
    }

    /// Stops the microphone now and hands back what was said once the
    /// recogniser has finished with what it already has — the release
    /// is instant; the wait, bounded, is here. For push-to-talk, where
    /// the last word is usually still being recognised when the thumb
    /// lifts.
    func finish() async -> String? {
        guard isListening else { return nil }
        engine.inputNode.removeTap(onBus: 0)
        if engine.isRunning { engine.stop() }
        request?.endAudio()
        // Wait for the recogniser's FINAL result — it stops us itself
        // when it arrives — not merely for the next partial, which is
        // how half a sentence gets typed and the rest said again.
        let before = heard
        for _ in 0..<20 where isListening {
            try? await Task.sleep(for: .milliseconds(100))
        }
        return stop() ?? (before.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : before)
    }

    /// Stops listening and hands back what was said, or nil if nothing
    /// was. Silence is not a message.
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
        // The audio session stays active: the next chunk starts a
        // breath later, and deactivating and reactivating back-to-back
        // is how "the microphone is busy" happens. `release()` is for
        // actually being done.
    }

    /// Give the audio session back. Call when leaving the screen.
    func release() {
        stop()
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }
}


/// Carries audio buffers from the render thread to the recognizer.
/// SFSpeechAudioBufferRecognitionRequest.append is documented as safe
/// to call from the audio callback; Swift cannot read that from a
/// header, so it is asserted here rather than worked around at every
/// call site.
private struct BufferSink: @unchecked Sendable {
    let request: SFSpeechAudioBufferRecognitionRequest
    func append(_ buffer: AVAudioPCMBuffer) { request.append(buffer) }
}
