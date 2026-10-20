import DeskhandClient
import Foundation

/// The two panes of the files tab.
public enum FileSide: String, Sendable, CaseIterable, Identifiable {
    /// This Mac.
    case local
    /// The selected remote computer.
    case remote

    public var id: String { rawValue }

    public var other: FileSide { self == .local ? .remote : .local }
}

/// How a pane spells paths.
public enum PathStyle: Sendable, Equatable {
    /// `C:\Users\name`: `\` separators, drive roots, case-insensitive names.
    case windows
    /// `/Users/name`.
    case posix

    /// The style of a computer by its `os`.
    public init(os: String) {
        self = os.lowercased() == "windows" ? .windows : .posix
    }

    /// Two names refer to the same item in a folder of this style.
    public func sameName(_ a: String, _ b: String) -> Bool {
        switch self {
        case .windows: a.compare(b, options: [.caseInsensitive], locale: nil) == .orderedSame
        case .posix: a == b
        }
    }
}

/// One file or folder row of a pane.
public struct FileItem: Identifiable, Sendable, Equatable, Hashable {
    public var name: String
    public var path: String
    public var isDirectory: Bool
    /// Bytes; nil for folders and packages.
    public var size: Int?
    public var modified: Date?
    /// Text of the kind column, such as `폴더` or `Windows 설치 프로그램`.
    public var kind: String
    public var isLink: Bool
    public var isHidden: Bool
    public var isReadOnly: Bool
    /// A macOS package such as an `.app`: listed and transferred as one item, never opened as a folder.
    public var isPackage: Bool
    /// Received by the preview daemon and kept in memory; nothing is on disk.
    public var isPreviewOnly: Bool

    public var id: String { path }

    public init(name: String, path: String, isDirectory: Bool, size: Int? = nil, modified: Date? = nil, kind: String? = nil,
                isLink: Bool = false, isHidden: Bool = false, isReadOnly: Bool = false, isPackage: Bool = false,
                isPreviewOnly: Bool = false) {
        self.name = name
        self.path = path
        self.isDirectory = isDirectory
        self.size = isDirectory && !isPackage ? nil : size
        self.modified = modified
        self.kind = kind ?? FileKind(name: name, isDirectory: isDirectory, isPackage: isPackage).title(for: name)
        self.isLink = isLink
        self.isHidden = isHidden
        self.isReadOnly = isReadOnly
        self.isPackage = isPackage
        self.isPreviewOnly = isPreviewOnly
    }

    /// A row for a remote `files.list` entry. The entry's `name` may be a relative path; the row
    /// shows its last component.
    public init(entry: FileEntry, style: PathStyle) {
        let name = FilePath.lastComponent(entry.name, style: style)
        self.init(name: name, path: entry.path, isDirectory: entry.dir, size: entry.size, modified: entry.mtime.date,
                  isLink: entry.link, isHidden: entry.hidden ?? (style == .posix && name.hasPrefix(".")),
                  isReadOnly: entry.readonly ?? false)
    }

    /// Opens as a folder in the pane.
    public var isFolder: Bool { isDirectory && !isPackage }

    public var fileKind: FileKind { FileKind(name: name, isDirectory: isDirectory, isPackage: isPackage) }

    /// `84.3MB`, or `—` for folders.
    public var sizeText: String { size.map(FileFormat.size) ?? "—" }

    /// `오늘 오후 3:12`, `어제 오후 6:40` or `2026. 10. 3. 오후 3:12`.
    public var modifiedText: String { modified.map { FileFormat.date($0) } ?? "—" }
}

/// Families of files with their symbol and kind text.
public enum FileKind: Sendable, Equatable {
    case folder
    case package
    case application
    /// Libraries and system binaries such as `.dll` and `.sys`.
    case library
    case installer
    case archive
    case image
    case video
    case audio
    case text
    case log
    case code
    case script
    case document
    case spreadsheet
    case pdf
    case dump
    case data
    case shortcut
    case other

