import XCTest
@testable import Vera

// The wire, exactly as verad writes it.
//
// These are ndjson lines copied from the shape cmd/verad/transport.go
// marshals: snake_case keys, a tool round split across three frames,
// and a question that is not a terminal frame. A frame the phone
// cannot decode is dropped silently by the reader, and the one frame
// that must never be dropped is the question — so every one of them is
// decoded here rather than trusted.

final class FrameTests: XCTestCase {

    private func decode(_ line: String) throws -> Frame {
        try JSONDecoder().decode(Frame.self, from: Data(line.utf8))
    }

    func testWordsAndTheEnd() throws {
        XCTAssertEqual(try decode(#"{"delta":"hello"}"#).delta, "hello")
        XCTAssertEqual(try decode(#"{"status":"Reading the file…"}"#).status, "Reading the file…")
        XCTAssertEqual(try decode(#"{"run":"r-7"}"#).run, "r-7")
        XCTAssertEqual(try decode(#"{"done":true}"#).done, true)
        XCTAssertEqual(try decode(#"{"error":"it fell over"}"#).error, "it fell over")
    }

    func testAToolCall() throws {
        let frame = try decode(#"{"tool_call":{"id":"call_1","name":"bash","args":"{\"command\":\"ls\"}"}}"#)
        XCTAssertEqual(frame.toolCall, Frame.ToolCall(id: "call_1", name: "bash", args: #"{"command":"ls"}"#))
    }

    func testWhatATooIsPrintingWhileItRuns() throws {
        let frame = try decode(#"{"tool_output":{"id":"call_1","text":"one\ntwo\n"}}"#)
        XCTAssertEqual(frame.toolOutput, Frame.ToolOutput(id: "call_1", text: "one\ntwo\n"))
    }

    func testAToolResultCarriesWhatItTookAndCost() throws {
        let frame = try decode(#"{"tool_result":{"id":"call_1","result":"one\ntwo","duration_ms":1200,"cost_usd":0.0042}}"#)
        XCTAssertEqual(frame.toolResult?.id, "call_1")
        XCTAssertEqual(frame.toolResult?.result, "one\ntwo")
        XCTAssertEqual(frame.toolResult?.durationMs, 1200)
        XCTAssertEqual(frame.toolResult?.costUSD ?? 0, 0.0042, accuracy: 0.000001)
    }

    // cost_usd is the one field verad leaves out when there is nothing
    // to say, so its absence must not take the frame with it.
    func testAFreeToolStillDecodes() throws {
        let frame = try decode(#"{"tool_result":{"id":"call_1","result":"","duration_ms":3}}"#)
        XCTAssertNil(frame.toolResult?.costUSD)
        XCTAssertEqual(frame.toolResult?.durationMs, 3)
    }

    func testAQuestion() throws {
        let frame = try decode(#"{"ask":{"id":"call_2","name":"write","args":"{\"path\":\"/tmp/x\"}","text":"nothing said otherwise"}}"#)
        let ask = try XCTUnwrap(frame.ask)
        XCTAssertEqual(ask.id, "call_2")
        XCTAssertEqual(ask.name, "write")
        XCTAssertEqual(ask.args, #"{"path":"/tmp/x"}"#)
        XCTAssertEqual(ask.text, "nothing said otherwise")
        // It does not end the exchange: the exchange is parked on it.
        XCTAssertNil(frame.done)
        XCTAssertNil(frame.error)
    }

    // The Mac may learn to say more than the phone knows. It has done
    // that once already, which is what this whole change is about.
    func testAFrameFromALaterMacStillReads() throws {
        let frame = try decode(#"{"delta":"hi","thinking":{"text":"…"},"tokens":42}"#)
        XCTAssertEqual(frame.delta, "hi")
    }
}
