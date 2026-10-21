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
    dependencies: [
        // v1.19.0 with Deskhand's per-cell contrast hook; source and MIT license live in Vendor.
        .package(path: "../Vendor/SwiftTerm"),
    ],
    targets: [
        // Local API client for the deskhand daemon (docs/local-api.md).
        .target(name: "DeskhandClient"),
        // Video decoding and display for the remote screen stream.
        .target(name: "DeskhandMedia"),
        // Views, design system and view state shared by the app and the snapshot renderer.
        .target(
            name: "DeskhandUI",
            dependencies: ["DeskhandClient", "DeskhandMedia", .product(name: "SwiftTerm", package: "SwiftTerm")],
            resources: [.process("Resources")]
        ),
        // Renders registered scenes to PNG offscreen, in light and dark mode.
        .executableTarget(name: "DeskhandSnapshots", dependencies: ["DeskhandUI", "DeskhandMedia"]),
        // In-process mock daemon shared by the client and UI tests.
        .target(name: "DeskhandTestSupport", path: "Tests/DeskhandTestSupport"),
        .testTarget(name: "DeskhandClientTests", dependencies: ["DeskhandClient", "DeskhandTestSupport"]),
        .testTarget(name: "DeskhandMediaTests", dependencies: ["DeskhandMedia"]),
        .testTarget(name: "DeskhandUITests", dependencies: ["DeskhandUI", "DeskhandMedia", "DeskhandTestSupport"]),
    ],
    swiftLanguageModes: [.v6]
)
