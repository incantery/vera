import XCTest
@testable import Vera

// The one-line summary of a call's arguments.
//
// The terminal's rule, kept: the order is the order the model wrote,
// because that order is the sentence. Anything that is not a flat
// object gives up honestly rather than guessing.

final class ArgsTests: XCTestCase {

    func testTheSummaryReadsInTheOrderItWasWritten() {
        let args = #"{"command":"git status","cwd":"/tmp","timeout":30}"#
        XCTAssertEqual(Args.summary(args), "command=git status cwd=/tmp timeout=30")
    }

    func testNestedValuesAreNotWorthALine() {
        let args = #"{"path":"/tmp/x","edits":[{"old":"a","new":"b"}],"opts":{"deep":true}}"#
        XCTAssertEqual(Args.summary(args), "path=/tmp/x edits=[…] opts={…}")
    }

    // A brace inside a string is not the end of the object.
    func testABraceInAValueDoesNotEndIt() {
        let args = #"{"command":"awk '{print $1}'","n":2}"#
        XCTAssertEqual(Args.summary(args), "command=awk '{print $1}' n=2")
    }

    func testNewlinesBecomeOneLine() {
        let args = #"{"text":"one\ntwo   three"}"#
        XCTAssertEqual(Args.summary(args), "text=one two three")
    }

    func testALongValueIsCutRatherThanWrapped() {
        let args = "{\"text\":\"\(String(repeating: "x", count: 200))\"}"
        let summary = Args.summary(args)
        XCTAssertTrue(summary.hasSuffix("…"))
        XCTAssertLessThan(summary.count, 40)
    }

    func testNoArgumentsIsNoLine() {
        XCTAssertEqual(Args.summary("{}"), "")
        XCTAssertEqual(Args.summary(""), "")
        XCTAssertEqual(Args.summary("   "), "")
    }

    // Not every tool is called with an object, and a summary is never
    // worth throwing away the text over.
    func testSomethingThatIsNotAnObjectFallsBackToItsOwnText() {
        XCTAssertEqual(Args.summary("[1, 2, 3]"), "[1, 2, 3]")
        XCTAssertEqual(Args.summary("not json at all"), "not json at all")
    }

    func testFieldsKeepTheirWholeValueForTheOpenedRow() {
        let fields = Args.fields(#"{"path":"/tmp/x","text":"one\ntwo"}"#)
        XCTAssertEqual(fields?.count, 2)
        XCTAssertEqual(fields?.first?.key, "path")
        XCTAssertEqual(fields?.last?.value, "one\ntwo")
    }

    func testFieldsGiveUpOnWhatIsNotAnObject() {
        XCTAssertNil(Args.fields("[1,2,3]"))
        XCTAssertNil(Args.fields("{\"broken\": "))
    }
}
