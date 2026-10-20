import DeskhandClient
import Foundation

/// Sample remote folders, local folders for snapshot scenes and transfers in every state.
extension PreviewFixtures {
    /// A remote folder with 3,000 items, listed in pages.
    public static let largeFolder = "C:\\qa\\captures"
    /// A remote folder whose listing the agent refuses.
    public static let deniedFolder = "C:\\Windows\\System32\\config"
    /// Remote files another process holds open: a move copies them but cannot delete the source.
    public static let lockedLog = "C:\\Shop\\logs\\shop-2026-10-20.log"

    public static func remoteHome(user: String) -> String { "C:\\Users\\\(user)" }

    /// A folder with long Korean, emoji and mixed-script names, under a deep path.
    public static func longNamesFolder(user: String) -> String {
        remoteHome(user: user) + "\\Documents\\2026년 하반기 결제 시스템 개편 — 단계별 QA 증적 자료 모음 (최종 검토 전 공유용, 외부 반출 금지)\\2차 회귀 테스트\\스크린샷과 로그"
    }

    /// A file name longer than 200 characters.
    public static let longFileName = "2026년 10월 20일 결제 승인 실패 재현 — 카드사 응답이 30초 넘게 늦어지면 주문 확인 화면이 멈추는 문제 (Windows 11 24H2, 125% 배율, 듀얼 모니터, 재현율 약 30퍼센트, 화면 녹화·네트워크 로그·덤프 포함, QA팀 1차 확인 완료 후 개발팀 전달 예정, 재현 영상 3개·HAR 파일 2개 동봉, 고객 문의 CS-2026-10-0342 연결, 담당 양태영).zip"