    private static let families: [String: FileKind] = {
        var map: [String: FileKind] = [:]
        func add(_ kind: FileKind, _ extensions: [String]) { for e in extensions { map[e] = kind } }
        add(.application, ["exe", "app", "com", "msc"])
        add(.library, ["dll", "sys", "ocx", "so", "dylib"])
        add(.installer, ["msi", "msix", "appx", "pkg", "dmg", "iso"])
        add(.archive, ["zip", "7z", "rar", "tar", "gz", "tgz", "bz2", "xz", "cab"])
        add(.image, ["png", "jpg", "jpeg", "gif", "heic", "bmp", "webp", "svg", "ico", "tif", "tiff"])
        add(.video, ["mp4", "mov", "mkv", "avi", "wmv", "webm"])
        add(.audio, ["mp3", "wav", "m4a", "flac", "aac", "ogg"])
        add(.text, ["txt", "md", "rtf", "csv"])
        add(.log, ["log", "etl", "evtx"])
        add(.code, ["json", "xml", "html", "htm", "css", "js", "ts", "cs", "c", "cpp", "h", "swift", "go", "rs", "java",
                    "sln", "csproj", "yml", "yaml", "ini", "cfg", "conf", "config", "toml", "plist"])
        add(.script, ["ps1", "psm1", "bat", "cmd", "sh", "py"])
        add(.document, ["doc", "docx", "pptx", "ppt", "hwp", "hwpx", "pages", "key"])
        add(.spreadsheet, ["xls", "xlsx", "numbers"])
        add(.pdf, ["pdf"])
        add(.dump, ["dmp", "mdmp", "hdmp"])
        add(.data, ["dat", "bin", "db", "sqlite", "bak", "tmp", "pak"])
        add(.shortcut, ["lnk", "url"])
        return map
    }()

    private static let titles: [String: String] = [
        "exe": .ui("kind.exe", "응용 프로그램"),
        "app": .ui("kind.exe", "응용 프로그램"),
        "dll": .ui("kind.dll", "응용 프로그램 확장"),
        "sys": .ui("kind.sys", "시스템 파일"),
        "msi": .ui("kind.msi", "Windows 설치 프로그램"),
        "msix": .ui("kind.msix", "앱 패키지"),
        "appx": .ui("kind.msix", "앱 패키지"),
        "pkg": .ui("kind.pkg", "설치 패키지"),
        "dmg": .ui("kind.diskImage", "디스크 이미지"),
        "iso": .ui("kind.diskImage", "디스크 이미지"),
        "zip": .ui("kind.zip", "ZIP 압축 파일"),
        "7z": .ui("kind.7z", "7-Zip 압축 파일"),
        "png": .ui("kind.png", "PNG 이미지"),
        "jpg": .ui("kind.jpeg", "JPEG 이미지"),
        "jpeg": .ui("kind.jpeg", "JPEG 이미지"),
        "svg": .ui("kind.svg", "SVG 이미지"),
        "txt": .ui("kind.txt", "텍스트 문서"),
        "md": .ui("kind.md", "Markdown 문서"),
        "rtf": .ui("kind.rtf", "서식 있는 텍스트"),
        "csv": .ui("kind.csv", "CSV 문서"),
        "log": .ui("kind.log", "로그 파일"),
        "etl": .ui("kind.etl", "이벤트 추적 로그"),
        "evtx": .ui("kind.evtx", "Windows 이벤트 로그"),
        "json": .ui("kind.json", "JSON 문서"),
        "xml": .ui("kind.xml", "XML 문서"),
        "html": .ui("kind.html", "HTML 문서"),
        "htm": .ui("kind.html", "HTML 문서"),
        "js": .ui("kind.js", "JavaScript 소스"),
        "ts": .ui("kind.ts", "TypeScript 소스"),
        "cs": .ui("kind.cs", "C# 소스"),
        "sln": .ui("kind.sln", "Visual Studio 솔루션"),
        "csproj": .ui("kind.csproj", "C# 프로젝트"),
        "yml": .ui("kind.yaml", "YAML 문서"),
        "yaml": .ui("kind.yaml", "YAML 문서"),
        "ini": .ui("kind.config", "설정 파일"),
        "cfg": .ui("kind.config", "설정 파일"),
        "conf": .ui("kind.config", "설정 파일"),
        "config": .ui("kind.config", "설정 파일"),
        "ps1": .ui("kind.ps1", "PowerShell 스크립트"),
        "bat": .ui("kind.bat", "배치 파일"),
        "cmd": .ui("kind.bat", "배치 파일"),
        "sh": .ui("kind.sh", "셸 스크립트"),
        "py": .ui("kind.py", "Python 스크립트"),
        "pdf": .ui("kind.pdf", "PDF 문서"),
        "doc": .ui("kind.word", "Word 문서"),
        "docx": .ui("kind.word", "Word 문서"),
        "pptx": .ui("kind.powerpoint", "PowerPoint 프레젠테이션"),
        "hwp": .ui("kind.hwp", "한글 문서"),
        "hwpx": .ui("kind.hwp", "한글 문서"),
        "xls": .ui("kind.excel", "Excel 통합 문서"),
        "xlsx": .ui("kind.excel", "Excel 통합 문서"),
        "dmp": .ui("kind.dump", "크래시 덤프"),
        "mdmp": .ui("kind.dump", "크래시 덤프"),
        "dat": .ui("kind.data", "데이터 파일"),
        "bin": .ui("kind.data", "데이터 파일"),
        "db": .ui("kind.database", "데이터베이스"),
        "sqlite": .ui("kind.database", "데이터베이스"),
        "bak": .ui("kind.backup", "백업 파일"),
        "tmp": .ui("kind.temp", "임시 파일"),
        "lnk": .ui("kind.shortcut", "바로 가기"),
        "url": .ui("kind.internetShortcut", "인터넷 바로 가기"),
    ]

