import DeskhandClient
import Foundation
import Synchronization
import SwiftUI

/// A visible part of one source line: up to `width` UTF-16 units around a column, with how many
/// units are hidden on each side.
public struct LineWindow: Equatable, Sendable {
    public var text: String
    public var hiddenBefore: Int
    public var hiddenAfter: Int
    /// The column inside `text`, from 1.
    public var column: Int
}

/// A source line window with its colors.
public struct HighlightedLine: Equatable, Sendable {
    public var window: LineWindow
    public var text: AttributedString
}

/// A script's text split once into lines at the ECMAScript line terminators (LF, CR, CRLF,
/// U+2028, U+2029), with colored line windows cached per line and window start.
public final class SourceText: Sendable {
    /// Units of a line shown at once; longer lines show a window around the column.
    public static let windowWidth = 2000

    public let lines: [String]
    private struct Cache {
        var highlighted: [Int: [Int: HighlightedLine]] = [:]
        /// Whether line N (index) starts inside a block comment, for lines scanned so far.
        var startsInComment: [Bool] = [false]
    }

    private let cache = Mutex(Cache())

    public init(text: String) {
        lines = Self.split(text)
    }

    /// `text` split at LF, CR, CRLF, U+2028 and U+2029.
    static func split(_ text: String) -> [String] {
        var lines: [String] = []
        let bytes = Array(text.utf8)
        var start = 0
        var i = 0
        func cut(_ end: Int, skip: Int) {
            lines.append(String(decoding: bytes[start..<end], as: UTF8.self))
            i = end + skip
            start = i
        }
        while i < bytes.count {
            switch bytes[i] {
            case 0x0A:
                cut(i, skip: 1)
            case 0x0D:
                cut(i, skip: i + 1 < bytes.count && bytes[i + 1] == 0x0A ? 2 : 1)
            case 0xE2 where i + 2 < bytes.count && bytes[i + 1] == 0x80 && (bytes[i + 2] == 0xA8 || bytes[i + 2] == 0xA9):
                cut(i, skip: 3)
            default:
                i += 1
            }
        }
        lines.append(String(decoding: bytes[start..<bytes.count], as: UTF8.self))
        return lines
    }

    /// The part of `line` to show around `column` (from 1, UTF-16): the whole line when it fits
    /// in `width`, otherwise `width` units with the column near the middle.
    public static func window(_ line: String, around column: Int, width: Int = windowWidth) -> LineWindow {
        let units = line.utf16
        let count = units.count
        guard count > width else {
            return LineWindow(text: line, hiddenBefore: 0, hiddenAfter: 0, column: max(1, column))
        }
        let start = min(max(0, column - width / 2), count - width)
        let lower = units.index(units.startIndex, offsetBy: start)
        let upper = units.index(lower, offsetBy: width)
        let text = String(decoding: Array(units[lower..<upper]), as: UTF16.self)
        return LineWindow(text: text, hiddenBefore: start, hiddenAfter: count - start - width, column: max(1, column - start))
    }

    /// Line `number` (from 1) around `column`, colored; a window that starts mid-line is read as code.
    public func highlightedWindow(line number: Int, around column: Int, width: Int = windowWidth) -> HighlightedLine {
        guard lines.indices.contains(number - 1) else {
            return HighlightedLine(window: LineWindow(text: "", hiddenBefore: 0, hiddenAfter: 0, column: 1), text: AttributedString())
        }
        let line = lines[number - 1]
        let window = Self.window(line, around: column, width: width)
        if let cached = cache.withLock({ $0.highlighted[number]?[window.hiddenBefore] }) { return cached }
        let inComment = window.hiddenBefore == 0 ? startsInComment(number) : false
        let runs = JavaScriptHighlighter.tokens(window.text, inComment: inComment).runs
        let highlighted = HighlightedLine(window: window, text: JavaScriptHighlighter.attributed(runs))
        cache.withLock { $0.highlighted[number, default: [:]][window.hiddenBefore] = highlighted }
        return highlighted
    }

    /// Whether line `number` (from 1) starts inside a block comment.
    func startsInComment(_ number: Int) -> Bool {
        let known = cache.withLock { $0.startsInComment }
        if number - 1 < known.count { return known[number - 1] }
        var states = known
        while states.count < number {
            let index = states.count - 1
            let line = lines[index]
            let ends = line.utf16.count > Self.windowWidth * 4
                ? false
                : JavaScriptHighlighter.tokens(line, inComment: states[index]).endsInComment
            states.append(ends)
        }
        let result = states[number - 1]
        cache.withLock { cache in
            if states.count > cache.startsInComment.count { cache.startsInComment = states }
        }
        return result
    }
}

/// Colors JavaScript keywords, strings, template literals, regular expressions, numbers and
/// comments, carrying block comments from line to line.
public enum JavaScriptHighlighter {
    static let keywords: Set<String> = [
        "async", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do", "else",
        "export", "extends", "false", "finally", "for", "from", "function", "if", "import", "in", "instanceof", "let", "new", "null",
        "of", "return", "static", "super", "switch", "this", "throw", "true", "try", "typeof", "undefined", "var", "void", "while",
        "with", "yield",
    ]

    /// Keywords after which `/` starts a regular expression.
    static let regexKeywords: Set<String> = ["return", "typeof", "case", "do", "else", "in", "of", "new", "delete", "void", "throw",
                                             "yield", "await", "instanceof"]

    public enum Token: Equatable, Sendable {
        case plain
        case keyword
        case string
        case regex
        case number
        case comment
    }

