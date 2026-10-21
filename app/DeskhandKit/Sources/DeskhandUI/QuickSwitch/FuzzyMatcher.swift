import Foundation

/// Fuzzy matching of a typed query against a title in Korean and English.
///
/// Hangul is compared by its letters: a lone consonant in the query (`ㅌㅁㄴ`) matches the
/// initial consonant of a syllable, a syllable matches a syllable with the same initial and a
/// vowel that starts the same way (`고` finds `과`), and a final consonant still being typed may
/// be the next syllable's initial (`텀` and `터민` find `터미널`). Latin letters match without case.
/// Spaces in the query are ignored. Query and candidate are compared in composed form (NFC),
/// so decomposed Hangul such as macOS file names matches too.
///
/// The score rewards matches at the start, at word starts and in runs, and costs gaps, letters
/// before the first match and unmatched letters: `파일` ranks `파일` over `파일 전송 큐 보기`
/// over `원격 파일 보기` over `파란 일기`.
public enum FuzzyMatcher {
    public struct Match: Equatable, Sendable {
        public var score: Int
        /// Offsets of the matched characters of the candidate, ascending.
        public var indices: [Int]

        public init(score: Int, indices: [Int]) {
            self.score = score
            self.indices = indices
        }
    }

    enum Score {
        static let match = 16
        static let boundary = 10
        static let firstCharacterMultiplier = 2
        static let consecutive = 4
        static let prefix = 4
        static let gapOpen = -10
        static let gapExtension = -1
        static let leadingLimit = 3
        static let exact = 50
    }

    public static func match(_ query: String, in candidate: String) -> Match? {
        match(query, in: FuzzyCandidate(candidate))
    }

    /// `match(_:in:)` against a candidate whose letters were prepared once.
    public static func match(_ query: String, in candidate: FuzzyCandidate) -> Match? {
        let atoms = queryAtoms(query)
        guard !atoms.isEmpty else { return Match(score: 0, indices: []) }
        let letters = candidate.letters
        guard !letters.isEmpty else { return nil }
        var best: Match?
        for start in letters.indices {
            guard let first = consume(atoms[0], in: letters, at: start) else { continue }
            var spans = [first]
            var next = first.next
            var matched = true
            for atom in atoms.dropFirst() {
                guard let span = find(atom, in: letters, from: next) else {
                    matched = false
                    break
                }
                spans.append(span)
                next = span.next
            }
            guard matched else { break }
            let indices = spans.flatMap(\.indices)
            let match = Match(score: score(indices, letters: letters, exact: isExact(query, candidate.folded)), indices: indices)
            if best.map({ match.score > $0.score }) ?? true { best = match }
        }
        return best
    }

    // MARK: Scoring

    private static func score(_ indices: [Int], letters: [Letter], exact: Bool) -> Int {
        var total = 0
        var previous: Int?
        var chunkBonus = 0
        for index in indices {
            let bonus = boundaryBonus(at: index, letters: letters)
            if let previous, index == previous + 1 {
                total += Score.match + max(bonus, chunkBonus, Score.consecutive)
            } else {
                if let previous {
                    total += Score.gapOpen + Score.gapExtension * (index - previous - 2)
                }
                chunkBonus = bonus
                total += Score.match + (previous == nil ? bonus * Score.firstCharacterMultiplier : bonus)
            }
            previous = index
        }
        if let first = indices.first {
            total += first == 0 ? Score.prefix : -min(first, Score.leadingLimit)
        }
        total -= (letters.count - indices.count) / 2
        if exact { total += Score.exact }
        return total
    }

    /// Word starts: the first character, a character after a space or punctuation, an upper-case
    /// letter after a lower-case one, and a change between Hangul and other characters.
    private static func boundaryBonus(at index: Int, letters: [Letter]) -> Int {
        guard index > 0 else { return Score.boundary }
        let previous = letters[index - 1].character
        let current = letters[index].character
        if previous.isWhitespace || previous.isPunctuation || previous.isSymbol { return Score.boundary }
        if previous.isLowercase, current.isUppercase { return Score.boundary }
        if (letters[index - 1].syllable == nil) != (letters[index].syllable == nil), !current.isWhitespace { return Score.boundary }
        return 0
    }