    public init(name: String, isDirectory: Bool, isPackage: Bool = false) {
        if isDirectory && !isPackage {
            self = .folder
            return
        }
        let ext = FileKind.pathExtension(of: name)
        if isPackage, ext != "app" {
            self = .package
            return
        }
        self = FileKind.families[ext] ?? .other
    }

    /// Lowercased extension after the last dot, or an empty string.
    static func pathExtension(of name: String) -> String {
        guard let dot = name.lastIndex(of: "."), dot != name.startIndex else { return "" }
        return String(name[name.index(after: dot)...]).lowercased()
    }

    /// Kind column text for `name`.
    public func title(for name: String) -> String {
        switch self {
        case .folder: return .ui("kind.folder", "폴더")
        case .package: return .ui("kind.package", "패키지")
        default: break
        }
        let ext = FileKind.pathExtension(of: name)
        if let title = FileKind.titles[ext] { return title }
        switch self {
        case .image: return .ui("kind.image", "이미지")
        case .video: return .ui("kind.video", "동영상")
        case .audio: return .ui("kind.audio", "오디오")
        case .archive: return .ui("kind.archive", "압축 파일")
        case .code: return .ui("kind.code", "소스 코드")
        default: break
        }
        if ext.isEmpty { return .ui("kind.document", "문서") }
        let upper = ext.uppercased()
        return .ui("kind.ext", "\(upper) 파일")
    }

    /// SF Symbol of the kind.
    public var symbol: String {
        switch self {
        case .folder: "folder.fill"
        case .package: "shippingbox"
        case .application: "square.grid.2x2.fill"
        case .library: "gearshape.2.fill"
        case .installer: "shippingbox.fill"
        case .archive: "doc.zipper"
        case .image: "photo"
        case .video: "film"
        case .audio: "music.note"
        case .text: "doc.text"
        case .log: "doc.text.magnifyingglass"
        case .code: "chevron.left.forwardslash.chevron.right"
        case .script: "terminal"
        case .document: "doc.richtext"
        case .spreadsheet: "tablecells"
        case .pdf: "doc.richtext"
        case .dump: "ladybug"
        case .data: "cylinder"
        case .shortcut: "arrow.up.forward.app"
        case .other: "doc"
        }
    }