    /// Remote folders by path for a Windows computer whose user is `user`. Times are relative to `now`.
    public static func remoteFolders(user: String, now: Date = .now) -> [String: [FileEntry]] {
        func ago(_ hours: Double) -> Timestamp { Timestamp(date: now.addingTimeInterval(-hours * 3600)) }
        func dir(_ folder: String, _ name: String, _ hours: Double, hidden: Bool = false) -> FileEntry {
            FileEntry(name: name, path: folder + (folder.hasSuffix("\\") ? "" : "\\") + name, dir: true, size: 0,
                      mtime: ago(hours), link: false, hidden: hidden ? true : nil)
        }
        func file(_ folder: String, _ name: String, _ size: Int, _ hours: Double, hidden: Bool = false, readonly: Bool = false) -> FileEntry {
            FileEntry(name: name, path: folder + (folder.hasSuffix("\\") ? "" : "\\") + name, dir: false, size: size,
                      mtime: ago(hours), link: false, hidden: hidden ? true : nil, readonly: readonly ? true : nil)
        }
        let root = "C:\\"
        let users = "C:\\Users"
        let home = remoteHome(user: user)
        let downloads = home + "\\Downloads"
        let documents = home + "\\Documents"
        let desktop = home + "\\Desktop"
        let longFolder = longNamesFolder(user: user)
        let longParent = FilePath.parent(longFolder, style: .windows) ?? documents
        let longGrandparent = FilePath.parent(longParent, style: .windows) ?? documents
        let shop = "C:\\Shop"
        let logs = "C:\\Shop\\logs"
        let qa = "C:\\qa"
        let windows = "C:\\Windows"
        let system32 = "C:\\Windows\\System32"
        let programData = "C:\\ProgramData"
        let crashDumps = "C:\\ProgramData\\Shop\\CrashDumps"

        var folders: [String: [FileEntry]] = [:]
        folders[root] = [
            dir(root, "PerfLogs", 900), dir(root, "Program Files", 30), dir(root, "Program Files (x86)", 30),
            dir(root, "ProgramData", 2, hidden: true), dir(root, "qa", 1), dir(root, "Shop", 3),
            dir(root, "System Volume Information", 400, hidden: true), dir(root, "Users", 700), dir(root, "Windows", 20),
            file(root, "pagefile.sys", 17_179_869_184, 1, hidden: true), file(root, "DumpStack.log.tmp", 12_288, 1, hidden: true),
        ]
        folders[users] = [dir(users, user, 1), dir(users, "Public", 700), dir(users, "Default", 900, hidden: true)]
        folders[home] = [
            dir(home, ".vscode", 50, hidden: true), dir(home, "AppData", 1, hidden: true), dir(home, "Desktop", 5),
            dir(home, "Documents", 4), dir(home, "Downloads", 0.2), dir(home, "Music", 300), dir(home, "Pictures", 30),
            dir(home, "source", 2), dir(home, "Videos", 300),
            file(home, "NTUSER.DAT", 3_407_872, 0.5, hidden: true), file(home, ".gitconfig", 214, 90, hidden: true),
        ]
        folders[desktop] = [
            file(desktop, "Shop.lnk", 1_402, 200), file(desktop, "작업 메모.txt", 2_381, 5),
            file(desktop, "Visual Studio 2022.lnk", 1_388, 400),
        ]
        folders[downloads] = [
            file(downloads, "Deskhand-Setup-0.4.2.msi", 84_301_568, 0.3),
            file(downloads, "Shop-build-1187.zip", 412_088_320, 26),
            file(downloads, "report.html", 48_210, 0.8),
            file(downloads, "VC_redist.x64.exe", 25_640_448, 300),
            file(downloads, "dotnet-sdk-9.0.100-win-x64.exe", 236_451_840, 280),
            file(downloads, "Screenshot 2026-10-20 151203.png", 1_204_551, 2.4),
            file(downloads, "🚀 출시 체크리스트.md", 6_120, 3),
            file(downloads, "주문 확인 화면 오류 재현 절차.docx", 88_004, 22),
            file(downloads, "checkout-trace.etl", 61_440_000, 4),
            dir(downloads, "로그 모음", 6), dir(downloads, "빈 폴더", 30),
        ]
        folders[downloads + "\\로그 모음"] = [
            file(downloads + "\\로그 모음", "shop-2026-10-18.log", 2_204_112, 50),
            file(downloads + "\\로그 모음", "shop-2026-10-19.log", 3_918_002, 26),
            file(downloads + "\\로그 모음", "installer.log", 121_880, 6),
        ]
        folders[downloads + "\\빈 폴더"] = []
        folders[documents] = [
            dir(documents, "Visual Studio 2022", 400), dir(documents, "2026년 하반기 결제 시스템 개편 — 단계별 QA 증적 자료 모음 (최종 검토 전 공유용, 외부 반출 금지)", 3),
            file(documents, "회의록 2026-10-20 최종본(수정).docx", 41_222, 7), file(documents, "견적서_v3.xlsx", 19_840, 70),
        ]
        folders[longGrandparent] = [dir(longGrandparent, "1차 기능 테스트", 300), dir(longGrandparent, "2차 회귀 테스트", 3)]
        folders[longParent] = [dir(longParent, "스크린샷과 로그", 3), file(longParent, "회귀 테스트 결과 요약.xlsx", 66_120, 5)]
        folders[longFolder] = [
            file(longFolder, longFileName, 734_003_200, 3),
            file(longFolder, "🚀 출시 체크리스트 — 최종 승인본 (결제·환불·포인트 적립 시나리오 전체 포함).md", 18_430, 4),
            file(longFolder, "📸 스크린샷 2026-10-20 오후 3.12.07 (주문 확인 화면 깨짐, 125% 배율).png", 2_811_904, 3.2),
            file(longFolder, "회의록 2026-10-20 최종본(수정)(진짜 최종)(1).docx", 52_118, 6),
            file(longFolder, "Ünïcödé façade — naïve résumé.txt", 1_024, 20),
            file(longFolder, "日本語ファイル名とハングル 파일 이름이 섞인 테스트.txt", 2_048, 21),
            file(longFolder, "a.txt", 12, 30),
            dir(longFolder, "아주 깊은 폴더 — 결제 승인 단계에서 수집한 HAR 파일과 콘솔 로그 원본 (가공 전, 개인정보 포함 가능성 있음)", 5),
            file(longFolder, "network-capture-checkout-approval-timeout-2026-10-20T15-12-07.876Z-session-4f9c2b7e.har", 9_420_118, 3),
        ]
        folders[qa] = [dir(qa, "captures", 0.5), dir(qa, "reports", 2), dir(qa, "scenarios", 48), dir(qa, "uploads", 1)]
        folders[qa + "\\reports"] = [
            file(qa + "\\reports", "checkout-report.html", 120_400, 2), file(qa + "\\reports", "checkout-report.json", 88_410, 2),
        ]
        folders[qa + "\\scenarios"] = [
            file(qa + "\\scenarios", "checkout.yaml", 4_210, 48), file(qa + "\\scenarios", "refund.yaml", 3_118, 72),
        ]
        folders[qa + "\\uploads"] = []
        folders[largeFolder] = (1...3000).map { n in
            let name = String(format: "capture-%04d.png", n)
            return file(largeFolder, name, 180_000 + (n * 7_919) % 1_400_000, 0.5 + Double(3000 - n) / 120)
        }
        folders[shop] = [dir(shop, "bin", 3), dir(shop, "config", 26), dir(shop, "logs", 0.1)]
        folders[shop + "\\bin"] = [
            file(shop + "\\bin", "Shop.exe", 48_234_496, 3), file(shop + "\\bin", "Shop.dll", 6_291_456, 3),
            file(shop + "\\bin", "appsettings.json", 2_210, 26), file(shop + "\\bin", "restart-shop.ps1", 940, 50),
        ]
        folders[shop + "\\config"] = [file(shop + "\\config", "shop.ini", 1_812, 26, readonly: true)]
        folders[logs] = [
            file(logs, "shop-2026-10-20.log", 9_812_004, 0.05), file(logs, "shop-2026-10-19.log", 14_220_118, 24),
            file(logs, "shop-2026-10-18.log", 12_004_771, 48), file(logs, "installer.log", 121_880, 3),
        ]
        folders[windows] = [
            dir(windows, "System32", 20), dir(windows, "Temp", 0.5), file(windows, "MEMORY.DMP", 2_147_483_648, 28),
        ]
        folders[system32] = [dir(system32, "config", 20), dir(system32, "drivers", 20), file(system32, "notepad.exe", 360_448, 400)]
        folders[programData] = [dir(programData, "Microsoft", 300), dir(programData, "Shop", 30)]
        folders[programData + "\\Shop"] = [dir(programData + "\\Shop", "CrashDumps", 1)]
        let wer = programData + "\\Microsoft\\Windows\\WER"
        folders[programData + "\\Microsoft"] = [dir(programData + "\\Microsoft", "Windows", 300)]
        folders[programData + "\\Microsoft\\Windows"] = [dir(programData + "\\Microsoft\\Windows", "WER", 52)]
        folders[wer] = [dir(wer, "ReportArchive", 52), dir(wer, "ReportQueue", 400)]
        folders[wer + "\\ReportQueue"] = []
        folders[wer + "\\ReportArchive"] = [dir(wer + "\\ReportArchive", FilePath.lastComponent(crashReportFolder, style: .windows), 52)]
        folders[crashReportFolder] = [file(crashReportFolder, "Report.wer", 41_872, 52)]
        let appData = home + "\\AppData"
        folders[appData] = [dir(appData, "Local", 1), dir(appData, "Roaming", 3)]
        folders[appData + "\\Roaming"] = []
        folders[appData + "\\Local"] = [dir(appData + "\\Local", "CrashDumps", 19.4), dir(appData + "\\Local", "Temp", 0.2)]
        folders[appData + "\\Local\\Temp"] = []
        folders[appData + "\\Local\\CrashDumps"] = [file(appData + "\\Local\\CrashDumps", "dotnet.exe.7712.dmp", 1_482_418_176, 19.4)]
        folders[crashDumps] = (1...7).map { n in
            file(crashDumps, "Shop.exe.\(4_000 + n * 13).dmp", 48_000_000 + n * 3_100_000, Double(n))
        }
        return folders
    }

