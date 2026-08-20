import AVFoundation
import Foundation
import Observation
import Speech
import os

private let diag = Logger(subsystem: "com.incantery.vera.mac", category: "speech")

// Hearing, on macOS 26 and later.
//
// SpeechAnalyzer is Apple's newer recogniser: fully on-device, with a
// model the system downloads once, and — the reason it exists in this
// file — independent of Siri and Dictation being switched on. The older
// SFSpeechRecognizer (Listener.swift) refuses with "Siri and Dictation
// are disabled" on a Mac that has both off, which is a strange thing to
// require of someone who wants to talk to their own machine.
//
// Same shape as the phone's listener: the microphone feeds the
// recogniser, only words leave the process.

@available(macOS 26, *)
@Observable
@MainActor
final class AnalyzerListener: Transcriber {
    private(set) var heard = ""
    private(set) var isListening = false
    private(set) var problem: String?
    private(set) var authorized = false
    private(set) var micAuthorized = false
    /// Always: that is the point of this recogniser.
    let onDevice = true
    let speechAuthorized = true
    @ObservationIgnored var onEnded: (String?) -> Void = { _ in }
    @ObservationIgnored var preferredMicrophone = ""
    private(set) var microphone: String?

    @ObservationIgnored private let engine = AVAudioEngine()
    @ObservationIgnored private var analyzer: SpeechAnalyzer?
    @ObservationIgnored private var transcriber: SpeechTranscriber?
    @ObservationIgnored private var input: AsyncStream<AnalyzerInput>.Continuation?
    @ObservationIgnored private var results: Task<Void, Never>?
    @ObservationIgnored private var session = 0
    @ObservationIgnored private var finishing = false

    /// What has been settled, and what is still being revised.
    @ObservationIgnored private var finalized = ""
    @ObservationIgnored private var volatile = ""

    init() {
        micAuthorized = AVCaptureDevice.authorizationStatus(for: .audio) == .authorized
        authorized = micAuthorized
    }

    func authorize() async -> Bool {
        micAuthorized = await AVCaptureDevice.requestAccess(for: .audio)
        if !micAuthorized { problem = "Vera can't hear the microphone. System Settings → Privacy & Security → Microphone." }
        authorized = micAuthorized
        return authorized
    }

    func start() {
        guard !isListening else { return }
        heard = ""
        finalized = ""
        volatile = ""
        problem = nil
        isListening = true
        session += 1
        let mine = session

        Task { [weak self] in
            guard let self else { return }
            do {
                try await begin()
            } catch {
                diag.error("begin: \(error.localizedDescription, privacy: .public)")
                guard session == mine, isListening else { return }
                problem = "Couldn't start listening: \(error.localizedDescription)"
                let said = stop()
                onEnded(said)
            }
        }
    }

    private func begin() async throws {
        let locale = await SpeechTranscriber.supportedLocale(equivalentTo: Locale.current) ?? Locale(identifier: "en-US")
        let transcriber = SpeechTranscriber(
            locale: locale,
            transcriptionOptions: [],
            reportingOptions: [.volatileResults],
            attributeOptions: []
        )
        self.transcriber = transcriber

        // The model is a one-time download. Asking for it here rather
        // than failing means the first press on a fresh Mac is slow
        // rather than broken.
        let analyzer = SpeechAnalyzer(modules: [transcriber])
        self.analyzer = analyzer
        guard let format = await SpeechAnalyzer.bestAvailableAudioFormat(compatibleWith: [transcriber]) else {
            throw ListenerError.noFormat
        }

        let (stream, continuation) = AsyncStream<AnalyzerInput>.makeStream()
        input = continuation

        let node = engine.inputNode
        if let mic = Microphones.choose(preferring: preferredMicrophone.isEmpty ? nil : preferredMicrophone) {
            try Microphones.use(mic, on: engine)
            microphone = mic.name
            diag.info("microphone \(mic.name, privacy: .public)\(mic.virtualDevice ? " (virtual)" : "", privacy: .public)")
        }
        let micFormat = node.outputFormat(forBus: 0)
        guard micFormat.sampleRate > 0, micFormat.channelCount > 0 else { throw ListenerError.noMicrophone }
        diag.info("mic \(micFormat.sampleRate, privacy: .public)Hz/\(micFormat.channelCount, privacy: .public)ch → analyzer \(format.sampleRate, privacy: .public)Hz/\(format.channelCount, privacy: .public)ch")
        guard let converter = AVAudioConverter(from: micFormat, to: format) else { throw ListenerError.noFormat }
        let feed = Feed(converter: converter, format: format, continuation: continuation)
        node.installTap(onBus: 0, bufferSize: 2048, format: micFormat) { @Sendable buffer, _ in
            feed.push(buffer)
        }
        engine.prepare()
        try engine.start()

        // Only now the slow part. The tap is already filling the stream,
        // which buffers, so whatever is said while the model is checked
        // is kept rather than missed.
        let status = await AssetInventory.status(forModules: [transcriber])
        diag.info("locale \(locale.identifier, privacy: .public) assets \(String(describing: status), privacy: .public)")
        if let request = try await AssetInventory.assetInstallationRequest(supporting: [transcriber]) {
            diag.info("downloading speech model")
            try await request.downloadAndInstall()
            diag.info("speech model installed")
        }


        // Detached on purpose. The main actor is where the overlay lays
        // itself out, and a results loop that shares it arrives in
        // bursts whenever the UI is busy — which, while a sentence is
        // being typed out on screen, is always.
        results = Task.detached { [weak self] in
            do {
                for try await result in transcriber.results {
                    let text = String(result.text.characters)
                    let final = result.isFinal
                    diag.info("result final=\(final, privacy: .public) \(text, privacy: .public)")
                    await MainActor.run { [weak self] in
                        self?.take(text, final: final)
                    }
                }
            } catch {
                diag.error("results: \(error.localizedDescription, privacy: .public)")
                await MainActor.run { [weak self] in
                    guard let self, isListening else { return }
                    problem = error.localizedDescription
                    let said = stop()
                    onEnded(said)
                }
            }
        }

        try await analyzer.start(inputSequence: stream)
        diag.info("analyzer started")
    }

