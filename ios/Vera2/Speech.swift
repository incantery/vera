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
//
// Two ways to use it. A single shot — start, talk, finish — for the
// conversation composer. And a CONTINUOUS run for dictation: the engine
// and its tap stay up for the whole session, and only the recognition
// request is rotated when Apple ends one by itself (it caps a session
// at about a minute, or a long silence). Rotating rather than
// restarting is the whole point: stopping the engine drops the audio
// spoken during the gap, and that is a whole sentence gone. The tap
// never stops, so nothing is ever dropped between chunks.

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

    /// In a continuous run, called with each settled chunk as Apple
    /// ends one session and the next begins. The final chunk arrives
    /// through `finish()`, not here.
    @ObservationIgnored var onChunk: (String) -> Void = { _ in }

    @ObservationIgnored private let recognizer = SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
    @ObservationIgnored private let engine = AVAudioEngine()
    @ObservationIgnored private let sink = BufferSink()
    @ObservationIgnored private var request: SFSpeechAudioBufferRecognitionRequest?
    @ObservationIgnored private var task: SFSpeechRecognitionTask?
    @ObservationIgnored private var continuous = false
    @ObservationIgnored private var tapped = false

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

    /// Single shot: start, talk, finish. Apple ending the session stops
    /// it. For the conversation composer.
    func start() { begin(continuous: false) }

    /// Continuous: the engine stays up until `finish()`; each session
    /// Apple ends becomes an onChunk, and listening carries straight on
    /// with no gap in the audio. For dictation, where a thought is
    /// longer than one recogniser session.
    func startContinuous() { begin(continuous: true) }

    private func begin(continuous: Bool) {
        guard !isListening else { return }
        guard let recognizer, recognizer.isAvailable else {
            problem = "Speech recognition isn't available right now."
            return
        }
        self.continuous = continuous
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

        if !tapped {
            let input = engine.inputNode
            let format = input.outputFormat(forBus: 0)
            let sink = sink
            input.installTap(onBus: 0, bufferSize: 1024, format: format) { @Sendable buffer, _ in
                sink.append(buffer)
            }
            engine.prepare()
            do {
                try engine.start()
            } catch {
                problem = "Couldn't start listening."
                input.removeTap(onBus: 0)
                return
            }
            tapped = true
        }

        isListening = true
        listen()
    }

    /// Opens a fresh recognition request against the running tap. The
    /// engine is not touched, so the audio stream is unbroken.
    private func listen() {
        guard let recognizer else { return }
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        if recognizer.supportsOnDeviceRecognition {
            request.requiresOnDeviceRecognition = true
        }
        self.request = request
        sink.point(at: request)

        task = recognizer.recognitionTask(with: request) { @Sendable [weak self] result, error in
            // The recognizer answers off the main actor and hands back
            // a non-Sendable result, so only the string crosses.
            let text = result?.bestTranscription.formattedString
            let finished = result?.isFinal ?? false
            let failed = error != nil
            Task { @MainActor [weak self] in
                guard let self else { return }
                if let text { heard = text }
                if finished || failed { rotateOrStop() }
            }
        }
    }

    /// A recognition session ended. In a continuous run, hand off what
    /// it settled and open the next one without stopping the engine; in
    /// a single shot, stop.
    private func rotateOrStop() {
        guard isListening else { return }
        let chunk = heard.trimmingCharacters(in: .whitespacesAndNewlines)
        guard continuous else {
            stop()
            return
        }
        task = nil
        request = nil
        heard = ""
        // Open the next session FIRST, so the running tap is feeding a
        // live request before the hand-off — a buffer arriving in the
        // hand-off is kept, not dropped into an abandoned request.
        if isListening { listen() }
        if !chunk.isEmpty { onChunk(chunk) }
    }

    /// Ends the run and hands back the last chunk once the recogniser
    /// has settled it — the tap is closed here, not between chunks.
    func finish() async -> String? {
        guard isListening else { return nil }
        continuous = false
        request?.endAudio()
        // Wait for the recogniser's FINAL result rather than the next
        // partial, which is how half a sentence gets typed and the rest
        // said again. rotateOrStop() → stop() clears isListening.
        for _ in 0..<20 where isListening {
            try? await Task.sleep(for: .milliseconds(100))
        }
        let said = heard.trimmingCharacters(in: .whitespacesAndNewlines)
        if isListening { stop() }
        return said.isEmpty ? nil : said
    }

    /// Stops now. Returns what the current chunk had, or nil.
    @discardableResult
    func stop() -> String? {
        guard isListening else { return nil }
        isListening = false
        continuous = false
        let said = heard.trimmingCharacters(in: .whitespacesAndNewlines)
        teardown()
        return said.isEmpty ? nil : said
    }

    private func teardown() {
        if tapped {
            engine.inputNode.removeTap(onBus: 0)
            if engine.isRunning { engine.stop() }
            tapped = false
        }
        request?.endAudio()
        task?.cancel()
        request = nil
        task = nil
        // The audio session stays active across a stop so a quick next
        // run does not hit a busy microphone; `release()` gives it back.
    }

    /// Give the audio session back. Call when leaving the screen.
    func release() {
        stop()
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }
}

/// Carries audio buffers from the render thread to the recognizer, and
/// lets the request behind it be swapped between chunks without ever
/// stopping the tap. `append` is documented safe to call from the audio
/// callback; the lock guards only the pointer swap, which is brief and
/// off the audio path's steady state.
private final class BufferSink: @unchecked Sendable {
    private let lock = NSLock()
    private var request: SFSpeechAudioBufferRecognitionRequest?

    func point(at request: SFSpeechAudioBufferRecognitionRequest) {
        lock.lock(); self.request = request; lock.unlock()
    }
    func append(_ buffer: AVAudioPCMBuffer) {
        lock.lock(); let request = self.request; lock.unlock()
        request?.append(buffer)
    }
}
