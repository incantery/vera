import AppKit
import Observation
import SwiftUI

// The station: one object that owns the Mac's senses and face, and the
// thread between them.
//
// hotkey → overlay → listener → Vera Core → frames → overlay. Every step
// is also an observation or a log line, so the Health view can show
// what happened without anyone opening Console.

struct Interaction: Identifiable, Sendable {
    let id = UUID()
    let at: Date
    let said: String
    var answer: String
    var error: String?
    var run: String?
    var focus: String?
}

@Observable
@MainActor
final class Station {
    static let shared = Station()

    enum Voice: Equatable { case idle, listening, thinking }
    /// What a voice session is for: words for the cursor, or words for
    /// Vera. Decided by the gesture, never guessed.
    enum Purpose: Equatable { case dictate, ask }

    let settings = Settings()
    let log = EventLog()
    let core: Core
    let focus = FocusTracker()
    let lifecycle = LifecycleTracker()
    let hotkey = Hotkey()
    let keys = KeyTap()
    let panel = AskPanel()
    /// The newer on-device recogniser where the OS has it; Apple's
    /// older one — which needs Siri or Dictation switched on — before.
    let listener: any Transcriber = {
        if #available(macOS 26, *) { return AnalyzerListener() }
        return Listener()
    }()
    let overlay = Overlay()

    private(set) var voice: Voice = .idle
    private(set) var purpose: Purpose = .ask
    private(set) var accessibility = KeyTap.trusted
    private(set) var recentEvents: [ContextEvent] = []
    private(set) var interactions: [Interaction] = []
    private(set) var started = false

    /// One standing conversation per app launch. The Mac's conversation,
    /// as the phone has its own.
    private let conversation = "mac-" + UUID().uuidString.prefix(8)

    @ObservationIgnored private var pressedAt: Date?
    /// Carbon repeats `pressed` for as long as the key is held, with no
    /// flag to tell a repeat from a press. This is that flag.
    @ObservationIgnored private var keyDown = false
    @ObservationIgnored private var latched = false
    @ObservationIgnored private var exchange: Task<Void, Never>?
    /// When and where the last dictation was typed, so the next one can
    /// start with a space rather than running into it.
    @ObservationIgnored private var lastTyped: (at: Date, bundle: String)?

    private init() {
        core = Core(address: settings.coreAddress, log: log)
    }

    // MARK: - Lifecycle

    func start() {
        guard !started else { return }
        started = true
        log.info("Vera \(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") ?? "") starting as \(core.device)")

        core.onConnected = { [weak self] in
            guard let self else { return }
            send(.plain("device.connected", device: core.device))
            if let app = focus.current { send(.focused(app, device: core.device)) }
        }
        core.onCommand = { AppDriver.perform($0) }
        core.start()

        focus.onFocus = { [weak self] app in self?.send(.focused(app, device: self?.core.device ?? "")) }
        focus.onUnfocus = { [weak self] app in self?.send(.unfocused(app, device: self?.core.device ?? "")) }
        focus.start()

        // The machine itself, as a thing that comes and goes. Vera Core
        // supervises coding agents on this Mac, and every belief it has
        // about them is read from silence — so an absence it does not
        // know about becomes a fleet of agents that all appear to have
        // stalled at once, in the middle of the night.
        lifecycle.onSleep = { [weak self] at in
            guard let self else { return }
            // A hot microphone must not go into the sleep with us.
            if voice == .listening { cancelListening() }
            // Best effort: there is about no time here, and the report
            // that counts is the one on the way back.
            send(.plain("device.slept", device: core.device, at: at))
        }
        lifecycle.onWake = { [weak self] at, went, slept in
            guard let self else { return }
            if let went {
                // Said again, with the moment it happened: this is the
                // one that arrives, and the span is only a span with it.
                send(.plain("device.slept", device: core.device, at: went))
            }
            send(.plain("device.woke", device: core.device, at: at,
                        text: slept > 0 ? "asleep for " + LifecycleTracker.roughly(slept) : nil))
            core.wake()
            // No activation notification was delivered while it slept,
            // so what has focus now is anybody's guess until it is asked.
            focus.restate()
        }
        lifecycle.onNetwork = { [weak self] up, at in
            guard let self else { return }
            send(.plain(up ? "device.online" : "device.offline", device: core.device, at: at))
        }
        lifecycle.start()

