// make-icon.swift — draws the Deskhand app icon with Core Graphics.
//
// The mark is a white monitor on an indigo-to-purple squircle with a white pointing hand
// reaching into the screen from the lower right. Every size is drawn from the same vector
// geometry, so small sizes stay sharp.
//
//   swift scripts/make-icon.swift            # writes app/Deskhand/Resources/Assets.xcassets/AppIcon.appiconset
//   swift scripts/make-icon.swift <outdir>   # writes the icon set into <outdir>

import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

// MARK: - Colors

let sRGB = CGColorSpace(name: CGColorSpace.sRGB)!

/// 0xRRGGBB in sRGB with optional alpha.
func srgb(_ hex: UInt32, _ alpha: CGFloat = 1) -> CGColor {
    CGColor(
        colorSpace: sRGB,
        components: [
            CGFloat((hex >> 16) & 0xFF) / 255,
            CGFloat((hex >> 8) & 0xFF) / 255,
            CGFloat(hex & 0xFF) / 255,
            alpha,
        ]
    )!
}

func gradient(_ stops: [(CGColor, CGFloat)]) -> CGGradient {
    CGGradient(colorsSpace: sRGB, colors: stops.map(\.0) as CFArray, locations: stops.map(\.1))!
}

enum Palette {
    static let indigo = srgb(0x5B5BF0)
    static let violet = srgb(0x7B4CEB)
    static let purple = srgb(0xB04AE2)
    static let screenTop = srgb(0x3A2E9E)
    static let screenBottom = srgb(0x5A2A9C)
    static let outline = srgb(0x6E45E6)
    static let crease = srgb(0x6E45E6, 0.45)
    static let white = srgb(0xFFFFFF)
}

// MARK: - Geometry (top-down 1024 x 1024 space)

enum Geo {
    static let canvas: CGFloat = 1024
    static let plate = CGRect(x: 100, y: 100, width: 824, height: 824)

    static let bezel = CGRect(x: 214, y: 250, width: 556, height: 370)
    static let bezelRadius: CGFloat = 52
    static let screen = bezel.insetBy(dx: 32, dy: 32)
    static let screenRadius: CGFloat = 24
    static let neck = CGRect(x: 457, y: 616, width: 70, height: 66)
    static let base = CGRect(x: 352, y: 676, width: 280, height: 40)

    /// Hand origin and rotation; the hand is drawn in its own local space below.
    static let handOrigin = CGPoint(x: 548, y: 410)
    static let handRotation: CGFloat = -0.16
}

/// Superellipse |x/a|^n + |y/b|^n = 1, the macOS icon outline.
func squircle(in rect: CGRect, exponent n: Double = 5, samples: Int = 720) -> CGPath {
    let a = rect.width / 2, b = rect.height / 2
    let path = CGMutablePath()
    for i in 0..<samples {
        let t = 2 * Double.pi * Double(i) / Double(samples)
        let c = cos(t), s = sin(t)
        let x = rect.midX + a * CGFloat(pow(abs(c), 2 / n) * (c < 0 ? -1 : 1))
        let y = rect.midY + b * CGFloat(pow(abs(s), 2 / n) * (s < 0 ? -1 : 1))
        if i == 0 { path.move(to: CGPoint(x: x, y: y)) } else { path.addLine(to: CGPoint(x: x, y: y)) }
    }
    path.closeSubpath()
    return path
}

func rounded(_ rect: CGRect, _ radius: CGFloat) -> CGPath {
    CGPath(roundedRect: rect, cornerWidth: radius, cornerHeight: radius, transform: nil)
}

/// Pointing hand in local coordinates (index finger up, knuckles of the folded fingers along the top
/// of the palm, thumb on the left). Returns the solid parts and the crease lines between fingers.
func handParts() -> (shapes: [CGPath], creases: [(CGPoint, CGPoint)]) {
    var shapes: [CGPath] = [
        rounded(CGRect(x: 58, y: 0, width: 72, height: 236), 36),     // index finger
        rounded(CGRect(x: 126, y: 118, width: 64, height: 136), 32),  // middle finger
        rounded(CGRect(x: 186, y: 140, width: 60, height: 124), 30),  // ring finger
        rounded(CGRect(x: 240, y: 168, width: 54, height: 110), 27),  // little finger
        rounded(CGRect(x: 58, y: 190, width: 236, height: 196), 72),  // palm
    ]
    var thumb = CGAffineTransform(translationX: 66, y: 246).rotated(by: -0.82)
    shapes.append(CGPath(roundedRect: CGRect(x: -32, y: -112, width: 64, height: 140),
                         cornerWidth: 32, cornerHeight: 32, transform: &thumb))
    let creases = [
        (CGPoint(x: 128, y: 196), CGPoint(x: 128, y: 248)),
        (CGPoint(x: 188, y: 206), CGPoint(x: 188, y: 256)),
        (CGPoint(x: 242, y: 222), CGPoint(x: 242, y: 266)),
    ]
    return (shapes, creases)
}

// MARK: - Drawing

