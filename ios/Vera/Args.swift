import Foundation

// A tool call's arguments, small enough to read on a phone.
//
// The terminal's rule, kept: the summary reads in the order the model
// wrote the call, because that order is the sentence — `command=…` for
// a shell, `path=…` for a file. JSONSerialization hands back a
// dictionary, which has no order at all, so this walks the text
// instead. It is a scanner for one shape — a flat object — and it
// gives up honestly on anything else rather than guessing.

enum Args {

    /// One line: `path=/tmp/x text=hello`. Values that are themselves
    /// objects or lists are not worth a line, so they say so.
    static func summary(_ json: String) -> String {
        let trimmed = json.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return "" }
        guard let pairs = fields(trimmed) else { return oneLine(trimmed) }
        guard !pairs.isEmpty else { return "" }
        return pairs.map { "\($0.key)=\(short($0.value))" }.joined(separator: " ")
    }

    /// The key/value pairs of a flat JSON object, in the order they
    /// were written. Nil if the text is not an object at all.
    static func fields(_ json: String) -> [(key: String, value: String)]? {
        var scanner = Scan(json)
        scanner.skipSpace()
        guard scanner.take("{") else { return nil }
        var out: [(key: String, value: String)] = []
        scanner.skipSpace()
        if scanner.take("}") { return out }
        while true {
            scanner.skipSpace()
            guard let key = scanner.string() else { return nil }
            scanner.skipSpace()
            guard scanner.take(":") else { return nil }
            scanner.skipSpace()
            guard let value = scanner.value() else { return nil }
            out.append((key, value))
            scanner.skipSpace()
            if scanner.take(",") { continue }
            if scanner.take("}") { return out }
            return nil
        }
    }

    /// A value, short enough to sit on a line with three others.
    static func short(_ value: String, limit: Int = 28) -> String {
        let text = oneLine(value)
        guard text.count > limit else { return text }
        return String(text.prefix(limit)) + "…"
    }

    /// Newlines and runs of spaces are what turn one argument into five
    /// lines. There is no room for either.
    static func oneLine(_ text: String) -> String {
        text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }
}

// MARK: - The scanner

/// Just enough JSON to walk a flat object without decoding it. It
/// never throws: every question it is asked has "no" as an answer.
private struct Scan {
    private let text: [Character]
    private var at = 0

    init(_ text: String) { self.text = Array(text) }

    private var here: Character? { at < text.count ? text[at] : nil }

    mutating func skipSpace() {
        while let c = here, c.isWhitespace { at += 1 }
    }

    mutating func take(_ c: Character) -> Bool {
        guard here == c else { return false }
        at += 1
        return true
    }

    /// A JSON string, unescaped enough to read: the escapes that appear
    /// in a tool's arguments are quotes, slashes and newlines.
    mutating func string() -> String? {
        guard take("\"") else { return nil }
        var out = ""
        while let c = here {
            at += 1
            if c == "\"" { return out }
            guard c == "\\" else {
                out.append(c)
                continue
            }
            guard let escaped = here else { return nil }
            at += 1
            switch escaped {
            case "n": out.append("\n")
            case "t": out.append("\t")
            case "r": out.append("\r")
            case "u":
                // Not worth decoding for a one-line summary; the point
                // is to get past it without losing the rest.
                at = min(at + 4, text.count)
                out.append("\u{FFFD}")
            default: out.append(escaped)
            }
        }
        return nil
    }

    /// Any value. Objects and lists are stepped over and reported as
    /// what they are — nobody reads `{…}` on one line anyway.
    mutating func value() -> String? {
        switch here {
        case "\"":
            return string()
        case "{":
            return skipNested("{", "}") ? "{…}" : nil
        case "[":
            return skipNested("[", "]") ? "[…]" : nil
        case nil:
            return nil
        default:
            var out = ""
            while let c = here, c != ",", c != "}", !c.isWhitespace {
                out.append(c)
                at += 1
            }
            return out.isEmpty ? nil : out
        }
    }

    /// Step over a bracketed value, counting depth and ignoring
    /// anything inside a string — a `}` in a path is not the end.
    private mutating func skipNested(_ open: Character, _ close: Character) -> Bool {
        guard take(open) else { return false }
        var depth = 1
        while let c = here {
            if c == "\"" {
                guard string() != nil else { return false }
                continue
            }
            at += 1
            if c == open { depth += 1 }
            if c == close {
                depth -= 1
                if depth == 0 { return true }
            }
        }
        return false
    }
}