    private static func isExact(_ query: String, _ folded: String) -> Bool {
        FuzzyCandidate.fold(query) == folded
    }

    // MARK: Matching

    /// Where an atom matched: the candidate characters it covers and the first character the
    /// next atom may use.
    private struct Span {
        let indices: [Int]
        let next: Int
    }

    private static func find(_ atom: Atom, in letters: [Letter], from start: Int) -> Span? {
        var index = start
        while index < letters.count {
            if let span = consume(atom, in: letters, at: index) { return span }
            index += 1
        }
        return nil
    }

    private static func consume(_ atom: Atom, in letters: [Letter], at index: Int) -> Span? {
        guard letters.indices.contains(index) else { return nil }
        let letter = letters[index]
        switch atom {
        case .character(let character):
            return letter.folded == character ? Span(indices: [index], next: index + 1) : nil
        case .consonant(let jamo):
            if let syllable = letter.syllable, syllable.initial == jamo { return Span(indices: [index], next: index + 1) }
            return letter.folded == jamo ? Span(indices: [index], next: index + 1) : nil
        case .vowel(let jamo):
            if let syllable = letter.syllable, syllable.medials.first == jamo { return Span(indices: [index], next: index + 1) }
            return letter.folded == jamo ? Span(indices: [index], next: index + 1) : nil
        case .syllable(let query):
            guard let syllable = letter.syllable, syllable.initial == query.initial,
                  syllable.medials.starts(with: query.medials) else { return nil }
            if query.finals.isEmpty { return Span(indices: [index], next: index + 1) }
            guard syllable.medials == query.medials else { return nil }
            if syllable.finals.starts(with: query.finals) { return Span(indices: [index], next: index + 1) }
            guard query.finals.starts(with: syllable.finals), query.finals.count == syllable.finals.count + 1,
                  letters.indices.contains(index + 1), let following = letters[index + 1].syllable,
                  following.initial == query.finals.last else { return nil }
            return Span(indices: [index, index + 1], next: index + 2)
        }
    }

    // MARK: Letters

    /// A query unit: a syllable, a lone consonant or vowel, or any other character folded to lower case.
    private enum Atom {
        case syllable(Jamo.Syllable)
        case consonant(Character)
        case vowel(Character)
        case character(Character)
    }

    private typealias Letter = FuzzyCandidate.Letter

    /// The query's atoms. A double consonant the input method joined from two typed ones
    /// (`ㄱ`+`ㅅ` → `ㄳ`) counts as both.
    private static func queryAtoms(_ query: String) -> [Atom] {
        query.precomposedStringWithCanonicalMapping.flatMap { character -> [Atom] in
            if character.isWhitespace { return [] }
            if let syllable = Jamo.syllable(character) { return [.syllable(syllable)] }
            if let parts = Jamo.joinedConsonants[character] { return parts.map(Atom.consonant) }
            if Jamo.consonants.contains(character) { return [.consonant(character)] }
            if Jamo.vowels.contains(character) { return [.vowel(character)] }
            return [.character(Character(String(character).lowercased()))]
        }
    }
}

/// A text prepared for matching: each character in composed form with its lower-case form and
/// its Hangul letters. Offsets of its letters are offsets of the text's characters.
public struct FuzzyCandidate: Equatable, Sendable {
    struct Letter: Equatable, Sendable {
        let character: Character
        let folded: Character
        let syllable: Jamo.Syllable?

        init(_ character: Character) {
            let composed = Character(String(character).precomposedStringWithCanonicalMapping)
            self.character = composed
            folded = Character(String(composed).lowercased())
            syllable = Jamo.syllable(composed)
        }
    }

    public let text: String
    let letters: [Letter]
    /// The text without spaces, composed and in lower case.
    let folded: String

