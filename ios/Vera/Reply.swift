import Foundation

// What came back, in the order it came back.
//
// A reply used to be a string. It is not one any more: Vera calls
// tools while she answers, and some of those she will not run without
// a word from the person. Both arrive interleaved with the words, and
// both have to be shown where they arrived — a question about a file,
// drawn under the paragraph it interrupted, is a question about
// nothing.
//
// So an exchange is a list of steps rather than a reply, and the words
// are one kind of step. Everything here is a value type with no
// opinions about the network: the whole of it can be driven by handing
// it frames, which is exactly what the tests do.

/// One tool round: the call, whatever it printed while it ran, and
/// what it came back with.
struct ToolRun: Identifiable, Codable, Sendable, Equatable {
    /// The call's id on the Mac. Output and result are tied to it.
    var id: String
    var name: String
    var args: String

    /// What it has printed so far. The Mac caps this before it sends
    /// it, so a command that prints a megabyte is not a megabyte here.
    var output = ""

    /// Absent while it is still running.
    var result: String?
    var durationMs: Int?
    var cost: Double?

    /// The exchange ended with this call still going. Nothing more is
    /// coming for it, so the row should stop spinning and say so.
    var stopped = false

    var isRunning: Bool { result == nil && !stopped }

    /// mote's convention, and what the terminal marks a card failed on.
    var isFailed: Bool { result?.hasPrefix("error:") ?? false }

    /// The result, or the output if there is no result yet — what an
    /// opened row shows.
    var body: String { result ?? output }
}

/// The three words. They are the Mac's vocabulary, not the phone's:
/// anything else is refused at the other end.
enum Choice {
    static let yes = "yes"
    static let no = "no"
    static let always = "always"

    /// A button on the card: the word that goes back, what it says on
    /// the front, and what it costs to press.
    struct Option: Identifiable, Sendable, Equatable {
        let choice: String
        let label: String
        let help: String
        var id: String { choice }
    }

    /// In the order a person reads them.
    static let all: [Option] = [
        Option(choice: yes, label: "Yes", help: "run it, and ask again next time"),
        Option(choice: no, label: "No", help: "don't run it"),
        Option(choice: always, label: "Always", help: "run it, and stop asking about ones like it"),
    ]
}

/// A tool Vera will not run without a word. The exchange is parked on
/// it: nothing else arrives until the answer goes back, and the Mac
/// answers itself "no" after two minutes of nobody saying anything.
struct Question: Identifiable, Codable, Sendable, Equatable {
    enum State: String, Codable, Sendable {
        /// On screen, waiting, answerable.
        case waiting
        /// A word went back. Nothing here can change it.
        case answered
        /// The exchange ended without an answer. Too late to say one.
        case closed
    }

    var id: String
    var name: String
    var args: String
    /// The Mac's own sentence for why it is asking at all.
    var reason: String

    var state: State = .waiting
    /// What was said, once something was said.
    var choice: String?
    /// The word did not get through. The question is open again, and
    /// this says why it has to be answered twice.
    var trouble: String?

    var isOpen: Bool { state == .waiting }
}

/// One thing that happened in a reply. The id is the view's, not the
/// Mac's — two calls to the same tool are two rows.
struct Step: Identifiable, Codable, Sendable, Equatable {
    var id = UUID()
    var body: Body

    enum Body: Codable, Sendable, Equatable {
        case said(String)
        case tool(ToolRun)
        case ask(Question)
    }
}

/// One turn: what was said, and everything that came back for it.
struct Exchange: Identifiable, Codable, Sendable {
    var id = UUID()
    var said: String
    /// How many pictures went with it. A count rather than the
    /// pictures: the transcript is a record of what was said, and the
    /// bytes are the Mac's to keep. Optional so a transcript written
    /// before this existed still decodes.
    var images: Int?
    var steps: [Step] = []
    var failed: String?
    var done = false

    /// What Vera is doing while she has nothing to say yet. Some work
    /// takes minutes, and a silent screen for that long reads as
    /// broken.
    var status: String?

    /// The work on the Mac this came from, and how much of it has been
    /// seen. Together they are enough to rejoin it after the app has
    /// been closed — the Mac kept going, so there is something to
    /// rejoin.
    var run: String?
    var seen = 0

    /// Everything Vera has said, words only. The screen draws the steps
    /// themselves; this is for the things that only ever wanted the
    /// prose — "has she started yet", and scrolling to the newest word.
    var reply: String {
        steps.reduce(into: "") { text, step in
            if case .said(let words) = step.body { text += words }
        }
    }

    /// The question waiting on a person, if there is one. There is at
    /// most one: the exchange is parked while it is open.
    var openQuestion: Question? {
        for step in steps.reversed() {
            if case .ask(let question) = step.body, question.isOpen { return question }
        }
        return nil
    }
}

// MARK: - Folding frames in

extension Exchange {

    /// Take one frame off the wire. Everything the screen shows about
    /// a reply is built here, and nothing here talks to the network —
    /// which is what makes the whole of it testable by hand.
    mutating func apply(_ frame: Frame) {
        seen += 1
        if let run = frame.run { self.run = run }
        if let delta = frame.delta, !delta.isEmpty {
            say(delta)
            // The first real word means the waiting is over.
            status = nil
        }
        if let status = frame.status { self.status = status }
        if let call = frame.toolCall { began(call) }
        if let output = frame.toolOutput { printed(output) }
        if let result = frame.toolResult { finished(result) }
        if let ask = frame.ask { raise(ask) }
        if let error = frame.error { failed = error }
        if frame.done == true || frame.error != nil { close() }
    }

