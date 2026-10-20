import DeskhandClient
import Foundation

/// The simulated Electron app of the preview debugger: “Shop Admin”, a point-of-sale admin with a
/// Node main process and one renderer (the checkout page). Its scripts, the execution traces the
/// renderer and main process walk through, the variables of every step and the console messages.
/// Object IDs encode the pause and the property path they name.
public enum PreviewDebugProgram {
    // MARK: App

    public static let appPath = "C:\\Shop\\desktop\\Shop Admin.exe"
    public static let appName = "Shop Admin"
    public static let appPID = 10244
    public static let inspectPort = 52011
    public static let devtoolsPort = 52012
    public static let mainTargetID = "5b3c6c1e-7f0a-4c55-9a43-2f3c9d1e8a11"
    public static let pageTargetID = "A3F1C2D9E8B74F6AA0C1D2E3F4A5B6C7"
    /// A WebView2 app: launching it never opens a debug port.
    public static let notElectronPath = "C:\\Shop\\bin\\Shop.exe"
    /// A fused Electron app that ignores `--inspect`: launching it attaches its renderer only.
    public static let kioskPath = "C:\\Users\\taeyoung\\AppData\\Local\\Programs\\shop-kiosk\\Shop Kiosk.exe"

    public static func targets(mainGen: Int = 0, pageGen: Int = 0, mainPaused: Bool = false, pagePaused: Bool = false,
                               pagePausePending: Bool = false) -> [DebugTarget] {
        [mainTarget(gen: mainGen, paused: mainPaused), pageTarget(gen: pageGen, paused: pagePaused, pausePending: pagePausePending)]
    }

    public static func mainTarget(gen: Int = 0, paused: Bool = false) -> DebugTarget {
        DebugTarget(id: mainTargetID, role: DebugTargetRole.main.rawValue, type: "node", url: Script.main.url, title: "Shop Admin — main",
                    attached: true, paused: paused, gen: gen)
    }

    public static func pageTarget(gen: Int = 0, paused: Bool = false, pausePending: Bool = false) -> DebugTarget {
        DebugTarget(id: pageTargetID, role: DebugTargetRole.renderer.rawValue, type: "page", url: "app://shop-admin/index.html",
                    title: "Shop 관리자 — 결제", attached: true, paused: paused, pausePending: pausePending, gen: gen)
    }

    // MARK: Scripts

    public enum Script: String, CaseIterable, Sendable {
        case main
        case index
        case checkout
        case api
        case receipt

        public var scriptID: String {
            switch self {
            case .main: "41"
            case .index: "87"
            case .checkout: "88"
            case .api: "89"
            case .receipt: "102"
            }
        }

        public var url: String {
            switch self {
            case .main: "file:///C:/Shop/desktop/resources/app/main.js"
            case .index: "app://shop-admin/renderer/index.js"
            case .checkout: "app://shop-admin/renderer/checkout.js"
            case .api: "app://shop-admin/renderer/api.js"
            case .receipt: "app://shop-admin/renderer/receipt.js"
            }
        }

        public var targetID: String { self == .main ? mainTargetID : pageTargetID }

        /// Scripts a launched session has loaded; `receipt.js` loads after the first order.
        static let loadedAtLaunch: [Script] = [.main, .index, .checkout, .api]

        public var source: String {
            switch self {
            case .main: PreviewDebugProgram.mainSource
            case .index: PreviewDebugProgram.indexSource
            case .checkout: PreviewDebugProgram.checkoutSource
            case .api: PreviewDebugProgram.apiSource
            case .receipt: PreviewDebugProgram.receiptSource
            }
        }

        public var lines: [String] { source.components(separatedBy: "\n") }

        /// The 1-based line containing `marker`.
        public func line(_ marker: String) -> Int {
            (lines.firstIndex { $0.contains(marker) } ?? 0) + 1
        }

        /// The 1-based column of the first non-space character of `line`.
        func column(_ line: Int) -> Int {
            guard line >= 1, line <= lines.count else { return 1 }
            return (lines[line - 1].prefix { $0 == " " }.count) + 1
        }

        /// The first line at or after `line` that holds code, as CDP moves a breakpoint.
        func breakableLine(atOrAfter line: Int) -> Int? {
            guard line >= 1 else { return nil }
            var index = line - 1
            while index < lines.count {
                let text = lines[index].trimmingCharacters(in: .whitespaces)
                if !text.isEmpty, !text.hasPrefix("//"), text != "}", text != "});", text != "};" { return index + 1 }
                index += 1
            }
            return nil
        }

        public static func script(url: String) -> Script? { allCases.first { $0.url == url } }
        public static func script(id: String) -> Script? { allCases.first { $0.scriptID == id } }
    }

    static let mainSource = """
        const { app, BrowserWindow, ipcMain } = require('electron');
        const path = require('node:path');
        const { printReceipt } = require('./printer');

        let mainWindow;

        function createWindow() {
          mainWindow = new BrowserWindow({
            width: 1280,
            height: 800,
            title: 'Shop 관리자',
            webPreferences: { preload: path.join(__dirname, 'preload.js') },
          });
          mainWindow.loadURL('app://shop-admin/index.html');
          console.log(`창을 만들었습니다: ${mainWindow.getSize().join('×')}`);
        }

        ipcMain.handle('receipt:print', async (_event, receipt) => {
          const printer = process.env.SHOP_PRINTER ?? 'EPSON TM-T88VI';
          console.log(`영수증 ${receipt.number} 출력 → ${printer}`);
          return printReceipt(printer, receipt);
        });

        app.whenReady().then(() => {
          console.info(`Shop 관리자 ${app.getVersion()} 시작 (Electron ${process.versions.electron}, Node ${process.versions.node})`);
          createWindow();
        });
        """

