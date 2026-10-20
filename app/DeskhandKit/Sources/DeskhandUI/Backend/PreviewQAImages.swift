import CoreGraphics
import CoreText
import Foundation
import ImageIO
import Synchronization
import UniformTypeIdentifiers

/// Drawn screenshots of a Windows desktop for the preview's QA evidence, as PNG data. Drawing uses
/// Core Graphics and Core Text only, so it runs off the main thread.
enum PreviewQAImages {
    enum Screen: String, Sendable, CaseIterable {
        /// The fixture app while the bot is still typing its answer.
        case fixtureTyping
        /// The fixture app after the answer ended with `[done]`.
        case fixtureDone
        /// Shop Admin's dashboard after signing in.
        case shopDashboard
    }

    static let size = CGSize(width: 1600, height: 1000)
    private static let cache = Mutex<[Screen: Data]>([:])

    static func png(_ screen: Screen) -> Data {
        if let data = cache.withLock({ $0[screen] }) { return data }
        let data = draw(screen)
        cache.withLock { $0[screen] = data }
        return data
    }

    private static func draw(_ screen: Screen) -> Data {
        let width = Int(size.width)
        let height = Int(size.height)
        guard let space = CGColorSpace(name: CGColorSpace.sRGB),
              let context = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0, space: space,
                                      bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return Data() }
        let canvas = Canvas(context: context, height: size.height)
        canvas.desktop()
        switch screen {
        case .fixtureTyping: canvas.fixture(done: false)
        case .fixtureDone: canvas.fixture(done: true)
        case .shopDashboard: canvas.dashboard()
        }
        canvas.taskbar()
        guard let image = context.makeImage() else { return Data() }
        let data = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil) else { return Data() }
        CGImageDestinationAddImage(destination, image, nil)
        CGImageDestinationFinalize(destination)
        return data as Data
    }

    /// Drawing in top-left coordinates.
    private struct Canvas {
        let context: CGContext
        let height: CGFloat

        func color(_ white: CGFloat, _ alpha: CGFloat = 1) -> CGColor { CGColor(gray: white, alpha: alpha) }
        func color(_ r: CGFloat, _ g: CGFloat, _ b: CGFloat, _ alpha: CGFloat = 1) -> CGColor {
            CGColor(srgbRed: r, green: g, blue: b, alpha: alpha)
        }

        func rect(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat) -> CGRect {
            CGRect(x: x, y: height - y - h, width: w, height: h)
        }

        func fill(_ r: CGRect, _ fill: CGColor, radius: CGFloat = 0) {
            context.addPath(CGPath(roundedRect: r, cornerWidth: radius, cornerHeight: radius, transform: nil))
            context.setFillColor(fill)
            context.fillPath()
        }

        func stroke(_ r: CGRect, _ stroke: CGColor, radius: CGFloat = 0, width: CGFloat = 1) {
            context.addPath(CGPath(roundedRect: r.insetBy(dx: width / 2, dy: width / 2), cornerWidth: radius, cornerHeight: radius, transform: nil))
            context.setStrokeColor(stroke)
            context.setLineWidth(width)
            context.strokePath()
        }

        /// Draws `string` with its top-left at `x`, `y` and returns its width.
        @discardableResult
        func text(_ string: String, _ x: CGFloat, _ y: CGFloat, size: CGFloat, bold: Bool = false, color: CGColor) -> CGFloat {
            guard let font = CTFontCreateUIFontForLanguage(bold ? .emphasizedSystem : .system, size, nil) else { return 0 }
            let attributes: [NSAttributedString.Key: Any] = [
                NSAttributedString.Key(kCTFontAttributeName as String): font,
                NSAttributedString.Key(kCTForegroundColorAttributeName as String): color,
            ]
            let line = CTLineCreateWithAttributedString(NSAttributedString(string: string, attributes: attributes))
            context.textPosition = CGPoint(x: x, y: height - y - CTFontGetAscent(font))
            CTLineDraw(line, context)
            return CGFloat(CTLineGetTypographicBounds(line, nil, nil, nil))
        }

        func desktop() {
            let colors = [color(0.07, 0.22, 0.47), color(0.27, 0.55, 0.82)] as CFArray
            guard let space = CGColorSpace(name: CGColorSpace.sRGB),
                  let gradient = CGGradient(colorsSpace: space, colors: colors, locations: [0, 1]) else { return }
            context.drawLinearGradient(gradient, start: CGPoint(x: 0, y: height), end: CGPoint(x: 1600, y: 0), options: [])
        }

        func taskbar() {
            fill(rect(0, 944, 1600, 56), color(0.95, 0.96))
            fill(rect(0, 944, 1600, 1), color(0.8))
            let start: CGFloat = 664
            for n in 0..<6 {
                let x = start + CGFloat(n) * 48
                if n == 0 {
                    for (dx, dy) in [(0.0, 0.0), (11.0, 0.0), (0.0, 11.0), (11.0, 11.0)] {
                        fill(rect(x + 10 + dx, 960 + dy, 10, 10), color(0.0, 0.44, 0.84), radius: 1.5)
                    }
                } else {
                    let tones = [color(0.98, 0.75, 0.18), color(0.13, 0.55, 0.95), color(0.16, 0.68, 0.38), color(0.55, 0.36, 0.86),
                                 color(0.92, 0.32, 0.24)]
                    fill(rect(x + 8, 956, 32, 32), tones[n - 1], radius: 7)
                }
            }
            text("오후 2:41", 1468, 958, size: 13, color: color(0.12))
            text("2026-10-21", 1460, 976, size: 12, color: color(0.25))
        }

        func window(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat, title: String) {
            fill(rect(x + 2, y + 6, w, h).offsetBy(dx: 0, dy: -4), color(0, 0.25), radius: 10)
            fill(rect(x, y, w, h), color(0.99), radius: 9)
            fill(rect(x, y, w, 44), color(0.94), radius: 9)
            fill(rect(x, y + 30, w, 14), color(0.94))
            fill(rect(x, y + 44, w, 1), color(0.86))
            text(title, x + 18, y + 13, size: 15, color: color(0.1))
            let right = x + w
            context.setStrokeColor(color(0.2))
            context.setLineWidth(1.4)
            context.move(to: CGPoint(x: right - 132, y: height - y - 22))
            context.addLine(to: CGPoint(x: right - 118, y: height - y - 22))
            context.strokePath()
            stroke(rect(right - 84, y + 15, 13, 13), color(0.2), width: 1.4)
            context.move(to: CGPoint(x: right - 36, y: height - y - 15))
            context.addLine(to: CGPoint(x: right - 23, y: height - y - 28))
            context.move(to: CGPoint(x: right - 36, y: height - y - 28))
            context.addLine(to: CGPoint(x: right - 23, y: height - y - 15))
            context.strokePath()
        }

        func fixture(done: Bool) {
            let x: CGFloat = 260
            let y: CGFloat = 110
            window(x, y, 1080, 720, title: "Deskhand Fixture")
            text("한글 라벨", x + 36, y + 76, size: 20, bold: true, color: color(0.1))
            text("Message", x + 36, y + 128, size: 13, color: color(0.4))
            fill(rect(x + 36, y + 150, 820, 46), color(1), radius: 5)
            stroke(rect(x + 36, y + 150, 820, 46), color(0.72), radius: 5)
            fill(rect(x + 37, y + 193, 818, 2), color(0.0, 0.37, 0.72))
            fill(rect(x + 872, y + 150, 172, 46), done ? color(0.0, 0.37, 0.72) : color(0.86), radius: 5)
            text("Send", x + 936, y + 162, size: 16, bold: true, color: done ? color(1) : color(0.55))
            text("Transcript", x + 36, y + 226, size: 13, color: color(0.4))
            fill(rect(x + 36, y + 248, 1008, 430), color(0.975), radius: 6)
            stroke(rect(x + 36, y + 248, 1008, 430), color(0.82), radius: 6)
            text("You: 안녕 QA", x + 60, y + 272, size: 17, color: color(0.15))
            text("Bot: echo 안녕 QA", x + 60, y + 306, size: 17, color: color(0.15))
            if done {
                text("[done]", x + 60, y + 340, size: 17, bold: true, color: color(0.05, 0.5, 0.22))
            } else {
                let width = text("입력 중…", x + 60, y + 340, size: 17, color: color(0.55))
                fill(rect(x + 64 + width, y + 341, 2, 21), color(0.2))
            }
        }

        func dashboard() {
            let x: CGFloat = 180
            let y: CGFloat = 80
            window(x, y, 1240, 800, title: "Shop Admin — 대시보드")
            fill(rect(x, y + 45, 220, 755), color(0.14, 0.16, 0.22))
            for (n, item) in ["대시보드", "주문", "상품", "고객", "설정"].enumerated() {
                let top = y + 80 + CGFloat(n) * 48
                if n == 0 { fill(rect(x + 12, top - 10, 196, 40), color(0.26, 0.32, 0.5), radius: 6) }
                text(item, x + 32, top, size: 16, bold: n == 0, color: color(n == 0 ? 1 : 0.78))
            }
            let left = x + 260
            text("환영합니다, qa-bot 님", left, y + 84, size: 26, bold: true, color: color(0.1))
            text("스테이징 · 마지막 로그인 오늘 오후 2:40", left, y + 124, size: 14, color: color(0.42))
            let cards = [("오늘 주문", "42건"), ("오늘 매출", "₩1,284,000"), ("반품 요청", "3건")]
            for (n, card) in cards.enumerated() {
                let cx = left + CGFloat(n) * 312
                fill(rect(cx, y + 168, 292, 132), color(1), radius: 10)
                stroke(rect(cx, y + 168, 292, 132), color(0.86), radius: 10)
                text(card.0, cx + 22, y + 190, size: 15, color: color(0.42))
                text(card.1, cx + 22, y + 226, size: 30, bold: true, color: color(0.1))
            }
            fill(rect(left, y + 328, 916, 420), color(1), radius: 10)
            stroke(rect(left, y + 328, 916, 420), color(0.86), radius: 10)
            text("시간대별 주문", left + 24, y + 350, size: 16, bold: true, color: color(0.15))
            let bars: [CGFloat] = [0.2, 0.35, 0.3, 0.55, 0.8, 0.62, 0.9, 0.7, 0.45, 0.5, 0.38, 0.6]
            for (n, value) in bars.enumerated() {
                let barHeight = 280 * value
                fill(rect(left + 40 + CGFloat(n) * 72, y + 720 - barHeight, 40, barHeight), color(0.33, 0.45, 0.88), radius: 4)
            }
        }
    }
}