    /// Words go on the end of the words already there, so a paragraph
    /// broken across fifty deltas is one block of text and not fifty.
    private mutating func say(_ words: String) {
        if let last = steps.indices.last, case .said(let existing) = steps[last].body {
            steps[last].body = .said(existing + words)
            return
        }
        steps.append(Step(body: .said(words)))
    }

    private mutating func began(_ call: Frame.ToolCall) {
        // A repeated call frame — a rejoin that overlapped by a frame —
        // is the same round, not a second one.
        guard tool(call.id) == nil else { return }
        steps.append(Step(body: .tool(ToolRun(id: call.id, name: call.name, args: call.args))))
    }

    private mutating func printed(_ output: Frame.ToolOutput) {
        guard let index = tool(output.id) else { return }
        guard case .tool(var run) = steps[index].body else { return }
        run.output += output.text
        steps[index].body = .tool(run)
    }

    private mutating func finished(_ result: Frame.ToolResult) {
        guard let index = tool(result.id) else { return }
        guard case .tool(var run) = steps[index].body else { return }
        run.result = result.result
        run.durationMs = result.durationMs
        run.cost = result.costUSD
        // A result means it was not stopped after all: a rejoined run
        // finishes calls the phone had already given up on.
        run.stopped = false
        steps[index].body = .tool(run)
    }

    private mutating func raise(_ ask: Frame.Ask) {
        // A rejoined run replays the question. If it is already on
        // screen it keeps whatever was said to it.
        guard question(ask.id) == nil else { return }
        steps.append(Step(body: .ask(Question(id: ask.id, name: ask.name, args: ask.args, reason: ask.text))))
        // The Mac sends a status line beside the question for clients
        // that cannot draw one. This one can, so the sentence about
        // waiting would only say twice what the card says once.
        status = nil
    }

    /// The exchange is over, however it ended. Nothing is still coming,
    /// so nothing on screen should claim to be waiting for it.
    mutating func close() {
        done = true
        status = nil
        for index in steps.indices {
            switch steps[index].body {
            case .ask(var question) where question.isOpen:
                question.state = .closed
                steps[index].body = .ask(question)
            case .tool(var run) where run.isRunning:
                run.stopped = true
                steps[index].body = .tool(run)
            default:
                break
            }
        }
    }
}

// MARK: - Answering

extension Exchange {

    /// The person tapped a button. The card closes on the tap rather
    /// than on the round trip — the answer is theirs the moment they
    /// give it, and a card that stays live while a request is in the
    /// air is a card that gets answered twice.
    ///
    /// It reports whether there was anything to answer, so a second tap
    /// on a closed question sends nothing.
    @discardableResult
    mutating func answering(_ id: String, _ choice: String) -> Bool {
        guard let index = question(id) else { return false }
        guard case .ask(var question) = steps[index].body, question.isOpen else { return false }
        question.state = .answered
        question.choice = choice
        question.trouble = nil
        steps[index].body = .ask(question)
        return true
    }

    /// The word did not get through. The question is open again, with
    /// the reason under it, because the exchange on the other end is
    /// still parked on an answer that never arrived.
    mutating func answerFailed(_ id: String, _ why: String) {
        guard let index = question(id) else { return }
        guard case .ask(var question) = steps[index].body, question.state == .answered else { return }
        question.state = .waiting
        question.choice = nil
        question.trouble = why
        steps[index].body = .ask(question)
    }

    // MARK: - Finding things by the Mac's ids

    private func tool(_ id: String) -> Int? {
        steps.lastIndex { if case .tool(let run) = $0.body { return run.id == id } else { return false } }
    }

    private func question(_ id: String) -> Int? {
        steps.lastIndex { if case .ask(let question) = $0.body { return question.id == id } else { return false } }
    }
}

// MARK: - What is on disk

extension Exchange {

    enum CodingKeys: String, CodingKey {
        case id, said, images, steps, failed, done, status, run, seen
        /// Only ever read. A transcript written before a reply could be
        /// anything but words has one of these and no steps.
        case reply
    }

    init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(UUID.self, forKey: .id) ?? UUID()
        said = try container.decodeIfPresent(String.self, forKey: .said) ?? ""
        images = try container.decodeIfPresent(Int.self, forKey: .images)
        failed = try container.decodeIfPresent(String.self, forKey: .failed)
        done = try container.decodeIfPresent(Bool.self, forKey: .done) ?? false
        status = try container.decodeIfPresent(String.self, forKey: .status)
        run = try container.decodeIfPresent(String.self, forKey: .run)
        seen = try container.decodeIfPresent(Int.self, forKey: .seen) ?? 0

        if let steps = try container.decodeIfPresent([Step].self, forKey: .steps) {
            self.steps = steps
        } else if let words = try container.decodeIfPresent(String.self, forKey: .reply), !words.isEmpty {
            self.steps = [Step(body: .said(words))]
        }
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(said, forKey: .said)
        try container.encodeIfPresent(images, forKey: .images)
        try container.encode(steps, forKey: .steps)
        try container.encodeIfPresent(failed, forKey: .failed)
        try container.encode(done, forKey: .done)
        try container.encodeIfPresent(status, forKey: .status)
        try container.encodeIfPresent(run, forKey: .run)
        try container.encode(seen, forKey: .seen)
    }
}