    public init(_ text: String) {
        self.text = text
        letters = text.map(Letter.init)
        folded = Self.fold(text)
    }

    static func fold(_ text: String) -> String {
        text.precomposedStringWithCanonicalMapping.filter { !$0.isWhitespace }.lowercased()
    }
}

/// Hangul syllables split into compatibility jamo, with compound vowels and final consonants
/// split into their parts (`ㅘ` → `ㅗㅏ`, `ㄺ` → `ㄹㄱ`).
enum Jamo {
    struct Syllable: Equatable, Sendable {
        let initial: Character
        let medials: [Character]
        let finals: [Character]
    }

    static let initials: [Character] = Array("ㄱㄲㄴㄷㄸㄹㅁㅂㅃㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎ")
    static let medials: [[Character]] = [
        ["ㅏ"], ["ㅐ"], ["ㅑ"], ["ㅒ"], ["ㅓ"], ["ㅔ"], ["ㅕ"], ["ㅖ"], ["ㅗ"], ["ㅗ", "ㅏ"], ["ㅗ", "ㅐ"], ["ㅗ", "ㅣ"],
        ["ㅛ"], ["ㅜ"], ["ㅜ", "ㅓ"], ["ㅜ", "ㅔ"], ["ㅜ", "ㅣ"], ["ㅠ"], ["ㅡ"], ["ㅡ", "ㅣ"], ["ㅣ"],
    ]
    static let finals: [[Character]] = [
        [], ["ㄱ"], ["ㄲ"], ["ㄱ", "ㅅ"], ["ㄴ"], ["ㄴ", "ㅈ"], ["ㄴ", "ㅎ"], ["ㄷ"], ["ㄹ"], ["ㄹ", "ㄱ"], ["ㄹ", "ㅁ"],
        ["ㄹ", "ㅂ"], ["ㄹ", "ㅅ"], ["ㄹ", "ㅌ"], ["ㄹ", "ㅍ"], ["ㄹ", "ㅎ"], ["ㅁ"], ["ㅂ"], ["ㅂ", "ㅅ"], ["ㅅ"], ["ㅆ"],
        ["ㅇ"], ["ㅈ"], ["ㅊ"], ["ㅋ"], ["ㅌ"], ["ㅍ"], ["ㅎ"],
    ]
    /// Compatibility consonants a person types alone (U+3131–U+314E).
    static let consonants: Set<Character> = Set("ㄱㄲㄳㄴㄵㄶㄷㄸㄹㄺㄻㄼㄽㄾㄿㅀㅁㅂㅃㅄㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎ")
    /// Consonants the input method joins from two typed consonants, and their parts.
    static let joinedConsonants: [Character: [Character]] = [
        "ㄳ": ["ㄱ", "ㅅ"], "ㄵ": ["ㄴ", "ㅈ"], "ㄶ": ["ㄴ", "ㅎ"], "ㄺ": ["ㄹ", "ㄱ"], "ㄻ": ["ㄹ", "ㅁ"], "ㄼ": ["ㄹ", "ㅂ"],
        "ㄽ": ["ㄹ", "ㅅ"], "ㄾ": ["ㄹ", "ㅌ"], "ㄿ": ["ㄹ", "ㅍ"], "ㅀ": ["ㄹ", "ㅎ"], "ㅄ": ["ㅂ", "ㅅ"],
    ]
    /// Compatibility vowels (U+314F–U+3163).
    static let vowels: Set<Character> = Set("ㅏㅐㅑㅒㅓㅔㅕㅖㅗㅘㅙㅚㅛㅜㅝㅞㅟㅠㅡㅢㅣ")

    static func syllable(_ character: Character) -> Syllable? {
        guard let scalar = character.unicodeScalars.first, character.unicodeScalars.count == 1,
              (0xAC00...0xD7A3).contains(scalar.value) else { return nil }
        let index = Int(scalar.value) - 0xAC00
        return Syllable(initial: initials[index / 588], medials: medials[(index % 588) / 28], finals: finals[index % 28])
    }
}
