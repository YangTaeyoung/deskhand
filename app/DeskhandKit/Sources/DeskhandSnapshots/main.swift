import AppKit
import DeskhandUI
import SwiftUI

/// Every scene, in output order. Each renders as `<name>-light.png` and `<name>-dark.png`.
let scenes: [SnapshotScene] = [
    SnapshotScene(name: "components-gallery", size: CGSize(width: 1440, height: 1024)) {
        ComponentsGallery()
    },
] + RootScenes.all + WorkspaceScenes.all + ScreenScenes.all + FilesScenes.all + TerminalScenes.all + DiagnosticsScenes.all

let usage = """
    사용법: DeskhandSnapshots --out <디렉터리> [--only <장면>]
           DeskhandSnapshots --list
    """

struct Options {
    var outputDirectory: URL?
    var only: String?
    var list = false
}

func parseOptions(_ arguments: [String]) -> Options? {
    var options = Options()
    var remaining = arguments[...]
    while let argument = remaining.popFirst() {
        switch argument {
        case "--out":
            guard let path = remaining.popFirst() else { return nil }
            options.outputDirectory = URL(fileURLWithPath: path, isDirectory: true).standardizedFileURL
        case "--only":
            guard let name = remaining.popFirst() else { return nil }
            options.only = name
        case "--list":
            options.list = true
        default:
            return nil
        }
    }
    return options
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}

guard let options = parseOptions(Array(CommandLine.arguments.dropFirst())) else {
    FileHandle.standardError.write(Data((usage + "\n").utf8))
    exit(2)
}

if options.list {
    for scene in scenes {
        print("\(scene.name)\t\(Int(scene.size.width))×\(Int(scene.size.height))")
    }
    exit(0)
}

guard let outputDirectory = options.outputDirectory else {
    FileHandle.standardError.write(Data((usage + "\n").utf8))
    exit(2)
}

let selected = scenes.filter { options.only == nil || $0.name == options.only }
if selected.isEmpty {
    fail("장면 '\(options.only ?? "")'이(가) 없습니다. 등록된 장면: \(scenes.map(\.name).joined(separator: ", "))")
}

NSApplication.shared.setActivationPolicy(.accessory)

do {
    try FileManager.default.createDirectory(at: outputDirectory, withIntermediateDirectories: true)
    for scene in selected {
        for appearance in SnapshotAppearance.allCases {
            let png = try SnapshotRenderer.pngData(for: scene, appearance: appearance)
            let file = outputDirectory.appendingPathComponent("\(scene.name)-\(appearance.rawValue).png")
            try png.write(to: file, options: .atomic)
            print(file.path)
        }
    }
} catch {
    fail("스냅샷 렌더링 실패: \(error)")
}