    /// Folders the agent refuses to list, with its message.
    public static let deniedFolders: [String: String] = [
        deniedFolder: "‘config’ 폴더에 접근할 권한이 없습니다. 관리자 계정으로 연결한 에이전트만 이 폴더를 읽을 수 있습니다",
        "C:\\System Volume Information": "‘System Volume Information’ 폴더에 접근할 권한이 없습니다",
    ]

    // MARK: Local folders for snapshot scenes

    public static let localBuildFolder = "/Users/taeyoung/Builds/Shop-1188"

    /// Items of a folder of this Mac, for snapshot scenes that must not read the disk.
    public static func localItems(now: Date = .now) -> [FileItem] {
        func ago(_ hours: Double) -> Date { now.addingTimeInterval(-hours * 3600) }
        let folder = localBuildFolder
        func item(_ name: String, dir: Bool = false, size: Int? = nil, _ hours: Double) -> FileItem {
            FileItem(name: name, path: folder + "/" + name, isDirectory: dir, size: size, modified: ago(hours))
        }
        return [
            item("QA 캡처 10월", dir: true, 4),
            item("symbols", dir: true, 1.2),
            item("Shop-Setup-1188-x64.msi", size: 1_203_486_720, 0.6),
            item("Shop-1188-symbols.zip", size: 318_767_104, 0.6),
            item("Deskhand-agent-0.1.0.zip", size: 14_221_312, 26),
            item("release-notes.md", size: 8_402, 0.7),
            item("checksums.txt", size: 412, 0.6),
            item("restart-shop.ps1", size: 940, 50),
            FileItem(name: "Shop.app", path: folder + "/Shop.app", isDirectory: true, size: 92_110_848, modified: ago(3),
                     isPackage: true),
            FileItem(name: ".DS_Store", path: folder + "/.DS_Store", isDirectory: false, size: 6_148, modified: ago(1), isHidden: true),
        ]
    }

