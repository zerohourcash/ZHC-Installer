import SwiftUI
import AppKit

struct InstallerEvent: Decodable {
    let type: String
    var phase: String?
    var done: Int64?
    var total: Int64?
    var speed: Double?
    var message: String?
}

@MainActor final class InstallerModel: ObservableObject {
    @Published var running = false
    @Published var complete = false
    @Published var phase = "welcome"
    @Published var done: Int64 = 0
    @Published var total: Int64 = 0
    @Published var speed: Double = 0
    @Published var error: String?
    @Published var detail = "Данные блокчейна и приложение Evolution 1.0.0"
    @Published var log = ""
    @Published var keepArchive = false
    @Published var confirmedReplacement = false
    @Published var cancelling = false
    private var process: Process?
    private var receivedCompletion = false
    private var lastStage = 0
    let home = FileManager.default.homeDirectoryForCurrentUser
    var dataDirectory: URL { home.appendingPathComponent("Library/Application Support/ZHCASH") }
    var nodeDirectory: URL { home.appendingPathComponent("Applications") }
    var installedApp: URL { nodeDirectory.appendingPathComponent("ZHCASH Evolution.app") }
    var logURL: URL { home.appendingPathComponent("Library/Logs/ZHC Installer/install.log") }
    let stages = ["Подготовка", "Данные блокчейна", "Проверка SHA-256", "Распаковка", "Приложение", "Запуск ноды"]
    var stageIndex: Int {
        if complete { return 5 }
        switch phase {
        case "snapshot_download": return 1
        case "snapshot_verify": return 2
        case "snapshot_extract", "snapshot_layout_verify", "configuration_restore", "snapshot_archive_cleanup": return 3
        case "node_configuration", "node_download", "node_install": return 4
        case "node_start", "node_readiness": return 5
        case "package_verify": return lastStage >= 4 ? 4 : 0
        default: return lastStage
        }
    }
    var title: String {
        if cancelling { return "Останавливаем установку" }
        if error != nil { return "Нужен ещё один шаг" }
        if complete { return "Ваша нода на орбите" }
        if !running { return "Ваша точка\nв сети ZHCASH" }
        return ["Готовим старт", "Загружаем вселенную", "Проверяем каждый байт", "Разворачиваем блокчейн", "Устанавливаем Evolution", "Выходим на орбиту"][stageIndex]
    }
    var fraction: Double? { total > 0 ? min(1, max(0, Double(done) / Double(total))) : nil }
    func bytes(_ n: Int64) -> String { ByteCountFormatter.string(fromByteCount: n, countStyle: .file) }
    var eta: String {
        guard speed > 0, total > done else { return "—" }
        let seconds = Int(Double(total - done) / speed)
        return seconds > 60 ? "~\(seconds / 60) мин" : "~\(seconds) сек"
    }