    /// Text-like files that the logs tab can show.
    public var isReadableAsLog: Bool {
        switch self {
        case .log, .text, .code, .script, .other: true
        default: false
        }
    }
}

/// Sizes, dates, rates and durations as the files tab writes them.
public enum FileFormat {
    static let locale = Locale(identifier: "ko_KR")

    /// `84.3MB`, `512바이트`, `0바이트`.
    public static func size(_ bytes: Int) -> String {
        if bytes == 0 { return .ui("size.zero", "0바이트") }
        return Int64(bytes).formatted(.byteCount(style: .file).locale(locale))
    }

    /// `88,301,568바이트`.
    public static func exactBytes(_ bytes: Int) -> String {
        let count = bytes.formatted(.number.locale(locale))
        return .ui("size.exact", "\(count)바이트")
    }

    /// `42.1MB/s`.
    public static func rate(_ bytesPerSecond: Int) -> String {
        let value = size(bytesPerSecond)
        return .ui("rate.perSecond", "\(value)/s")
    }

    /// `38%`, rounded down.
    public static func percent(_ fraction: Double) -> String {
        (floor(fraction * 100) / 100).formatted(.percent.precision(.fractionLength(0)).locale(locale))
    }

    /// `3,000`.
    public static func count(_ value: Int) -> String {
        value.formatted(.number.locale(locale))
    }

    /// `오늘 오후 3:12`, `어제 오후 6:40`, otherwise `2026. 10. 3. 오후 3:12`.
    public static func date(_ date: Date, now: Date = .now, calendar: Calendar = .current) -> String {
        let time = date.formatted(.dateTime.hour().minute().locale(locale))
        if calendar.isDate(date, inSameDayAs: now) { return .ui("date.today", "오늘 \(time)") }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
            return .ui("date.yesterday", "어제 \(time)")
        }
        return date.formatted(.dateTime.year().month(.defaultDigits).day().hour().minute().locale(locale))
    }

    /// `약 9초`, `약 2분 5초`, `약 1시간 4분`.
    public static func remaining(seconds: Double) -> String {
        let total = max(1, Int(seconds.rounded(.up)))
        let units: Set<Duration.UnitsFormatStyle.Unit> = total >= 3600 ? [.hours, .minutes] : [.minutes, .seconds]
        let text = Duration.seconds(total).formatted(.units(allowed: units, width: .abbreviated).locale(locale))
        return .ui("eta.about", "약 \(text)")
    }
}

/// Column a pane sorts by.
public enum FileSortKey: String, Sendable, CaseIterable {
    case name
    case modified
    case size
    case kind
}

/// A name split into text and number runs for sorting the way Korean Finder lists names:
/// symbols, then numbers by value (`file 9` before `file 10`), then Hangul, Han and other
/// scripts, ignoring case, width and diacritics.
public struct NameKey: Comparable, Sendable {
    enum Part: Comparable, Sendable {
        case number(Int, digits: Int)
        /// Each character as `group << 24 | scalar`.
        case text([UInt32])

        static func < (a: Part, b: Part) -> Bool {
            switch (a, b) {
            case let (.number(m, dm), .number(n, dn)): return m != n ? m < n : dm < dn
            case let (.text(m), .text(n)): return m.lexicographicallyPrecedes(n)
            case let (.number, .text(t)): return (t.first ?? 0) >> 24 != 0
            case let (.text(t), .number): return (t.first ?? 0) >> 24 == 0
            }
        }
    }

    let parts: [Part]