    /// Local items with long names, for the long-names scene.
    public static func localLongItems(now: Date = .now) -> [FileItem] {
        let folder = "/Users/taeyoung/Documents/고객사 전달 자료/2026년 하반기 결제 시스템 개편 — 최종 산출물 (검토 완료본)"
        func ago(_ hours: Double) -> Date { now.addingTimeInterval(-hours * 3600) }
        let names: [(String, Int?)] = [
            ("결제 시스템 개편 최종 산출물 — 설치 파일, 릴리스 노트, 체크섬, 서명 확인서, 고객사 배포 승인 메일 원문, QA 결과 보고서 전체 묶음 (2026년 10월 20일 기준, 외부 반출 시 보안팀 승인 필요, 문의는 결제플랫폼팀 양태영).zip", 1_884_102_656),
            ("🎉 배포 완료 축하 메시지 모음.txt", 3_210),
            ("📦 Shop-Setup-1188-x64 (서명됨).msi", 1_203_486_720),
            ("회의록 2026-10-20 최종본(수정)(진짜 최종)(1).docx", 52_118),
            ("README.md", 2_204),
        ]
        return names.map { name, size in
            FileItem(name: name, path: folder + "/" + name, isDirectory: false, size: size, modified: ago(3))
        }
    }

    // MARK: Transfers