    public typealias Run = (text: String, token: Token)

    /// The line split into runs and their token kinds.
    public static func tokens(_ line: String) -> [Run] {
        tokens(line, inComment: false).runs
    }

    /// The line's runs when it starts inside a block comment or not, and whether it ends inside one.
    public static func tokens(_ line: String, inComment: Bool) -> (runs: [Run], endsInComment: Bool) {
        var runs: [Run] = []
        var plain = ""
        let chars = Array(line)
        var i = 0
        /// Whether a `/` here would start a regular expression rather than divide.
        var regexAllowed = true
        func flush() {
            if !plain.isEmpty { runs.append((plain, .plain)); plain = "" }
        }
        func closeComment(from start: Int) -> Int? {
            var j = start
            while j + 1 < chars.count {
                if chars[j] == "*", chars[j + 1] == "/" { return j + 2 }
                j += 1
            }
            return nil
        }
        if inComment {
            guard let end = closeComment(from: 0) else { return ([(line, .comment)], true) }
            runs.append((String(chars[0..<end]), .comment))
            i = end
        }
        while i < chars.count {
            let c = chars[i]
            if c == "/", i + 1 < chars.count, chars[i + 1] == "/" {
                flush()
                runs.append((String(chars[i...]), .comment))
                return (runs, false)
            }
            if c == "/", i + 1 < chars.count, chars[i + 1] == "*" {
                flush()
                guard let end = closeComment(from: i + 2) else {
                    runs.append((String(chars[i...]), .comment))
                    return (runs, true)
                }
                runs.append((String(chars[i..<end]), .comment))
                i = end
                continue
            }
            if c == "/", regexAllowed, let end = regexEnd(chars, from: i) {
                flush()
                runs.append((String(chars[i..<end]), .regex))
                i = end
                regexAllowed = false
                continue
            }
            if c == "\"" || c == "'" || c == "`" {
                flush()
                var j = i + 1
                while j < chars.count, chars[j] != c {
                    if chars[j] == "\\" { j += 1 }
                    j += 1
                }
                let end = min(j, chars.count - 1)
                runs.append((String(chars[i...end]), .string))
                i = end + 1
                regexAllowed = false
                continue
            }
            if c.isNumber, c.isASCII, i == 0 || !isIdentifier(chars[i - 1]) {
                flush()
                let end = numberEnd(chars, from: i)
                runs.append((String(chars[i..<end]), .number))
                i = end
                regexAllowed = false
                continue
            }
            if isIdentifier(c) {
                var j = i
                while j < chars.count, isIdentifier(chars[j]) { j += 1 }
                let word = String(chars[i..<j])
                if keywords.contains(word), i == 0 || chars[i - 1] != "." {
                    flush()
                    runs.append((word, .keyword))
                    regexAllowed = regexKeywords.contains(word)
                } else {
                    plain += word
                    regexAllowed = false
                }
                i = j
                continue
            }
            plain.append(c)
            if !c.isWhitespace { regexAllowed = !")]}".contains(c) }
            i += 1
        }
        flush()
        return (runs, false)
    }

    static func isIdentifier(_ c: Character) -> Bool {
        c.isLetter || c.isNumber || c == "_" || c == "$"
    }

    /// The end of a regular expression literal starting at `start`, flags included, or nil when
    /// the line ends first.
    static func regexEnd(_ chars: [Character], from start: Int) -> Int? {
        var j = start + 1
        var inClass = false
        while j < chars.count {
            let c = chars[j]
            if c == "\\" {
                j += 2
                continue
            }
            if c == "[" { inClass = true } else if c == "]" { inClass = false } else if c == "/", !inClass {
                j += 1
                while j < chars.count, chars[j].isLetter { j += 1 }
                return j
            }
            j += 1
        }
        return nil
    }

    /// The end of a number literal: hex, binary or octal, decimals with `_`, a fraction, an exponent, a bigint `n`.
    static func numberEnd(_ chars: [Character], from start: Int) -> Int {
        var j = start
        if chars[j] == "0", j + 1 < chars.count, "xXbBoO".contains(chars[j + 1]) {
            j += 2
            while j < chars.count, chars[j].isHexDigit || chars[j] == "_" { j += 1 }
        } else {
            while j < chars.count, chars[j].isNumber || chars[j] == "_" { j += 1 }
            if j < chars.count, chars[j] == ".", j + 1 < chars.count, chars[j + 1].isNumber {
                j += 1
                while j < chars.count, chars[j].isNumber || chars[j] == "_" { j += 1 }
            }
            if j < chars.count, "eE".contains(chars[j]) {
                var k = j + 1
                if k < chars.count, "+-".contains(chars[k]) { k += 1 }
                if k < chars.count, chars[k].isNumber {
                    j = k
                    while j < chars.count, chars[j].isNumber { j += 1 }
                }
            }
        }
        if j < chars.count, chars[j] == "n" { j += 1 }
        return j
    }

    static func attributed(_ runs: [Run]) -> AttributedString {
        var result = AttributedString()
        for run in runs {
            var piece = AttributedString(run.text)
            switch run.token {
            case .plain: piece.foregroundColor = .primary
            case .keyword: piece.foregroundColor = DS.Code.keyword
            case .string, .regex: piece.foregroundColor = DS.Code.string
            case .number: piece.foregroundColor = DS.Code.number
            case .comment: piece.foregroundColor = DS.Code.comment
            }
            result += piece
        }
        return result
    }

    public static func highlight(_ line: String) -> AttributedString {
        attributed(tokens(line))
    }
}