        overlay.edge = settings.overlayEdge
        overlay.onPresented = { [weak self] in self?.send(.plain("surface.presented", device: self?.core.device ?? "")) }
        overlay.onDismissed = { [weak self] in self?.send(.plain("surface.dismissed", device: self?.core.device ?? "")) }

        listener.onEnded = { [weak self] said in self?.finishListening(with: said) }
        hotkey.onEvent = { [weak self] event in self?.handle(event) }
        applyHotkey()

        keys.onEvent = { [weak self] event in self?.handle(event) ?? false }
        panel.onAsk = { [weak self] text in self?.askTyped(text) }
        applyKeyTap()

        // Permissions are asked for on the first press, not at launch —
        // a dialog before the person has done anything reads as a demand.
    }

    func stop() {
        keys.stop()
        hotkey.unregister()
        lifecycle.stop()
        focus.stop()
        core.stop()
        listener.stop()
    }

    func applyHotkey() {
        hotkey.register(settings.hotkey)
        if let problem = hotkey.problem {
            log.error(problem)
        } else {
            log.info("Hotkey \(settings.hotkey.display) registered")
        }
    }

    /// Starts the Fn tap if Accessibility allows; otherwise keeps
    /// checking, because the grant happens in System Settings and
    /// nothing tells the app when the switch is flipped.
    func applyKeyTap() {
        accessibility = KeyTap.trusted
        let wanted = settings.fnDictates || settings.fnOpensVera
        if !wanted {
            keys.stop()
            return
        }
        if accessibility {
            if !keys.running {
                keys.start()
                if let problem = keys.problem { log.error(problem) } else { log.info("Fn key tap running") }
            }
            return
        }
        keys.stop()
        Task { [weak self] in
            while let self, !KeyTap.trusted, settings.fnDictates || settings.fnOpensVera {
                try? await Task.sleep(for: .seconds(2))
            }
            self?.applyKeyTap()
        }
    }

    func requestAccessibility() {
        KeyTap.requestTrust()
        log.info("asked for Accessibility")
        applyKeyTap()
    }

    func applyCoreAddress() {
        core.address = settings.coreAddress
        log.info("Vera Core address is now \(settings.coreAddress)")
    }

    func applyOverlayEdge() {
        overlay.edge = settings.overlayEdge
    }

    // MARK: - Observations

    private func send(_ event: ContextEvent) {
        recentEvents.insert(event, at: 0)
        if recentEvents.count > 100 { recentEvents.removeLast(recentEvents.count - 100) }
        log.event(event.summary)
        core.observe(event)
    }

    // MARK: - Voice

    private func handle(_ event: Hotkey.Event) {
        log.info("hotkey \(event) keyDown=\(keyDown) voice=\(voice)")
        switch event {
        case .pressed:
            guard !keyDown else { return } // auto-repeat, not a press
            keyDown = true
            if voice == .listening {
                // A latched session ends on the next press.
                finishListening()
                return
            }
            pressedAt = Date()
            latched = false
            beginListening(for: .ask)
        case .released:
            keyDown = false
            guard voice == .listening, let pressedAt else { return }
            let held = Date().timeIntervalSince(pressedAt)
            if settings.tapLatches && held < 0.3 {
                latched = true
                return
            }
            finishListening()
        }
    }

    /// Fn: held, the words go to the cursor; with T, the panel opens;
    /// with anything else (arrows, delete) it was never meant for us,
    /// and the recording that started on the way down is dropped.
    private func handle(_ event: KeyTap.Event) -> Bool {
        switch event {
        case .fnDown:
            guard settings.fnDictates, voice == .idle, !panel.isVisible else { return false }
            latched = false
            beginListening(for: .dictate)
            return false
        case .fnUp:
            guard voice == .listening, purpose == .dictate else { return false }
            finishListening()
            return false
        case .fnCombo(let keyCode):
            if voice == .listening, purpose == .dictate {
                cancelListening()
            }
            if settings.fnOpensVera, keyCode == 17 { // T
                panel.toggle()
                return true
            }
            return false
        }
    }

    /// The menu bar item's button: start a latched session, or end one.
    func toggleVoice() {
        if voice == .listening {
            finishListening()
        } else if voice == .idle {
            latched = true
            beginListening(for: .ask)
        }
    }

    private func beginListening(for what: Purpose) {
        exchange?.cancel()
        purpose = what
        voice = .listening
        overlay.show(.listening(heard: ""))
        Task { [weak self] in
            guard let self else { return }
            if !listener.authorized {
                guard await listener.authorize() else {
                    voice = .idle
                    overlay.show(.error(listener.problem ?? "Vera can't hear you."))
                    overlay.dismiss(after: .seconds(4))
                    return
                }
            }
            // The hotkey may have been released during the permission dialog.
            guard voice == .listening else { return }
            listener.preferredMicrophone = settings.microphone
            listener.start()
            if let problem = listener.problem {
                voice = .idle
                log.error(problem)
                overlay.show(.error(problem))
                overlay.dismiss(after: .seconds(4))
                return
            }
            send(.plain("voice.capture.started", device: core.device))
            mirrorHearing()
        }
    }

    /// Keeps the overlay's text in step with what is being heard, and
    /// ends a latched session that has gone quiet: a tap that was never
    /// followed by a second press should not leave the microphone open
    /// all afternoon.
    private func mirrorHearing() {
        Task { [weak self] in
            var last = ""
            var changed = Date()
            while let self, voice == .listening {
                let heard = listener.heard
                if heard != last {
                    last = heard
                    changed = Date()
                }
                overlay.show(.listening(heard: heard))
                if latched, Date().timeIntervalSince(changed) > 15 {
                    finishListening()
                    return
                }
                try? await Task.sleep(for: .milliseconds(80))
            }
        }
    }

    /// Ends listening. `with` is what the listener already handed back
    /// when it stopped on its own; otherwise the microphone closes now
    /// and the words are collected once the recogniser has drained —
    /// the key release is instant, the wait is shown as processing.
    private func finishListening(with ended: String?? = nil) {
        guard voice == .listening else { return }
        latched = false
        send(.plain("voice.capture.stopped", device: core.device))
        if let ended {
            settle(ended)
            return
        }
        voice = .thinking
        overlay.show(.processing(said: listener.heard, status: "Transcribing…"))
        exchange = Task { [weak self] in
            guard let self else { return }
            let said = await listener.finish()
            settle(said)
        }
    }

    /// Fn was a chord after all. Nothing is sent anywhere.
    private func cancelListening() {
        guard voice == .listening else { return }
        listener.stop()
        voice = .idle
        latched = false
        overlay.hide()
    }

    private func settle(_ said: String?) {
        guard let said else {
            voice = .idle
            log.info("nothing heard" + (listener.problem.map { " — \($0)" } ?? ""))
            overlay.show(.error(listener.problem ?? "Didn't catch that."))
            overlay.dismiss(after: .seconds(1))
            return
        }
        send(.plain("voice.transcription.completed", device: core.device, text: said))
        switch purpose {
        case .ask: ask(said)
        case .dictate: type(said)
        }
    }

    // MARK: - Dictation

    /// Words for the cursor: tidied by Vera Core if it answers in time,
    /// typed as heard if it does not. The cursor never waits on a model.
    private func type(_ said: String) {
        voice = .thinking
        let target = focus.current
        let editable = Paster.focusedFieldIsEditable()
        overlay.show(.processing(said: said, status: "Cleaning up…"))
        exchange = Task { [weak self] in
            guard let self else { return }
            var text = said
            var raw = true
            do {
                let cleaned = try await core.dictate(said, app: target)
                if !cleaned.text.isEmpty {
                    text = cleaned.text
                    raw = cleaned.raw ?? false
                }
            } catch {
                log.error("dictate cleanup: \(error.localizedDescription) — typing as heard")
            }
            // Two dictations into the same field within a minute are one
            // passage; separate them the way a typist would.
            if let lastTyped, lastTyped.bundle == target?.bundleID, Date().timeIntervalSince(lastTyped.at) < 60,
               let first = text.first, first.isLetter || first.isNumber {
                text = " " + text
            }
            do {
                try Paster.insert(text)
                lastTyped = (Date(), target?.bundleID ?? "")
                log.info("typed into \(target?.name ?? "?") (editable=\(editable.map { String($0) } ?? "unknown"), raw=\(raw)): \(text.prefix(120))")
                send(.plain("dictation.inserted", device: core.device, text: text))
                overlay.show(.inserted(text: text, into: target?.name, raw: raw))
                overlay.dismiss(after: .seconds(1.5))
            } catch {
                log.error("insert: \(error.localizedDescription)")
                overlay.show(.error(error.localizedDescription))
                overlay.dismiss(after: .seconds(4))
            }
            voice = .idle
        }
    }

    // MARK: - Typed

    /// A question from the panel. Same conversation, different door.
    ///
    /// Anything pasted into the panel goes with it, and is taken off
    /// the panel here: a picture belongs to exactly one question, and
    /// the next one starts empty.
    func askTyped(_ text: String) {
        let pictures = panel.model.attached
        panel.model.attached = []
        panel.model.lastQuestion = pictures.isEmpty ? text : text + " · " + Attachment.summary(pictures)
        panel.model.answer = ""
        panel.model.status = nil
        panel.model.error = nil
        panel.model.busy = true
        var interaction = Interaction(at: Date(), said: text, answer: "", focus: focus.current?.name)
        interactions.insert(interaction, at: 0)
        Task { [weak self] in
            guard let self else { return }
            do {
                for try await frame in core.say(text, conversation: conversation, images: pictures) {
                    if let run = frame.run { interaction.run = run }
                    if let status = frame.status { panel.model.status = status }
                    if let delta = frame.delta { panel.model.answer += delta }
                    if let error = frame.error { throw CoreError.broken(error) }
                }
                interaction.answer = panel.model.answer
            } catch {
                interaction.error = error.localizedDescription
                panel.model.error = error.localizedDescription
            }
            panel.model.busy = false
            if let i = interactions.firstIndex(where: { $0.id == interaction.id }) { interactions[i] = interaction }
        }
    }

    private func ask(_ said: String) {
        voice = .thinking
        overlay.show(.processing(said: said, status: nil))
        var interaction = Interaction(at: Date(), said: said, answer: "", focus: focus.current?.name)
        interactions.insert(interaction, at: 0)
        if interactions.count > 20 { interactions.removeLast(interactions.count - 20) }

        exchange = Task { [weak self] in
            guard let self else { return }
            var answer = ""
            do {
                for try await frame in core.say(said, conversation: conversation) {
                    if let run = frame.run { interaction.run = run }
                    if let status = frame.status {
                        overlay.show(.processing(said: said, status: status))
                    }
                    if let delta = frame.delta {
                        answer += delta
                        overlay.show(.answering(said: said, answer: answer, done: false))
                    }
                    if let error = frame.error {
                        throw CoreError.broken(error)
                    }
                }
                interaction.answer = answer
                log.info("Vera: \(answer.prefix(120))")
                overlay.show(.answering(said: said, answer: answer, done: true))
                overlay.dismiss(after: .seconds(min(12, max(4, Double(answer.count) / 18))))
            } catch {
                interaction.answer = answer
                interaction.error = error.localizedDescription
                log.error("exchange: \(error.localizedDescription)")
                overlay.show(.error(error.localizedDescription))
                overlay.dismiss(after: .seconds(5))
            }
            if let i = interactions.firstIndex(where: { $0.id == interaction.id }) {
                interactions[i] = interaction
            }
            voice = .idle
        }
    }
}