    static let indexSource = """
        import { submitOrder, applyCoupon } from './checkout.js';

        const submit = document.querySelector('#submit-order');
        const couponButton = document.querySelector('#apply-coupon');

        console.log('결제 화면을 불러왔습니다');

        submit.addEventListener('click', async () => {
          submit.disabled = true;
          try {
            await submitOrder();
          } finally {
            submit.disabled = false;
          }
        });

        couponButton.addEventListener('click', () => {
          applyCoupon(document.querySelector('#coupon-code').value);
        });
        """

    static let checkoutSource = """
        import { postOrder, applyDiscount } from './api.js';
        import { formatWon } from './format.js';

        const state = {
          cart: [
            { sku: 'A-1001', name: '무선 바코드 스캐너', price: 32000, qty: 1 },
            { sku: 'B-2040', name: '영수증 용지 (10롤)', price: 9000, qty: 2 },
            { sku: 'C-3300', name: '카드 단말기 거치대', price: 2000, qty: 1 },
          ],
          coupon: { code: 'GOLD10', rate: 0.1 },
          customer: { id: 'C-20931', name: '김민지', tier: 'gold' },
        };

        function subtotalOf(cart) {
          return cart.reduce((sum, line) => sum + line.price * line.qty, 0);
        }

        export async function submitOrder() {
          const subtotal = subtotalOf(state.cart);
          const coupon = state.coupon;
          const discount = coupon ? Math.round(subtotal * coupon.rate) : 0;
          const order = {
            id: 'ORD-2026-1021-0042',
            customer: state.customer,
            lines: state.cart,
            subtotal,
            discount,
            total: subtotal - discount,
          };
          console.log('주문 제출', order.id, formatWon(order.total));
          const receipt = await postOrder(order);
          showReceipt(receipt);
          return receipt;
        }

        export function applyCoupon(code) {
          const order = { id: 'ORD-2026-1021-0043', lines: state.cart };
          const coupon = { code, rate: 0.1 };
          state.coupon = coupon;
          return applyDiscount(order, coupon);
        }

        function showReceipt(receipt) {
          document.querySelector('#receipt').textContent = `영수증 ${receipt.number}`;
          window.shop.printReceipt(receipt);
        }
        """

    static let apiSource = """
        const BASE_URL = 'https://api.shop.example.kr/v2';
        const STORE = '강남점';

        async function request(path, body) {
          const response = await fetch(`${BASE_URL}${path}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-Store': encodeURIComponent(STORE) },
            body: JSON.stringify(body),
          });
          if (!response.ok) {
            throw new Error(`요청 실패 ${response.status}: ${path}`);
          }
          return response.json();
        }

        export async function fetchCoupon(customerId) {
          const started = performance.now();
          const result = await request('/coupons/lookup', { customerId });
          const elapsed = Math.round(performance.now() - started);
          if (elapsed > 1500) console.warn(`쿠폰 서버 응답이 느립니다 (${elapsed}ms)`);
          return result.coupons[0] ?? null;
        }

        export async function postOrder(order) {
          const payload = {
            orderId: order.id,
            amount: order.total,
            items: order.lines.map((line) => ({ sku: line.sku, qty: line.qty })),
          };
          return request('/orders', payload);
        }

        export function applyDiscount(order, coupon) {
          const amount = order.summary.total;
          return Math.round(amount * coupon.rate);
        }
        """

    static let receiptSource = """
        export function receiptLines(receipt) {
          return [
            `Shop 강남점  ${receipt.printedAt}`,
            `주문 ${receipt.orderId}`,
            `합계 ${receipt.total.toLocaleString('ko-KR')}원`,
          ];
        }
        """

    // MARK: Values

    /// A value of the simulated app.
    public indirect enum Value: Equatable, Sendable {
        case string(String)
        case number(Double)
        case bool(Bool)
        case null
        case undefined
        /// An object of `className` with ordered properties; `plain` objects preview as `{…}`.
        case object(className: String, [Field], plain: Bool = false)
        case array([Value])
        case function(String)
        /// An Error object.
        case error(className: String, message: String, stack: String)
        /// The global object, whose properties are the global scope.
        case global
    }

    public struct Field: Equatable, Sendable {
        public var name: String
        public var value: Value

        public init(_ name: String, _ value: Value) {
            self.name = name
            self.value = value
        }
    }

    static func plain(_ fields: [Field]) -> Value { .object(className: "Object", fields, plain: true) }