    private func take(_ text: String, final: Bool) {
        guard isListening || finishing else { return }
        if final {
            finalized += (finalized.isEmpty || text.hasPrefix(" ") ? "" : " ") + text
            volatile = ""
        } else {
            volatile = text
        }
        heard = (finalized + (volatile.isEmpty ? "" : " " + volatile)).trimmingCharacters(in: .whitespaces)
    }

    /// The key is up: the microphone closes now, and the answer waits
    /// for the recogniser to drain what it was given. The audio was
    /// being processed all along, so this is the tail, not the whole.
    func finish() async -> String? {
        guard isListening else { return nil }
        isListening = false
        finishing = true
        let mine = session
        engine.inputNode.removeTap(onBus: 0)
        if engine.isRunning { engine.stop() }
        input?.finish()
        input = nil
        let analyzer = self.analyzer
        let results = self.results
        self.analyzer = nil
        self.results = nil
        self.transcriber = nil

        let began = Date()
        // Bounded: a recogniser that will not finish should cost a
        // couple of seconds, not the exchange.
        let drained = Task {
            try? await analyzer?.finalizeAndFinishThroughEndOfInput()
            _ = await results?.result
        }
        let timeout = Task { try? await Task.sleep(for: .seconds(3)); drained.cancel() }
        _ = await drained.result
        timeout.cancel()
        results?.cancel()
        diag.info("finished in \(Int(Date().timeIntervalSince(began) * 1000), privacy: .public)ms")

        guard session == mine else { return nil }
        finishing = false
        let said = heard.trimmingCharacters(in: .whitespacesAndNewlines)
        return said.isEmpty ? nil : said
    }

    @discardableResult
    func stop() -> String? {
        guard isListening else { return nil }
        isListening = false
        finishing = false
        engine.inputNode.removeTap(onBus: 0)
        if engine.isRunning { engine.stop() }
        input?.finish()
        input = nil
        let analyzer = self.analyzer
        let results = self.results
        self.analyzer = nil
        self.results = nil
        self.transcriber = nil
        Task {
            try? await analyzer?.finalizeAndFinishThroughEndOfInput()
            results?.cancel()
        }
        let said = heard.trimmingCharacters(in: .whitespacesAndNewlines)
        return said.isEmpty ? nil : said
    }

    enum ListenerError: LocalizedError {
        case noFormat, noMicrophone
        var errorDescription: String? {
            switch self {
            case .noFormat: "Speech recognition has no usable audio format."
            case .noMicrophone: "No microphone input is available."
            }
        }
    }
}

/// Converts microphone buffers to the recogniser's format on the render
/// thread and hands them over. The converter is not Sendable and is
/// only ever touched from the tap, which the box asserts.
@available(macOS 26, *)
private final class Feed: @unchecked Sendable {
    let converter: AVAudioConverter
    let format: AVAudioFormat
    let continuation: AsyncStream<AnalyzerInput>.Continuation
    // Touched only on the render thread.
    private var buffers = 0
    private var peak: Float = 0
    private var fed: AVAudioFrameCount = 0

    init(converter: AVAudioConverter, format: AVAudioFormat, continuation: AsyncStream<AnalyzerInput>.Continuation) {
        self.converter = converter
        self.format = format
        self.continuation = continuation
    }

    func push(_ buffer: AVAudioPCMBuffer) {
        buffers += 1
        if let ch = buffer.floatChannelData?[0] {
            for i in 0..<Int(buffer.frameLength) { peak = max(peak, abs(ch[i])) }
        }
        let ratio = format.sampleRate / buffer.format.sampleRate
        let capacity = AVAudioFrameCount(Double(buffer.frameLength) * ratio) + 16
        guard let out = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: capacity) else { return }
        var consumed = false
        var error: NSError?
        converter.convert(to: out, error: &error) { _, status in
            if consumed {
                status.pointee = .noDataNow
                return nil
            }
            consumed = true
            status.pointee = .haveData
            return buffer
        }
        if error == nil, out.frameLength > 0 {
            fed += out.frameLength
            continuation.yield(AnalyzerInput(buffer: out))
        }
        // Every second or so: is there sound, and is it getting through?
        if buffers % 25 == 0 {
            diag.info("audio buffers=\(self.buffers, privacy: .public) peak=\(self.peak, privacy: .public) fed=\(self.fed, privacy: .public) frames\(error.map { " error=\($0.localizedDescription)" } ?? "", privacy: .public)")
            peak = 0
        }
    }
}