    /// Transfers of the development PC in every state: running, paused, verifying, a move that
    /// kept its source, a partial success, a failure and a finished push.
    public static func transfers(hostID: String = devPCID, user: String = "taeyoung", localHome: String = "/Users/taeyoung") -> [Transfer] {
        let downloads = remoteHome(user: user) + "\\Downloads"
        func files(_ count: Int, prefix: String, size: Int, done: Int, ext: String = "png") -> [TransferFile] {
            (1...count).map { n in
                let finished = n <= done
                return TransferFile(rel: String(format: "\(prefix)-%02d.\(ext)", n), size: size, bytesDone: finished ? size : 0,
                                    state: finished ? "done" : "queued", outcome: finished ? "written" : nil,
                                    sha256: finished ? String(format: "%08lx9c1f07d24ab2e8f1", n * 2_654_435) : nil)
            }
        }
        var partialFiles = files(24, prefix: "QA 캡처", size: 1_480_000, done: 24)
        partialFiles[6].outcome = "renamed"
        partialFiles[6].rel = "QA 캡처-07 (2).png"
        partialFiles[11].outcome = "skipped"
        partialFiles[11].sha256 = nil
        partialFiles[17] = TransferFile(rel: "QA 캡처-18.png", size: 1_480_000, bytesDone: 0, state: "failed", outcome: "failed",
                                        error: EnvelopeError(code: "PERMISSION_DENIED", message: "액세스가 거부되었습니다. 대상 폴더에 쓰기 권한이 없습니다"))
        partialFiles[22] = TransferFile(rel: "원본/2026년 10월 결제 승인 실패 재현 화면 녹화 원본 — 편집 전 (개인정보 가림 처리 안 됨).mov", size: 1_480_000,
                                        bytesDone: 0, state: "failed", outcome: "failed",
                                        error: EnvelopeError(code: "INVALID_ARGUMENT", message: "대상 경로가 Windows 경로 길이 한도를 넘습니다. 폴더 이름을 줄이세요"))
        let pausedFiles = files(7, prefix: "Shop.exe", size: 54_000_000, done: 3, ext: "dmp")
        return [
            Transfer(transferID: "t-preview-running", hostID: hostID, direction: "push",
                     local: localHome + "/Builds/Shop-1188/Shop-Setup-1188-x64.msi",
                     remote: downloads + "\\Shop-Setup-1188-x64.msi", onConflict: "fail", state: "running",
                     bytesDone: 457_324_953, bytesTotal: 1_203_486_720, filesDone: 0, filesTotal: 1, rateBps: 46_137_344,
                     files: [TransferFile(rel: "Shop-Setup-1188-x64.msi", size: 1_203_486_720, bytesDone: 457_324_953, state: "running")]),
            Transfer(transferID: "t-preview-paused", hostID: hostID, direction: "pull",
                     local: localHome + "/Downloads/CrashDumps", remote: "C:\\ProgramData\\Shop\\CrashDumps", onConflict: "fail",
                     state: "paused", bytesDone: 162_000_000, bytesTotal: 378_000_000, filesDone: 3, filesTotal: 7, rateBps: 0,
                     error: EnvelopeError(code: "OFFLINE", message: "연결이 끊겨 멈췄습니다."), files: pausedFiles),
            Transfer(transferID: "t-preview-retained", hostID: hostID, direction: "move_pull",
                     local: localHome + "/Downloads/shop-2026-10-19.log", remote: "C:\\Shop\\logs\\shop-2026-10-19.log",
                     onConflict: "fail", state: "done", bytesDone: 14_220_118, bytesTotal: 14_220_118, filesDone: 1, filesTotal: 1,
                     rateBps: 0, outcome: "copied_source_retained",
                     error: EnvelopeError(code: "PERMISSION_DENIED", message: "Shop.exe가 이 파일을 열어 두고 있어 원격 PC의 원본을 지우지 못했습니다."),
                     files: [TransferFile(rel: "shop-2026-10-19.log", size: 14_220_118, bytesDone: 14_220_118, state: "done",
                                          outcome: "copied_source_retained", sha256: "4f9c2b7ee21a90c3",
                                          error: EnvelopeError(code: "PERMISSION_DENIED", message: "원본 삭제 실패: 다른 프로세스가 파일을 사용 중입니다"))]),
            Transfer(transferID: "t-preview-partial", hostID: hostID, direction: "push",
                     local: localHome + "/Desktop/QA 캡처 10월", remote: "C:\\qa\\uploads\\QA 캡처 10월", onConflict: "rename",
                     state: "done", bytesDone: 32_560_000, bytesTotal: 35_520_000, filesDone: 22, filesTotal: 24, rateBps: 0,
                     outcome: "partial", files: partialFiles),
            Transfer(transferID: "t-preview-failed", hostID: hostID, direction: "pull",
                     local: localHome + "/Downloads/MEMORY.DMP", remote: "C:\\Windows\\MEMORY.DMP", onConflict: "fail",
                     state: "failed", bytesDone: 0, bytesTotal: 2_147_483_648, filesDone: 0, filesTotal: 1, rateBps: 0,
                     outcome: "failed", error: EnvelopeError(code: "ELEVATION_REQUIRED", message: "관리자 권한이 필요한 파일입니다."),
                     files: [TransferFile(rel: "MEMORY.DMP", size: 2_147_483_648, bytesDone: 0, state: "failed", outcome: "failed",
                                          error: EnvelopeError(code: "ELEVATION_REQUIRED", message: "관리자 권한이 필요한 파일입니다."))]),
            Transfer(transferID: "t-preview-done", hostID: hostID, direction: "push",
                     local: localHome + "/Downloads/Deskhand-agent-0.1.0.zip", remote: downloads + "\\Deskhand-agent-0.1.0.zip",
                     onConflict: "fail", state: "done", bytesDone: 14_221_312, bytesTotal: 14_221_312, filesDone: 1, filesTotal: 1,
                     rateBps: 0, outcome: "written",
                     files: [TransferFile(rel: "Deskhand-agent-0.1.0.zip", size: 14_221_312, bytesDone: 14_221_312, state: "done",
                                          outcome: "written", sha256: "9c1f07d24ab2e8f1")]),
        ]
    }

    /// A transfer in its verifying step, for snapshot scenes.
    public static func verifyingTransfer(hostID: String = devPCID, localHome: String = "/Users/taeyoung") -> Transfer {
        Transfer(transferID: "t-preview-verifying", hostID: hostID, direction: "move_push",
                 local: localHome + "/Builds/Shop-1188/Shop-1188-symbols.zip", remote: "C:\\Shop\\symbols\\Shop-1188-symbols.zip",
                 onConflict: "replace", state: "verifying", bytesDone: 318_767_104, bytesTotal: 318_767_104, filesDone: 0,
                 filesTotal: 1, rateBps: 0,
                 files: [TransferFile(rel: "Shop-1188-symbols.zip", size: 318_767_104, bytesDone: 318_767_104, state: "verifying")])
    }
}