    static let cartLines: Value = .array([
        plain([Field("sku", .string("A-1001")), Field("name", .string("무선 바코드 스캐너")), Field("price", .number(32000)), Field("qty", .number(1))]),
        plain([Field("sku", .string("B-2040")), Field("name", .string("영수증 용지 (10롤)")), Field("price", .number(9000)), Field("qty", .number(2))]),
        plain([Field("sku", .string("C-3300")), Field("name", .string("카드 단말기 거치대")), Field("price", .number(2000)), Field("qty", .number(1))]),
    ])
    static let customer = plain([Field("id", .string("C-20931")), Field("name", .string("김민지")), Field("tier", .string("gold"))])
    static let coupon = plain([Field("code", .string("GOLD10")), Field("rate", .number(0.1))])
    static let order = plain([
        Field("id", .string("ORD-2026-1021-0042")), Field("customer", customer), Field("lines", cartLines),
        Field("subtotal", .number(52000)), Field("discount", .number(5200)), Field("total", .number(46800)),
    ])
    static let payload = plain([
        Field("orderId", .string("ORD-2026-1021-0042")), Field("amount", .number(46800)),
        Field("items", .array([
            plain([Field("sku", .string("A-1001")), Field("qty", .number(1))]),
            plain([Field("sku", .string("B-2040")), Field("qty", .number(2))]),
            plain([Field("sku", .string("C-3300")), Field("qty", .number(1))]),
        ])),
    ])
    static let receiptValue = plain([
        Field("number", .string("R-0042")), Field("orderId", .string("ORD-2026-1021-0042")), Field("total", .number(46800)),
        Field("printedAt", .string("2026-10-21 14:32:08")),
    ])
    static let response = Value.object(className: "Response", [
        Field("ok", .bool(true)), Field("status", .number(201)), Field("statusText", .string("Created")),
        Field("url", .string("https://api.shop.example.kr/v2/orders")), Field("redirected", .bool(false)),
    ])
    static let couponOrder = plain([Field("id", .string("ORD-2026-1021-0043")), Field("lines", cartLines)])
    static let couponTyped = plain([Field("code", .string("GOLD10")), Field("rate", .number(0.1))])
    public static let typeError: Value = {
        let api = Script.api
        let checkout = Script.checkout
        let index = Script.index
        let line = api.line("const amount = order.summary.total")
        let frames = [
            "    at applyDiscount (\(api.url):\(line):32)",
            "    at applyCoupon (\(checkout.url):\(checkout.line("return applyDiscount")):10)",
            "    at HTMLButtonElement.<anonymous> (\(index.url):\(index.line("applyCoupon(document")):3)",
        ]
        let message = "Cannot read properties of undefined (reading 'total')"
        return .error(className: "TypeError", message: message, stack: (["TypeError: " + message] + frames).joined(separator: "\n"))
    }()
    static let button = Value.object(className: "HTMLButtonElement", [
        Field("id", .string("submit-order")), Field("disabled", .bool(false)), Field("textContent", .string("주문 제출")),
        Field("type", .string("button")),
    ])
    static let couponButtonValue = Value.object(className: "HTMLButtonElement", [
        Field("id", .string("apply-coupon")), Field("disabled", .bool(false)), Field("textContent", .string("쿠폰 적용")),
    ])
    static let ipcEvent = Value.object(className: "IpcMainInvokeEvent", [
        Field("frameId", .number(1)), Field("processId", .number(10251)),
        Field("sender", .object(className: "WebContents", [Field("id", .number(1)), Field("isDestroyed", .function("ƒ isDestroyed()"))])),
    ])

    static let windowGlobals: [Field] = [
        Field("document", .object(className: "HTMLDocument", [
            Field("title", .string("Shop 관리자 — 결제")), Field("URL", .string("app://shop-admin/index.html")),
            Field("readyState", .string("complete")),
        ])),
        Field("location", .object(className: "Location", [
            Field("href", .string("app://shop-admin/index.html")), Field("origin", .string("app://shop-admin")),
            Field("pathname", .string("/index.html")),
        ])),
        Field("localStorage", .object(className: "Storage", [
            Field("cartDraft", .string("{\"lines\":3,\"savedAt\":\"2026-10-21T05:31:40Z\"}")), Field("length", .number(1)),
        ])),
        Field("navigator", .object(className: "Navigator", [
            Field("language", .string("ko-KR")), Field("onLine", .bool(true)),
            Field("userAgent", .string("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) ShopAdmin/1.18.0 Chrome/130.0.6723.137 Electron/33.2.0 Safari/537.36")),
        ])),
        Field("shop", .object(className: "Object", [
            Field("printReceipt", .function("ƒ printReceipt(receipt)")), Field("version", .string("1.18.0")),
        ], plain: true)),
        Field("shopConfig", plain([
            Field("apiBase", .string("https://api.shop.example.kr/v2")), Field("store", .string("강남점")),
            Field("printer", .string("EPSON TM-T88VI")),
            Field("features", plain([Field("coupons", .bool(true)), Field("receiptQR", .bool(false))])),
        ])),
        Field("window", .global),
    ]

    static let nodeGlobals: [Field] = [
        Field("process", .object(className: "process", [
            Field("pid", .number(Double(appPID))), Field("platform", .string("win32")), Field("arch", .string("x64")),
            Field("versions", plain([Field("node", .string("20.18.1")), Field("electron", .string("33.2.0")),
                                     Field("chrome", .string("130.0.6723.137")), Field("v8", .string("13.0.245.25-electron.0"))])),
        ])),
        Field("Buffer", .function("ƒ Buffer(arg, encodingOrOffset, length)")),
        Field("setTimeout", .function("ƒ setTimeout(callback, after, arg1, arg2, arg3)")),
        Field("global", .global),
    ]

    // MARK: Traces

    /// What a target runs: the submit-order flow and the coupon flow in the renderer, the receipt
    /// print handler in the main process.
    public enum Flow: String, Sendable, CaseIterable {
        case submit
        case coupon
        case print
        /// The first-line stop of the main process.
        case boot

        public var targetID: String { self == .print || self == .boot ? mainTargetID : pageTargetID }
    }

    /// A frame of a step: the function, where it is, and its scopes' variables.
    public struct FrameSpec: Sendable {
        public var function: String
        public var script: Script
        public var line: Int
        public var locals: [Field]
    }

    /// One statement a flow reaches, with the whole stack, innermost first.
    public struct Step: Sendable {
        public var frames: [FrameSpec]
        /// A console message the statement prints.
        public var log: (level: ConsoleLevel, text: String)?
        /// The statement throws this value.
        public var throwing: Value?

        var depth: Int { frames.count }
    }

    public static func steps(_ flow: Flow) -> [Step] {
        switch flow {
        case .submit: submitSteps
        case .coupon: couponSteps
        case .print: printSteps
        case .boot: [Step(frames: [FrameSpec(function: "", script: .main, line: 1, locals: [])])]
        }
    }

    static let submitSteps: [Step] = {
        let index = Script.index
        let checkout = Script.checkout
        let api = Script.api
        func handler(_ marker: String) -> FrameSpec { FrameSpec(function: "", script: index, line: index.line(marker), locals: []) }
        let handlerAtAwait = handler("await submitOrder()")
        func submit(_ marker: String, _ locals: [Field]) -> FrameSpec {
            FrameSpec(function: "submitOrder", script: checkout, line: checkout.line(marker), locals: locals)
        }
        let atPost = submit("await postOrder(order)", [Field("subtotal", .number(52000)), Field("coupon", coupon),
                                                       Field("discount", .number(5200)), Field("order", order), Field("receipt", .undefined)])
        let afterPost = [Field("subtotal", .number(52000)), Field("coupon", coupon), Field("discount", .number(5200)),
                         Field("order", order), Field("receipt", receiptValue)]
        func post(_ marker: String, payloadValue: Value) -> FrameSpec {
            FrameSpec(function: "postOrder", script: api, line: api.line(marker),
                      locals: [Field("order", order), Field("payload", payloadValue)])
        }
        func request(_ marker: String, response value: Value) -> FrameSpec {
            FrameSpec(function: "request", script: api, line: api.line(marker),
                      locals: [Field("path", .string("/orders")), Field("body", payload), Field("response", value)])
        }
        return [
            Step(frames: [handler("submit.disabled = true")]),
            Step(frames: [handlerAtAwait]),
            Step(frames: [submit("const subtotal = subtotalOf", [Field("subtotal", .undefined), Field("coupon", .undefined),
                                                                 Field("discount", .undefined), Field("order", .undefined), Field("receipt", .undefined)]),
                          handlerAtAwait]),
            Step(frames: [FrameSpec(function: "subtotalOf", script: checkout, line: checkout.line("return cart.reduce"),
                                    locals: [Field("cart", cartLines)]),
                          submit("const subtotal = subtotalOf", [Field("subtotal", .undefined), Field("coupon", .undefined),
                                                                 Field("discount", .undefined), Field("order", .undefined), Field("receipt", .undefined)]),
                          handlerAtAwait]),
            Step(frames: [submit("const coupon = state.coupon", [Field("subtotal", .number(52000)), Field("coupon", .undefined),
                                                                 Field("discount", .undefined), Field("order", .undefined), Field("receipt", .undefined)]),
                          handlerAtAwait]),
            Step(frames: [submit("const discount =", [Field("subtotal", .number(52000)), Field("coupon", coupon),
                                                      Field("discount", .undefined), Field("order", .undefined), Field("receipt", .undefined)]),
                          handlerAtAwait]),
            Step(frames: [submit("const order = {", [Field("subtotal", .number(52000)), Field("coupon", coupon),
                                                     Field("discount", .number(5200)), Field("order", .undefined), Field("receipt", .undefined)]),
                          handlerAtAwait]),
            Step(frames: [submit("console.log('주문 제출'", [Field("subtotal", .number(52000)), Field("coupon", coupon),
                                                          Field("discount", .number(5200)), Field("order", order), Field("receipt", .undefined)]),
                          handlerAtAwait],
                 log: (.log, "주문 제출 ORD-2026-1021-0042 46,800원")),
            Step(frames: [atPost, handlerAtAwait]),
            Step(frames: [post("const payload = {", payloadValue: .undefined), atPost, handlerAtAwait]),
            Step(frames: [post("return request('/orders'", payloadValue: payload), atPost, handlerAtAwait]),
            Step(frames: [request("const response = await fetch", response: .undefined),
                          post("return request('/orders'", payloadValue: payload), atPost, handlerAtAwait]),
            Step(frames: [atPost, handlerAtAwait]),
            Step(frames: [request("if (!response.ok)", response: response)]),
            Step(frames: [request("return response.json()", response: response)]),
            Step(frames: [submit("showReceipt(receipt)", afterPost)]),
            Step(frames: [FrameSpec(function: "showReceipt", script: checkout, line: checkout.line("document.querySelector('#receipt')"),
                                    locals: [Field("receipt", receiptValue)]),
                          submit("showReceipt(receipt)", afterPost)]),
            Step(frames: [submit("return receipt;", afterPost)]),
            Step(frames: [handler("submit.disabled = false")]),
        ]
    }()

    static let couponSteps: [Step] = {
        let index = Script.index
        let checkout = Script.checkout
        let api = Script.api
        let handler = FrameSpec(function: "", script: index, line: index.line("applyCoupon(document"), locals: [])
        func apply(_ marker: String, _ locals: [Field]) -> FrameSpec {
            FrameSpec(function: "applyCoupon", script: checkout, line: checkout.line(marker),
                      locals: [Field("code", .string("GOLD10"))] + locals)
        }
        let atReturn = apply("return applyDiscount", [Field("order", couponOrder), Field("coupon", couponTyped)])
        return [
            Step(frames: [handler]),
            Step(frames: [apply("const order = { id: 'ORD-2026-1021-0043'", [Field("order", .undefined), Field("coupon", .undefined)]), handler]),
            Step(frames: [apply("const coupon = { code, rate", [Field("order", couponOrder), Field("coupon", .undefined)]), handler]),
            Step(frames: [apply("state.coupon = coupon", [Field("order", couponOrder), Field("coupon", couponTyped)]), handler]),
            Step(frames: [atReturn, handler]),
            Step(frames: [FrameSpec(function: "applyDiscount", script: api, line: api.line("const amount = order.summary.total"),
                                    locals: [Field("order", couponOrder), Field("coupon", couponTyped), Field("amount", .undefined)]),
                          atReturn, handler],
                 throwing: typeError),
        ]
    }()

    static let printSteps: [Step] = {
        let main = Script.main
        func handler(_ marker: String, printer: Value) -> FrameSpec {
            FrameSpec(function: "", script: main, line: main.line(marker),
                      locals: [Field("_event", ipcEvent), Field("receipt", receiptValue), Field("printer", printer)])
        }
        return [
            Step(frames: [handler("const printer = process.env", printer: .undefined)]),
            Step(frames: [handler("console.log(`영수증", printer: .string("EPSON TM-T88VI"))],
                 log: (.log, "영수증 R-0042 출력 → EPSON TM-T88VI")),
            Step(frames: [handler("return printReceipt(printer", printer: .string("EPSON TM-T88VI"))]),
        ]
    }()

    /// The module or closure variables of a script.
    static func moduleFields(_ script: Script) -> [Field] {
        switch script {
        case .main:
            [Field("mainWindow", .object(className: "BrowserWindow", [Field("id", .number(1)), Field("title", .string("Shop 관리자"))])),
             Field("createWindow", .function("ƒ createWindow()")), Field("path", .object(className: "Object", [
                 Field("sep", .string("\\")), Field("join", .function("ƒ join(...args)")),
             ], plain: true)),
             Field("printReceipt", .function("ƒ printReceipt(printer, receipt)"))]
        case .index:
            [Field("submit", button), Field("couponButton", couponButtonValue), Field("submitOrder", .function("ƒ submitOrder()")),
             Field("applyCoupon", .function("ƒ applyCoupon(code)"))]
        case .checkout:
            [Field("state", plain([Field("cart", cartLines), Field("coupon", coupon), Field("customer", customer)])),
             Field("subtotalOf", .function("ƒ subtotalOf(cart)")), Field("showReceipt", .function("ƒ showReceipt(receipt)")),
             Field("postOrder", .function("ƒ postOrder(order)")), Field("applyDiscount", .function("ƒ applyDiscount(order, coupon)")),
             Field("formatWon", .function("ƒ formatWon(value)"))]
        case .api:
            [Field("BASE_URL", .string("https://api.shop.example.kr/v2")), Field("STORE", .string("강남점")),
             Field("request", .function("ƒ request(path, body)"))]
        case .receipt:
            []
        }
    }

    // MARK: Pause states

    /// The pause of `flow` at `step` with `gen`: call frames whose scopes carry object IDs of this pause.
    public static func pauseState(flow: Flow, step: Int, gen: Int, reason: DebugPauseReason, hitBreakpoints: [String] = []) -> DebugPauseState {
        let spec = steps(flow)[step]
        let frames = spec.frames.enumerated().map { index, frame in
            DebugCallFrame(
                frameID: "cf:\(flow.rawValue):\(step):\(index):\(gen)", function: frame.function, scriptID: frame.script.scriptID,
                url: frame.script.url, line: frame.line, column: frame.script.column(frame.line),
                scopeChain: scopes(for: frame).enumerated().map { scopeIndex, scope in
                    DebugScope(type: scope.type, objectID: ObjectPath(gen: gen, flow: flow, step: step, frame: index, scope: scopeIndex).id,
                               name: scope.name)
                },
                thisValue: RemoteValue(type: "undefined", valuePreview: "undefined"))
        }
        var exception: RemoteValue?
        if let thrown = spec.throwing, reason == .exception {
            exception = remote(thrown, objectID: ObjectPath(gen: gen, flow: flow, step: step, frame: -1, scope: 0).id)
        }
        return DebugPauseState(targetID: flow.targetID, gen: gen, paused: true, reason: reason, hitBreakpoints: hitBreakpoints,
                               callFrames: frames, exception: exception)
    }

    /// The step of `flow` whose innermost frame is at `script`:`line`.
    public static func stepIndex(_ flow: Flow, script: Script, line: Int) -> Int? {
        steps(flow).firstIndex { $0.frames.first?.script == script && $0.frames.first?.line == line }
    }

    static func scopes(for frame: FrameSpec) -> [(type: String, name: String?, fields: [Field])] {
        let globals = frame.script == .main ? nodeGlobals : windowGlobals
        let enclosing: (type: String, name: String?, fields: [Field]) = frame.script == .main
            ? ("closure", nil, moduleFields(.main))
            : ("module", ScriptURL.fileName(frame.script.url), moduleFields(frame.script))
        return [("local", nil, frame.locals), enclosing, ("global", nil, globals)]
    }

    /// Where an object ID points: a scope of a frame of a pause, then property names.
    struct ObjectPath: Equatable {
        var gen: Int
        var flow: Flow
        var step: Int
        /// -1 for the thrown exception.
        var frame: Int
        var scope: Int
        var names: [String] = []

        var id: String { (["o", String(gen), flow.rawValue, String(step), String(frame), String(scope)] + names).joined(separator: "~") }

        init(gen: Int, flow: Flow, step: Int, frame: Int, scope: Int, names: [String] = []) {
            self.gen = gen
            self.flow = flow
            self.step = step
            self.frame = frame
            self.scope = scope
            self.names = names
        }

        init?(_ id: String) {
            let parts = id.components(separatedBy: "~")
            guard parts.count >= 6, parts[0] == "o", let gen = Int(parts[1]), let flow = Flow(rawValue: parts[2]),
                  let step = Int(parts[3]), let frame = Int(parts[4]), let scope = Int(parts[5]) else { return nil }
            self.init(gen: gen, flow: flow, step: step, frame: frame, scope: scope, names: Array(parts.dropFirst(6)))
        }

        func appending(_ name: String) -> ObjectPath {
            var copy = self
            copy.names.append(name)
            return copy
        }
    }

    /// The value an object ID names.
    static func value(at path: ObjectPath) -> Value? {
        let flowSteps = steps(path.flow)
        guard flowSteps.indices.contains(path.step) else { return nil }
        let step = flowSteps[path.step]
        var current: Value
        if path.frame == -1 {
            guard let thrown = step.throwing else { return nil }
            current = thrown
        } else {
            guard step.frames.indices.contains(path.frame) else { return nil }
            let frameScopes = scopes(for: step.frames[path.frame])
            guard frameScopes.indices.contains(path.scope) else { return nil }
            current = .object(className: "Object", frameScopes[path.scope].fields, plain: true)
        }
        for name in path.names {
            guard let next = fields(of: current, global: step.frames.first?.script == .main ? nodeGlobals : windowGlobals)
                .first(where: { $0.name == name })?.value else { return nil }
            current = next
        }
        return current
    }

    static func fields(of value: Value, global: [Field]) -> [Field] {
        switch value {
        case .object(_, let fields, _): fields
        case .array(let items): items.enumerated().map { Field(String($0.offset), $0.element) } + [Field("length", .number(Double(items.count)))]
        case .error(let className, let message, let stack):
            [Field("message", .string(message)), Field("stack", .string(stack)), Field("name", .string(className))]
        case .global: global
        default: []
        }
    }

    /// Properties of the object `objectID` names, CDP order, with `[[Prototype]]` last for objects.
    public static func properties(objectID: String) -> [DebugProperty]? {
        guard let path = ObjectPath(objectID), let value = value(at: path) else { return nil }
        let globals = path.flow == .print ? nodeGlobals : windowGlobals
        var props = fields(of: value, global: globals).map { field in
            DebugProperty(name: field.name, writable: true, value: remote(field.value, objectID: path.appending(field.name).id))
        }
        let isScope = path.names.isEmpty && path.frame >= 0
        if !isScope {
            switch value {
            case .object(let className, _, _):
                props.append(DebugProperty(name: "[[Prototype]]", kind: DebugPropertyKind.internal.rawValue,
                                           value: RemoteValue(type: "object", className: className == "Object" ? "Object" : className,
                                                              valuePreview: className == "Object" ? "Object" : className, hasChildren: false)))
            case .array:
                props.append(DebugProperty(name: "[[Prototype]]", kind: DebugPropertyKind.internal.rawValue,
                                           value: RemoteValue(type: "object", subtype: "array", className: "Array", valuePreview: "Array(0)",
                                                              hasChildren: false)))
            default:
                break
            }
        }
        return props
    }

    // MARK: Remote values

    /// The RemoteValue of `value` with the preview rules of local-api.md §9.
    public static func remote(_ value: Value, objectID: String?) -> RemoteValue {
        switch value {
        case .string(let s):
            return RemoteValue(type: "string", valuePreview: clip(quoted(s)))
        case .number(let n):
            return RemoteValue(type: "number", valuePreview: number(n))
        case .bool(let b):
            return RemoteValue(type: "boolean", valuePreview: b ? "true" : "false")
        case .null:
            return RemoteValue(type: "object", subtype: "null", valuePreview: "null")
        case .undefined:
            return RemoteValue(type: "undefined", valuePreview: "undefined")
        case .function(let signature):
            return RemoteValue(type: "function", className: "Function", valuePreview: signature)
        case .object(let className, let fields, let plain):
            let head = plain ? "" : className + " "
            return RemoteValue(type: "object", className: className, valuePreview: clip(head + objectPreview(fields)), objectID: objectID,
                               hasChildren: !fields.isEmpty)
        case .array(let items):
            return RemoteValue(type: "object", subtype: "array", className: "Array",
                               valuePreview: clip("Array(\(items.count)) [" + items.prefix(5).map(short).joined(separator: ", ")
                                                  + (items.count > 5 ? ", …" : "") + "]"),
                               objectID: objectID, hasChildren: !items.isEmpty)
        case .error(let className, let message, _):
            return RemoteValue(type: "object", subtype: "error", className: className, valuePreview: clip("\(className): \(message)"),
                               objectID: objectID)
        case .global:
            return RemoteValue(type: "object", className: "Window", valuePreview: "Window", objectID: objectID)
        }
    }

    private static func objectPreview(_ fields: [Field]) -> String {
        let shown = fields.prefix(5).map { "\($0.name): \(short($0.value))" }
        return "{" + shown.joined(separator: ", ") + (fields.count > 5 ? ", …" : "") + "}"
    }

    /// A value inside an object preview: primitives in full, objects as `{…}`, arrays as `Array(n)`.
    private static func short(_ value: Value) -> String {
        switch value {
        case .string(let s): quoted(s.count > 40 ? String(s.prefix(40)) + "…" : s)
        case .number(let n): number(n)
        case .bool(let b): b ? "true" : "false"
        case .null: "null"
        case .undefined: "undefined"
        case .function: "ƒ"
        case .object(let className, _, let plain): plain ? "{…}" : className
        case .array(let items): "Array(\(items.count))"
        case .error(let className, _, _): className
        case .global: "Window"
        }
    }

    private static func quoted(_ s: String) -> String {
        let escaped = s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\n", with: "\\n")
        return "\"" + escaped + "\""
    }

    private static func number(_ n: Double) -> String {
        n.rounded() == n && abs(n) < 1e15 ? String(Int(n)) : String(n)
    }

    private static func clip(_ text: String) -> String {
        text.count > 200 ? String(text.prefix(199)) + "…" : text
    }

    // MARK: Evaluation

    /// Evaluates a variable path (`order.lines[0].name`), a number or a quoted string in the scopes
    /// of `frame`, or in the global scope with object IDs that stay valid when `frame` is nil.
    static func evaluate(_ expression: String, frame: ObjectPath?, flow: Flow) -> DebugEvaluation {
        let text = expression.trimmingCharacters(in: .whitespacesAndNewlines)
        if let number = Double(text) { return DebugEvaluation(value: remote(.number(number), objectID: nil)) }
        if text.count >= 2, let first = text.first, first == text.last, first == "\"" || first == "'" {
            return DebugEvaluation(value: remote(.string(String(text.dropFirst().dropLast())), objectID: nil))
        }
        let names = text.replacingOccurrences(of: "[", with: ".").replacingOccurrences(of: "]", with: "")
            .split(separator: ".", omittingEmptySubsequences: false).map { $0.trimmingCharacters(in: .whitespaces) }
        let identifier = try? NSRegularExpression(pattern: #"^[A-Za-z_$][\w$]*$|^\d+$"#)
        let valid = !names.isEmpty && names.allSatisfy { name in
            identifier?.firstMatch(in: name, range: NSRange(name.startIndex..., in: name)) != nil
        }
        guard valid, let head = names.first else {
            return thrown("EvalError", "미리보기 데몬은 변수 경로(예: order.total), 숫자, 문자열만 평가합니다")
        }
        let roots: [ObjectPath] = frame.map { frame in
            (0..<3).map { ObjectPath(gen: frame.gen, flow: frame.flow, step: frame.step, frame: frame.frame, scope: $0) }
        } ?? [ObjectPath(gen: -1, flow: flow, step: 0, frame: 0, scope: 2)]
        let globals = flow == .print ? nodeGlobals : windowGlobals
        for root in roots {
            guard let scope = value(at: root), fields(of: scope, global: globals).contains(where: { $0.name == head }) else { continue }
            var path = root
            var current = scope
            for name in names {
                switch current {
                case .undefined, .null:
                    let kind = current == .null ? "null" : "undefined"
                    return thrown("TypeError", "Cannot read properties of \(kind) (reading '\(name)')")
                default:
                    break
                }
                path = path.appending(name)
                current = fields(of: current, global: globals).first(where: { $0.name == name })?.value ?? .undefined
            }
            return DebugEvaluation(value: remote(current, objectID: path.id))
        }
        return thrown("ReferenceError", "\(head) is not defined")
    }

    /// Evaluates `expression` in frame `frame` of a pause made by `pauseState(flow:step:gen:reason:)`.
    public static func evaluate(_ expression: String, flow: Flow, step: Int, gen: Int, frame: Int) -> DebugEvaluation {
        evaluate(expression, frame: ObjectPath(gen: gen, flow: flow, step: step, frame: frame, scope: 0), flow: flow)
    }

    private static func thrown(_ className: String, _ message: String) -> DebugEvaluation {
        DebugEvaluation(value: RemoteValue(type: "object", subtype: "error", className: className, valuePreview: "\(className): \(message)"),
                        exception: ExceptionInfo(text: "Uncaught \(className): \(message)", line: 1, column: 1))
    }

    // MARK: Console

    /// Messages a launched session prints before any flow runs, seconds after launch.
    public static func launchConsole() -> [(after: Double, target: String, source: ConsoleSource, level: ConsoleLevel, text: String,
                                            script: Script?, line: Int?)] {
        [
            (0.4, mainTargetID, .console, .info, "Shop 관리자 1.18.0 시작 (Electron 33.2.0, Node 20.18.1)", .main, Script.main.line("console.info(`Shop")),
            (0.9, mainTargetID, .console, .log, "창을 만들었습니다: 1280×800", .main, Script.main.line("console.log(`창을")),
            (1.6, pageTargetID, .browser, .warning,
             "Electron Security Warning (Insecure Content-Security-Policy) This renderer process has either no Content Security Policy set or a policy with \"unsafe-eval\" enabled.",
             nil, nil),
            (1.8, pageTargetID, .console, .log, "결제 화면을 불러왔습니다", .index, Script.index.line("console.log('결제 화면")),
            (2.1, pageTargetID, .console, .debug, "[cart] 품목 3개, 합계 52,000원", .checkout, Script.checkout.line("return cart.reduce")),
            (3.4, pageTargetID, .console, .warning, "쿠폰 서버 응답이 느립니다 (1,820ms)", .api, Script.api.line("console.warn(`쿠폰")),
        ]
    }

    /// The uncaught exception of the coupon flow as a console entry.
    static func couponException(seq: Int, time: Timestamp) -> ConsoleEntry {
        let api = Script.api
        let checkout = Script.checkout
        let index = Script.index
        let throwLine = api.line("const amount = order.summary.total")
        return ConsoleEntry(
            seq: seq, time: time, targetID: pageTargetID, source: .exception, level: .error,
            text: "Uncaught TypeError: Cannot read properties of undefined (reading 'total')",
            scriptID: api.scriptID, url: api.url, line: throwLine, column: 32,
            stack: [
                StackFrameInfo(function: "applyDiscount", scriptID: api.scriptID, url: api.url, line: throwLine, column: 32),
                StackFrameInfo(function: "applyCoupon", scriptID: checkout.scriptID, url: checkout.url, line: checkout.line("return applyDiscount"),
                               column: 10),
                StackFrameInfo(function: "HTMLButtonElement.<anonymous>", scriptID: index.scriptID, url: index.url,
                               line: index.line("applyCoupon(document"), column: 3),
            ])
    }

    /// Console entries of a session that launched at `start` and ran the submit flow and the coupon flow once.
    public static func consoleEntries(start: Date) -> [ConsoleEntry] {
        var entries = launchConsole().enumerated().map { index, item in
            ConsoleEntry(seq: index + 1, time: Timestamp(date: start.addingTimeInterval(item.after)), targetID: item.target,
                         source: item.source, level: item.level, text: item.text, scriptID: item.script?.scriptID, url: item.script?.url,
                         line: item.line,
                         column: item.line.map { item.script?.column($0) ?? 1 })
        }
        let checkout = Script.checkout
        let main = Script.main
        entries.append(ConsoleEntry(seq: entries.count + 1, time: Timestamp(date: start.addingTimeInterval(42.6)), targetID: pageTargetID,
                                    level: .log, text: "주문 제출 ORD-2026-1021-0042 46,800원", scriptID: checkout.scriptID, url: checkout.url,
                                    line: checkout.line("console.log('주문 제출'"), column: 3))
        entries.append(ConsoleEntry(seq: entries.count + 1, time: Timestamp(date: start.addingTimeInterval(43.9)), targetID: mainTargetID,
                                    level: .log, text: "영수증 R-0042 출력 → EPSON TM-T88VI", scriptID: main.scriptID, url: main.url,
                                    line: main.line("console.log(`영수증"), column: 3))
        entries.append(couponException(seq: entries.count + 1, time: Timestamp(date: start.addingTimeInterval(71.2))))
        return entries
    }

    // MARK: Sessions

    /// A launched Shop Admin session on `hostID`.
    public static func session(hostID: String, debugID: String = "d-shop-admin", created: Date, state: DebugSessionState = .running,
                               targets: [DebugTarget]? = nil, breakpoints: [DebugBreakpoint] = [], exceptions: ExceptionPauseMode = .uncaught,
                               breakpointsActive: Bool = true, closeReason: DebugCloseReason? = nil, exitCode: Int? = nil,
                               createdBy: DebugActor = DebugActor(actor: "human", clientName: "Deskhand"),
                               mainUnavailable: MainUnavailableReason? = nil, rev: Int = 1) -> DebugSession {
        DebugSession(debugID: debugID, hostID: hostID, jobID: "j-debug-1", kind: .launch, name: appName, path: appPath,
                     args: ["--env=staging"], cwd: "C:\\Shop\\desktop", pid: appPID,
                     ports: DebugPorts(inspect: mainUnavailable == nil ? inspectPort : nil, remoteDebugging: devtoolsPort),
                     state: state, targets: targets ?? Self.targets(), mainUnavailable: mainUnavailable, breakpoints: breakpoints,
                     breakpointsActive: breakpointsActive, pauseOnExceptions: exceptions, createdBy: createdBy, createdAt: Timestamp(date: created),
                     closedAt: state == .closed ? Timestamp(date: created.addingTimeInterval(240)) : nil, closeReason: closeReason,
                     exitCode: exitCode, rev: rev)
    }

    /// Breakpoints of the sample sessions: on `postOrder`'s payload (resolved), on the order total
    /// with a condition (resolved), a disabled one, and one on a script that has not loaded.
    public static func sampleBreakpoints() -> [DebugBreakpoint] {
        let api = Script.api
        let checkout = Script.checkout
        let payloadLine = api.line("const payload = {")
        let orderLine = checkout.line("console.log('주문 제출'")
        return [
            DebugBreakpoint(breakpointID: "bp-1", match: .fileName, pattern: "api.js", line: payloadLine,
                            locations: [BreakpointLocation(targetID: pageTargetID, scriptID: api.scriptID, url: api.url, line: payloadLine,
                                                           column: api.column(payloadLine))]),
            DebugBreakpoint(breakpointID: "bp-2", match: .fileName, pattern: "checkout.js", line: orderLine - 1, condition: "order.total > 40000",
                            locations: [BreakpointLocation(targetID: pageTargetID, scriptID: checkout.scriptID, url: checkout.url,
                                                           line: orderLine, column: checkout.column(orderLine))]),
            DebugBreakpoint(breakpointID: "bp-3", match: .url, pattern: Script.main.url, line: Script.main.line("const printer = process.env"),
                            enabled: false,
                            locations: [BreakpointLocation(targetID: mainTargetID, scriptID: Script.main.scriptID, url: Script.main.url,
                                                           line: Script.main.line("const printer = process.env"), column: 3)]),
            DebugBreakpoint(breakpointID: "bp-4", match: .fileName, pattern: "settings.js", line: 12),
        ]
    }
}
