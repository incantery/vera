import XCTest
@testable import Vera

// A question, from the frame that raises it to the word that closes it.
//
// Exchange.apply is the whole of it and touches nothing outside itself,
// so these drive it the way the wire does: frames in, screen out.

final class AskTests: XCTestCase {

    private func ask(_ id: String = "call_2") -> Frame {
        var frame = Frame()
        frame.ask = Frame.Ask(id: id, name: "write", args: #"{"path":"/tmp/x"}"#,
                              text: "nothing said otherwise")
        return frame
    }

    private func question(in exchange: Exchange, _ id: String = "call_2") -> Question? {
        for step in exchange.steps {
            if case .ask(let question) = step.body, question.id == id { return question }
        }
        return nil
    }

    func testAQuestionArrivesWhereItWasAsked() {
        var exchange = Exchange(said: "tidy that up")
        exchange.apply(Frame(delta: "I'll write it out."))
        exchange.apply(ask())

        XCTAssertEqual(exchange.steps.count, 2)
        let question = try? XCTUnwrap(self.question(in: exchange))
        XCTAssertEqual(question?.name, "write")
        XCTAssertEqual(question?.reason, "nothing said otherwise")
        XCTAssertEqual(question?.state, .waiting)
        XCTAssertEqual(exchange.openQuestion?.id, "call_2")
        // The words before it are still the words before it.
        XCTAssertEqual(exchange.reply, "I'll write it out.")
    }

    // verad sends a status line beside the question for clients that
    // cannot draw one. This one can, and two sentences saying the same
    // thing is one too many.
    func testTheQuestionReplacesTheSentenceAboutWaiting() {
        var exchange = Exchange(said: "go on")
        exchange.apply(Frame(status: "Waiting for you: write…"))
        exchange.apply(ask())
        XCTAssertNil(exchange.status)
    }

    func testAnsweringClosesTheCard() {
        var exchange = Exchange(said: "go on")
        exchange.apply(ask())

        XCTAssertTrue(exchange.answering("call_2", Choice.yes))
        XCTAssertEqual(question(in: exchange)?.state, .answered)
        XCTAssertEqual(question(in: exchange)?.choice, Choice.yes)
        XCTAssertNil(exchange.openQuestion)

        // A second tap has nothing to answer, so nothing goes back.
        XCTAssertFalse(exchange.answering("call_2", Choice.no))
        XCTAssertEqual(question(in: exchange)?.choice, Choice.yes)
    }

    func testAWordThatDidNotGetThroughOpensItAgain() {
        var exchange = Exchange(said: "go on")
        exchange.apply(ask())
        XCTAssertTrue(exchange.answering("call_2", Choice.always))

        exchange.answerFailed("call_2", "I can't reach that Mac from here.")
        XCTAssertEqual(question(in: exchange)?.state, .waiting)
        XCTAssertNil(question(in: exchange)?.choice)
        XCTAssertEqual(question(in: exchange)?.trouble, "I can't reach that Mac from here.")
        // Still answerable — the exchange on the other end is still
        // parked on an answer that never arrived.
        XCTAssertNotNil(exchange.openQuestion)
        XCTAssertTrue(exchange.answering("call_2", Choice.always))
        XCTAssertNil(question(in: exchange)?.trouble)
    }

    // A failure that arrives after the question was answered some other
    // way — on the Mac, or by the two-minute timeout — must not reopen
    // a card that is no longer a question.
    func testAFailureCannotReopenAClosedQuestion() {
        var exchange = Exchange(said: "go on")
        exchange.apply(ask())
        exchange.close()
        exchange.answerFailed("call_2", "nope")
        XCTAssertEqual(question(in: exchange)?.state, .closed)
    }

    func testTheExchangeEndingClosesAQuestionNobodyAnswered() {
        var exchange = Exchange(said: "go on")
        exchange.apply(ask())
        exchange.apply(Frame(delta: "They did not answer, so no.", done: true))

        XCTAssertEqual(question(in: exchange)?.state, .closed)
        XCTAssertNil(exchange.openQuestion)
        XCTAssertFalse(exchange.answering("call_2", Choice.yes))
        XCTAssertTrue(exchange.done)
    }

    func testAnAnsweredQuestionSurvivesTheEnd() {
        var exchange = Exchange(said: "go on")
        exchange.apply(ask())
        XCTAssertTrue(exchange.answering("call_2", Choice.yes))
        exchange.close()
        XCTAssertEqual(question(in: exchange)?.state, .answered)
    }

    // A rejoined run replays its frames from where the phone stopped
    // reading, and an overlap of one frame is normal.
    func testAReplayedQuestionIsNotASecondQuestion() {
        var exchange = Exchange(said: "go on")
        exchange.apply(ask())
        XCTAssertTrue(exchange.answering("call_2", Choice.no))
        exchange.apply(ask())

        XCTAssertEqual(exchange.steps.count, 1)
        XCTAssertEqual(question(in: exchange)?.choice, Choice.no)
    }
}

// The tool rounds around it: a call, what it printed while it ran, and
// what it came back with — all tied together by the Mac's call id.

final class ToolRoundTests: XCTestCase {

    private func run(in exchange: Exchange, _ id: String = "call_1") -> ToolRun? {
        for step in exchange.steps {
            if case .tool(let run) = step.body, run.id == id { return run }
        }
        return nil
    }

    private func call(_ id: String = "call_1") -> Frame {
        var frame = Frame()
        frame.toolCall = Frame.ToolCall(id: id, name: "bash", args: #"{"command":"ls"}"#)
        return frame
    }

    func testACallBecomesARunningRow() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(call())

        let run = run(in: exchange)
        XCTAssertEqual(run?.name, "bash")
        XCTAssertTrue(run?.isRunning ?? false)
        XCTAssertFalse(run?.isFailed ?? true)
    }

