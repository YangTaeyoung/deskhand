// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "DeskhandKit",
    defaultLocalization: "ko",
    platforms: [.macOS(.v26)],
    products: [
        .library(name: "DeskhandClient", targets: ["DeskhandClient"]),
        .library(name: "DeskhandMedia", targets: ["DeskhandMedia"]),
        .library(name: "DeskhandUI", targets: ["DeskhandUI"]),
        .executable(name: "DeskhandSnapshots", targets: ["DeskhandSnapshots"]),
    ],
    targets: [
        // Local API client for the deskhand daemon (docs/local-api.md).
        .target(name: "DeskhandClient"),
        // Video decoding and display for the remote screen stream.
        .target(name: "DeskhandMedia"),
        // Views, design system and view state shared by the app and the snapshot renderer.
        .target(
            name: "DeskhandUI",
            dependencies: ["DeskhandClient", "DeskhandMedia"],
            resources: [.process("Resources")]
        ),
        // Renders registered scenes to PNG offscreen, in light and dark mode.
        .executableTarget(name: "DeskhandSnapshots", dependencies: ["DeskhandUI"]),
        .testTarget(name: "DeskhandUITests", dependencies: ["DeskhandUI"]),
    ],
    swiftLanguageModes: [.v6]
)