    /// 0 for spaces, punctuation and symbols, 2 for Hangul, 3 for Han, 4 for other letters.
    private static func group(_ scalar: Unicode.Scalar) -> UInt32 {
        switch scalar.value {
        case 0x1100...0x11FF, 0x3130...0x318F, 0xAC00...0xD7A3: return 2
        case 0x3400...0x4DBF, 0x4E00...0x9FFF, 0xF900...0xFAFF: return 3
        default: return scalar.properties.isAlphabetic ? 4 : 0
        }
    }

    public init(_ name: String) {
        var parts: [Part] = []
        var text: [UInt32] = []
        var digits = ""
        func flushText() {
            guard !text.isEmpty else { return }
            parts.append(.text(text))
            text = []
        }
        func flushDigits() {
            guard !digits.isEmpty else { return }
            parts.append(.number(Int(digits.prefix(18)) ?? .max, digits: digits.count))
            digits = ""
        }
        let folded = name.folding(options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive], locale: nil)
        for scalar in folded.unicodeScalars {
            if scalar.value >= 0x30, scalar.value <= 0x39 {
                flushText()
                digits.unicodeScalars.append(scalar)
            } else {
                flushDigits()
                text.append(Self.group(scalar) << 24 | scalar.value)
            }
        }
        flushText()
        flushDigits()
        self.parts = parts
    }

    public static func < (a: NameKey, b: NameKey) -> Bool {
        for (x, y) in zip(a.parts, b.parts) where x != y {
            return x < y
        }
        return a.parts.count < b.parts.count
    }
}

/// A row with its name and kind keys computed once.
public struct KeyedFileItem: Sendable {
    public let item: FileItem
    let name: NameKey
    let kind: NameKey

    public init(_ item: FileItem) {
        self.item = item
        name = NameKey(item.name)
        kind = NameKey(item.kind)
    }
}

/// Sort order of a pane: folders first, then by `key`, ties broken by name.
public struct FileSort: Sendable, Equatable {
    public var key: FileSortKey
    public var ascending: Bool

    public init(key: FileSortKey = .name, ascending: Bool = true) {
        self.key = key
        self.ascending = ascending
    }

    public static let standard = FileSort()

    /// `name:asc`.
    public var storageValue: String { "\(key.rawValue):\(ascending ? "asc" : "desc")" }

    public init?(storageValue: String) {
        let parts = storageValue.split(separator: ":")
        guard parts.count == 2, let key = FileSortKey(rawValue: String(parts[0])) else { return nil }
        self.init(key: key, ascending: parts[1] == "asc")
    }

    public func sorted(_ items: [FileItem]) -> [FileItem] {
        sorted(items.map(KeyedFileItem.init)).map(\.item)
    }

    public func sorted(_ items: [KeyedFileItem]) -> [KeyedFileItem] {
        items.sorted(by: inOrder)
    }

    func inOrder(_ a: KeyedFileItem, _ b: KeyedFileItem) -> Bool {
        if a.item.isFolder != b.item.isFolder { return a.item.isFolder }
        let byName: ComparisonResult = a.name < b.name ? .orderedAscending : (b.name < a.name ? .orderedDescending : .orderedSame)
        let order: ComparisonResult
        switch key {
        case .name:
            order = byName
        case .modified:
            order = compare(a.item.modified ?? .distantPast, b.item.modified ?? .distantPast)
        case .size:
            order = compare(a.item.size ?? -1, b.item.size ?? -1)
        case .kind:
            order = a.kind < b.kind ? .orderedAscending : (b.kind < a.kind ? .orderedDescending : .orderedSame)
        }
        if order == .orderedSame {
            return byName == .orderedSame ? a.item.path < b.item.path : byName == .orderedAscending
        }
        return ascending ? order == .orderedAscending : order == .orderedDescending
    }

    private func compare<T: Comparable>(_ a: T, _ b: T) -> ComparisonResult {
        a < b ? .orderedAscending : (a > b ? .orderedDescending : .orderedSame)
    }
}