    func testOutputArrivesWhileItRuns() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(call())
        var first = Frame(); first.toolOutput = Frame.ToolOutput(id: "call_1", text: "one\n")
        var second = Frame(); second.toolOutput = Frame.ToolOutput(id: "call_1", text: "two\n")
        exchange.apply(first)
        exchange.apply(second)

        XCTAssertEqual(run(in: exchange)?.output, "one\ntwo\n")
        XCTAssertTrue(run(in: exchange)?.isRunning ?? false)
    }

    func testTheResultStopsTheSpinner() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(call())
        var done = Frame()
        done.toolResult = Frame.ToolResult(id: "call_1", result: "one\ntwo", durationMs: 1200, costUSD: 0.0042)
        exchange.apply(done)

        let run = run(in: exchange)
        XCTAssertFalse(run?.isRunning ?? true)
        XCTAssertEqual(run?.durationMs, 1200)
        XCTAssertEqual(run?.cost ?? 0, 0.0042, accuracy: 0.000001)
        XCTAssertEqual(run?.body, "one\ntwo")
    }

    // mote's convention, and what the terminal marks a card failed on.
    func testAnErrorResultIsAFailedRow() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(call())
        var done = Frame()
        done.toolResult = Frame.ToolResult(id: "call_1", result: "error: no such file", durationMs: 2)
        exchange.apply(done)
        XCTAssertTrue(run(in: exchange)?.isFailed ?? false)
    }

    func testARunningToolStopsWhenTheExchangeDoes() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(call())
        exchange.close()

        let run = run(in: exchange)
        XCTAssertFalse(run?.isRunning ?? true)
        XCTAssertTrue(run?.stopped ?? false)
    }

    // A phone that came back to a run still going gets the result for
    // a call it had already written off.
    func testAResultAfterTheStopUnstopsTheRow() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(call())
        exchange.close()
        var done = Frame()
        done.toolResult = Frame.ToolResult(id: "call_1", result: "one", durationMs: 9)
        exchange.apply(done)

        XCTAssertFalse(run(in: exchange)?.stopped ?? true)
        XCTAssertEqual(run(in: exchange)?.body, "one")
    }

    // Output for a call the phone never saw belongs to nothing, and
    // inventing a row for it would put a nameless card on the screen.
    func testStrayOutputIsIgnored() {
        var exchange = Exchange(said: "what's in there")
        var stray = Frame(); stray.toolOutput = Frame.ToolOutput(id: "call_9", text: "?")
        exchange.apply(stray)
        XCTAssertTrue(exchange.steps.isEmpty)
    }

    func testWordsToolWordsStayInThatOrder() {
        var exchange = Exchange(said: "what's in there")
        exchange.apply(Frame(delta: "Let me look. "))
        exchange.apply(Frame(delta: "One moment."))
        exchange.apply(call())
        var done = Frame(); done.toolResult = Frame.ToolResult(id: "call_1", result: "one", durationMs: 1)
        exchange.apply(done)
        exchange.apply(Frame(delta: "There's one file."))

        XCTAssertEqual(exchange.steps.count, 3)
        if case .said(let words) = exchange.steps[0].body {
            // Fifty deltas are one paragraph, not fifty.
            XCTAssertEqual(words, "Let me look. One moment.")
        } else {
            XCTFail("the words did not come first")
        }
        if case .tool = exchange.steps[1].body {} else { XCTFail("the call is not where it was made") }
        if case .said(let words) = exchange.steps[2].body {
            XCTAssertEqual(words, "There's one file.")
        } else {
            XCTFail("the words after the call went missing")
        }
        XCTAssertEqual(exchange.reply, "Let me look. One moment.There's one file.")
    }

    func testSeenCountsFramesSoARejoinKnowsWhereToStart() {
        var exchange = Exchange(said: "hello")
        exchange.apply(Frame(run: "r-7"))
        exchange.apply(Frame(delta: "hi"))
        exchange.apply(Frame(done: true))
        XCTAssertEqual(exchange.seen, 3)
        XCTAssertEqual(exchange.run, "r-7")
    }
}

// What is on disk. A transcript written before a reply could be
// anything but words still has to read back as what was on screen.

final class TranscriptShapeTests: XCTestCase {

    func testAnOlderTranscriptStillReads() throws {
        let old = #"{"id":"E621E1F8-C36C-495A-93FC-0C247A3E6E5F","said":"hello","reply":"hello yourself","done":true,"seen":3}"#
        let exchange = try JSONDecoder().decode(Exchange.self, from: Data(old.utf8))
        XCTAssertEqual(exchange.said, "hello")
        XCTAssertEqual(exchange.reply, "hello yourself")
        XCTAssertEqual(exchange.steps.count, 1)
        XCTAssertTrue(exchange.done)
    }

    func testAQuestionSurvivesBeingWrittenDown() throws {
        var exchange = Exchange(said: "go on")
        var frame = Frame()
        frame.ask = Frame.Ask(id: "call_2", name: "write", args: "{}", text: "nothing said otherwise")
        exchange.apply(frame)
        exchange.answering("call_2", Choice.always)

        let data = try JSONEncoder().encode(exchange)
        let back = try JSONDecoder().decode(Exchange.self, from: data)
        guard case .ask(let question) = back.steps.first?.body else {
            return XCTFail("the question did not survive the round trip")
        }
        XCTAssertEqual(question.state, .answered)
        XCTAssertEqual(question.choice, Choice.always)
    }
}