func drawIcon(in ctx: CGContext, scale: CGFloat) {
    // Shadow offsets and blur are in device pixels, so they scale with the output size.
    func shadow(_ dy: CGFloat, _ blur: CGFloat, _ alpha: CGFloat) {
        ctx.setShadow(offset: CGSize(width: 0, height: -dy * scale), blur: blur * scale, color: srgb(0x1A1040, alpha))
    }

    let plate = squircle(in: Geo.plate)

    // Drop shadow under the plate.
    ctx.saveGState()
    shadow(14, 30, 0.45)
    ctx.addPath(plate)
    ctx.setFillColor(Palette.violet)
    ctx.fillPath()
    ctx.restoreGState()

    // Plate gradient and a soft highlight in the upper left.
    ctx.saveGState()
    ctx.addPath(plate)
    ctx.clip()
    ctx.drawLinearGradient(
        gradient([(Palette.indigo, 0), (Palette.violet, 0.5), (Palette.purple, 1)]),
        start: CGPoint(x: Geo.plate.minX, y: Geo.plate.minY),
        end: CGPoint(x: Geo.plate.maxX, y: Geo.plate.maxY),
        options: [.drawsBeforeStartLocation, .drawsAfterEndLocation]
    )
    ctx.drawRadialGradient(
        gradient([(srgb(0xFFFFFF, 0.26), 0), (srgb(0xFFFFFF, 0), 1)]),
        startCenter: CGPoint(x: 300, y: 220), startRadius: 0,
        endCenter: CGPoint(x: 300, y: 220), endRadius: 560,
        options: []
    )
    ctx.restoreGState()

    // Monitor: white bezel, deep screen, neck and base.
    ctx.saveGState()
    shadow(10, 24, 0.30)
    ctx.setFillColor(Palette.white)
    ctx.addPath(rounded(Geo.bezel, Geo.bezelRadius))
    ctx.addPath(rounded(Geo.neck, 8))
    ctx.addPath(rounded(Geo.base, 20))
    ctx.fillPath()
    ctx.restoreGState()

    ctx.saveGState()
    ctx.addPath(rounded(Geo.screen, Geo.screenRadius))
    ctx.clip()
    ctx.drawLinearGradient(
        gradient([(Palette.screenTop, 0), (Palette.screenBottom, 1)]),
        start: CGPoint(x: 0, y: Geo.screen.minY),
        end: CGPoint(x: 0, y: Geo.screen.maxY),
        options: []
    )
    ctx.drawRadialGradient(
        gradient([(srgb(0x9C8CFF, 0.45), 0), (srgb(0x9C8CFF, 0), 1)]),
        startCenter: CGPoint(x: Geo.screen.minX + 120, y: Geo.screen.minY + 70), startRadius: 0,
        endCenter: CGPoint(x: Geo.screen.minX + 120, y: Geo.screen.minY + 70), endRadius: 300,
        options: []
    )
    ctx.restoreGState()

    // Hand: outline in the plate color so it separates from the monitor, then white fill, then creases.
    let (shapes, creases) = handParts()
    ctx.saveGState()
    ctx.translateBy(x: Geo.handOrigin.x, y: Geo.handOrigin.y)
    ctx.rotate(by: Geo.handRotation)

    ctx.saveGState()
    shadow(12, 26, 0.35)
    ctx.setStrokeColor(Palette.outline)
    ctx.setLineWidth(30)
    ctx.setLineJoin(.round)
    for shape in shapes { ctx.addPath(shape) }
    ctx.strokePath()
    ctx.restoreGState()

    ctx.setFillColor(Palette.white)
    for shape in shapes {
        ctx.addPath(shape)
        ctx.fillPath()
    }

    ctx.setStrokeColor(Palette.crease)
    ctx.setLineWidth(8)
    ctx.setLineCap(.round)
    for (from, to) in creases {
        ctx.move(to: from)
        ctx.addLine(to: to)
    }
    ctx.strokePath()
    ctx.restoreGState()
}

/// Renders the icon at `pixels` x `pixels`.
func render(pixels: Int) -> CGImage {
    let ctx = CGContext(
        data: nil, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: 0,
        space: sRGB, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    )!
    ctx.interpolationQuality = .high
    ctx.setShouldAntialias(true)
    let scale = CGFloat(pixels) / Geo.canvas
    // Top-down coordinates: flip so y grows downward, then scale the 1024 design space.
    ctx.translateBy(x: 0, y: CGFloat(pixels))
    ctx.scaleBy(x: scale, y: -scale)
    drawIcon(in: ctx, scale: scale)
    return ctx.makeImage()!
}

func writePNG(_ image: CGImage, to url: URL) throws {
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil) else {
        throw NSError(domain: "make-icon", code: 1, userInfo: [NSLocalizedDescriptionKey: "cannot create \(url.path)"])
    }
    CGImageDestinationAddImage(dest, image, nil)
    guard CGImageDestinationFinalize(dest) else {
        throw NSError(domain: "make-icon", code: 2, userInfo: [NSLocalizedDescriptionKey: "cannot write \(url.path)"])
    }
}

// MARK: - Icon set

let repoRoot = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let outDir = CommandLine.arguments.count > 1
    ? URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
    : repoRoot.appendingPathComponent("app/Deskhand/Resources/Assets.xcassets/AppIcon.appiconset", isDirectory: true)

try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)

var images: [[String: String]] = []
for points in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let filename = scale == 1 ? "icon_\(points)x\(points).png" : "icon_\(points)x\(points)@2x.png"
        try writePNG(render(pixels: points * scale), to: outDir.appendingPathComponent(filename))
        images.append([
            "filename": filename,
            "idiom": "mac",
            "scale": "\(scale)x",
            "size": "\(points)x\(points)",
        ])
    }
}

let contents: [String: Any] = [
    "images": images,
    "info": ["author": "scripts/make-icon.swift", "version": 1],
]
let json = try JSONSerialization.data(withJSONObject: contents, options: [.prettyPrinted, .sortedKeys])
try json.write(to: outDir.appendingPathComponent("Contents.json"))
print(outDir.path)