    func start() {
        guard !running, confirmedReplacement else { return }
        error = nil; complete = false; receivedCompletion = false; lastStage = 0
        cancelling = false; phase = "initialization"; done = 0; total = 0; speed = 0; log = ""
        guard let resources = Bundle.main.resourceURL else { error = "Не найден пакет приложения."; return }
        let helper = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/zhc-installer")
        guard FileManager.default.isExecutableFile(atPath: helper.path),
              FileManager.default.fileExists(atPath: resources.appendingPathComponent("evolution-macos.zip").path) else {
            error = "Пакет установщика неполный. Загрузите DMG повторно."; return
        }
        do {
            let capacity = try home.resourceValues(forKeys: [.volumeAvailableCapacityForImportantUsageKey])
            if let free = capacity.volumeAvailableCapacityForImportantUsage, free < 40_000_000_000 {
                error = "На диске доступно \(bytes(free)). Освободите минимум 40 ГБ для загрузки и распаковки данных блокчейна."; return
            }
            try FileManager.default.createDirectory(at: logURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            try "".write(to: logURL, atomically: true, encoding: .utf8)
            let p = Process()
            p.executableURL = helper
            var environment = ProcessInfo.processInfo.environment
            environment["HOME"] = home.path
            if environment["PATH", default: ""].isEmpty {
                environment["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin"
            }
            p.environment = environment
            p.arguments = ["--macos-app", "--progress-json", "--no-wait-on-exit", "--no-install-telemetry", "--datadir", dataDirectory.path, "--node-dir", nodeDirectory.path]
            if keepArchive { p.arguments?.append("--keep-snapshot-archive") }
            let output = Pipe(), events = Pipe()
            p.standardOutput = output; p.standardError = events; p.standardInput = FileHandle.nullDevice
            process = p; running = true; detail = "Проверяем встроенный пакет Evolution"
            let group = DispatchGroup()
            func consume(_ handle: FileHandle, isEvents: Bool) {
                group.enter()
                DispatchQueue.global(qos: .utility).async { [weak self] in
                    var buffer = Data()
                    while true {
                        let part = handle.availableData
                        if part.isEmpty { break }
                        buffer.append(part)
                        while let index = buffer.firstIndex(where: { $0 == 10 || $0 == 13 }) {
                            let line = Data(buffer[..<index]); buffer.removeSubrange(...index)
                            DispatchQueue.main.async { self?.receive(line, isEvents: isEvents) }
                        }
                        if buffer.count > 65536 { buffer.removeAll() }
                    }
                    if !buffer.isEmpty {
                        let remainder = buffer
                        DispatchQueue.main.async { self?.receive(remainder, isEvents: isEvents) }
                    }
                    group.leave()
                }
            }
            try p.run()
            consume(output.fileHandleForReading, isEvents: false)
            consume(events.fileHandleForReading, isEvents: true)
            DispatchQueue.global(qos: .utility).async { [weak self] in
                p.waitUntilExit(); group.wait()
                DispatchQueue.main.async {
                    guard let self else { return }
                    self.running = false; self.process = nil
                    if self.cancelling {
                        self.complete = false
                        self.error = "Установка остановлена. Не запускайте ноду до повторной успешной установки данных блокчейна. Кошельки и конфигурация сохраняются."
                    } else if p.terminationStatus == 0 && self.receivedCompletion {
                        self.complete = true; self.phase = "completed"
                    } else if self.error == nil {
                        self.error = "Установщик завершился с кодом \(p.terminationStatus). Откройте журнал и повторите установку."
                    }
                    self.cancelling = false
                }
            }
        } catch { running = false; process = nil; self.error = error.localizedDescription }
    }

    func receive(_ data: Data, isEvents: Bool) {
        if isEvents, let event = try? JSONDecoder().decode(InstallerEvent.self, from: data) {
            if event.type == "error" { error = event.message.map(displayMessage); return }
            if event.type == "complete" { receivedCompletion = true; return }
            if event.type == "ready" { detail = event.message.map(displayMessage) ?? "RPC ноды готов"; return }
            if let newPhase = event.phase {
                if newPhase != phase { lastStage = stageIndex; done = 0; total = 0; speed = 0 }
                phase = newPhase
            }
            if event.type == "progress" { done = event.done ?? 0; total = event.total ?? 0; speed = event.speed ?? 0 }
            return
        }
        guard var line = String(data: data, encoding: .utf8), !line.isEmpty else { return }
        line = line.replacingOccurrences(of: #"(?i)(rpcpassword|password|secret|token)\s*[=:]\s*\S+"#, with: "$1=<redacted>", options: .regularExpression)
        line = displayMessage(line)
        log += line + "\n"
        if log.count > 14000 { log = String(log.suffix(14000)) }
        if let handle = try? FileHandle(forWritingTo: logURL) {
            defer { try? handle.close() }
            _ = try? handle.seekToEnd(); try? handle.write(contentsOf: Data((line + "\n").utf8))
        }
    }
    private func displayMessage(_ text: String) -> String {
        text.replacingOccurrences(of: #"(?i)\bsnapshot\b"#, with: "данные блокчейна", options: .regularExpression)
    }
    func cancel() { guard running else { return }; cancelling = true; process?.terminate() }
    var nodeLaunchArguments: [String] {
        ["-datadir=" + dataDirectory.path, "-server=1", "-choosedatadir=0"]
    }
    func openNode() {
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.arguments = nodeLaunchArguments
        NSWorkspace.shared.openApplication(at: installedApp, configuration: configuration) { [weak self] _, error in
            if let error {
                Task { @MainActor in self?.error = error.localizedDescription }
            }
        }
    }
    func showLog() { NSWorkspace.shared.open(logURL) }
}
