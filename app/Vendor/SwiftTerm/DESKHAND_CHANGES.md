# Deskhand's SwiftTerm copy

This directory is SwiftTerm v1.19.0 (`464df52`, MIT; see `LICENSE`).
Deskhand keeps a local copy so the terminal can enforce a per-cell text
contrast floor. The only Deskhand changes from that release are in
`Sources/SwiftTerm/Mac/MacTerminalView.swift` and
`Sources/SwiftTerm/Apple/AppleTerminalView.swift`, plus two warning-only
annotations in `Sources/SwiftTerm/Apple/Metal/MetalTerminalRenderer.swift`:

- `minimumTextContrast` opts a macOS terminal view into contrast adjustment.
- `legibleTextColor` checks the foreground against the actual rendered cell
  background, preserving its hue when already readable and blending it toward
  black or white only when needed.

Keep this patch small and check it again when updating SwiftTerm.
