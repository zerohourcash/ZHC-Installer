import SwiftUI
import AppKit

@main struct ZHCInstallerApp: App {
    @NSApplicationDelegateAdaptor(InstallerDelegate.self) var delegate
    @StateObject private var model = InstallerModel()
    var body: some Scene {
        Window("ZHC Installer", id:"installer") {
            InstallerView(model:model).onAppear { delegate.model = model }
                .preferredColorScheme(.dark)
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)
        .commands { CommandGroup(replacing:.newItem) {} }
    }
}

@MainActor final class InstallerDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    weak var model: InstallerModel?
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular); NSApp.activate(ignoringOtherApps:true)
        DispatchQueue.main.async { NSApp.windows.first?.delegate = self }
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if model?.running == true { showStopNotice(); return .terminateCancel }
        return .terminateNow
    }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if model?.running == true { showStopNotice(); return false }
        return true
    }
    private func showStopNotice() {
        let alert = NSAlert(); alert.messageText = "Установка ещё выполняется"
        alert.informativeText = "Сначала нажмите «Остановить» и дождитесь завершения процесса. После прерывания распаковки потребуется повторная установка снапшота."
        alert.addButton(withTitle:"Вернуться к установке"); alert.runModal()
    }
}

struct InstallerView: View {
    @ObservedObject var model: InstallerModel
    @State private var showLog = false
    @State private var confirmStop = false
    private let cyan = Color(red:0.47,green:0.94,blue:0.91)
    var body: some View {
        ZStack {
            CosmosView(active:model.running)
            LinearGradient(colors:[Color.black.opacity(0.1),Color(red:0.025,green:0.03,blue:0.07).opacity(0.96)],startPoint:.center,endPoint:.bottom)
            VStack(alignment:.leading,spacing:0) {
                HStack {
                    Image(systemName:"sparkle").foregroundStyle(cyan)
                    Text("ZHC").font(.system(size:18,weight:.bold,design:.rounded))
                    Text("INSTALLER").font(.system(size:10,weight:.medium,design:.monospaced)).tracking(3).foregroundStyle(.white.opacity(0.5))
                    Spacer()
                    Text("macOS  /  APPLE SILICON").font(.system(size:9,design:.monospaced)).tracking(1.6).foregroundStyle(.white.opacity(0.45))
                }.padding(.top,30)
                Spacer().frame(height:65)
                Text(model.complete ? "CONNECTION ESTABLISHED" : "YOUR NODE. YOUR NETWORK.")
                    .font(.system(size:10,weight:.medium,design:.monospaced)).tracking(2.3).foregroundStyle(cyan)
                Text(model.title).font(.system(size:model.running ? 37 : 43,weight:.light)).tracking(-1.3)
                    .lineSpacing(3).frame(maxWidth:540,alignment:.leading).padding(.top,18)
                Text(model.complete ? "Evolution установлен. Нода отвечает по RPC.\nДальнейшая синхронизация продолжается в приложении." : "Полная нода. Проверенный снапшот.\nОдин установщик — от первого байта до запуска.")
                    .font(.system(size:13)).foregroundStyle(.white.opacity(0.6)).lineSpacing(5).padding(.top,18)
                Spacer()
                VStack(alignment:.leading,spacing:18) {
                    if model.running || model.complete {
                        HStack {
                            Text(model.complete ? "УСТАНОВКА ЗАВЕРШЕНА" : "ЭТАП \(model.stageIndex+1) / 6  ·  \(model.stages[model.stageIndex].uppercased())")
                                .font(.system(size:10,weight:.medium,design:.monospaced)).tracking(1)
                            Spacer()
                            if let fraction = model.fraction, !model.complete {
                                Text("\(Int(fraction*100))% этапа").font(.system(size:14,weight:.medium,design:.monospaced)).foregroundStyle(cyan)
                            }
                        }
                        if model.complete { ProgressView(value:1).tint(cyan) }
                        else if let fraction = model.fraction { ProgressView(value:fraction).tint(cyan) }
                        else { ProgressView().controlSize(.small).tint(cyan) }
                        HStack(spacing:34) {
                            metric("ОБЪЁМ",model.total > 0 ? "\(model.bytes(model.done)) / \(model.bytes(model.total))" : "Ожидание события")
                            metric("СКОРОСТЬ",model.speed > 0 ? "\(model.bytes(Int64(model.speed)))/с" : "—")
                            metric("ОСТАЛОСЬ",model.eta)
                            Spacer()
                        }
                        HStack(spacing:6) {
                            ForEach(0..<6) { i in
                                VStack(alignment:.leading,spacing:8) {
                                    Capsule().fill(model.complete || i <= model.stageIndex ? cyan : .white.opacity(0.12)).frame(height:2)
                                    Text(model.stages[i]).font(.system(size:9)).foregroundStyle(i == model.stageIndex ? .white : .white.opacity(0.45))
                                }.frame(maxWidth:.infinity)
                            }
                        }
                        if model.complete { Text(model.detail).font(.system(size:11,design:.monospaced)).foregroundStyle(cyan) }
                    } else {
                        HStack(spacing:24) {
                            metric("СНАПШОТ","11,2 ГБ · SHA-256")
                            metric("ПРИЛОЖЕНИЕ","Evolution 1.0.0 Qt")
                            metric("РАЗМЕЩЕНИЕ","~/Applications")
                        }
                        Text("Данные: ~/Library/Application Support/ZHCASH").font(.system(size:10,design:.monospaced)).foregroundStyle(.white.opacity(0.5))
                        Toggle("Заменить данные блокчейна снапшотом, сохранив кошельки и .conf",isOn:$model.confirmedReplacement)
                            .toggleStyle(.checkbox).font(.system(size:11))
                        Toggle("Сохранить ZIP снапшота после установки",isOn:$model.keepArchive)
                            .toggleStyle(.checkbox).font(.system(size:11)).foregroundStyle(.white.opacity(0.6))
                    }
                    if let error = model.error {
                        Text(error).font(.system(size:12)).foregroundStyle(Color(red:1,green:0.68,blue:0.53)).fixedSize(horizontal:false,vertical:true).lineLimit(5)
                    }
                    HStack {
                        Image(systemName:"lock.shield").foregroundStyle(cyan.opacity(0.8))
                        Text("Кошельки сохраняются · Телеметрия выключена").font(.system(size:10)).foregroundStyle(.white.opacity(0.45))
                        Spacer()
                        if model.running {
                            Button(model.cancelling ? "Останавливаем…" : "Остановить") { confirmStop = true }.disabled(model.cancelling)
                        } else if model.complete {
                            Button { model.openNode() } label: { Label("Открыть Evolution",systemImage:"arrow.up.right").padding(.horizontal,12).padding(.vertical,6) }.buttonStyle(.borderedProminent).tint(cyan).foregroundStyle(.black)
                        } else {
                            Button { model.start() } label: { Label(model.error == nil ? "Начать установку" : "Повторить",systemImage:"arrow.right").padding(.horizontal,12).padding(.vertical,6) }
                                .buttonStyle(.borderedProminent).tint(cyan).foregroundStyle(.black).disabled(!model.confirmedReplacement)
                        }
                    }
                }.padding(24).background(.black.opacity(0.26),in:RoundedRectangle(cornerRadius:18))
                    .overlay(RoundedRectangle(cornerRadius:18).stroke(.white.opacity(0.12),lineWidth:0.6))
                HStack {
                    Text("ZHCASH  /  EVOLUTION").tracking(2).font(.system(size:9,design:.monospaced)).foregroundStyle(.white.opacity(0.3))
                    Spacer()
                    Button("Журнал установки") { showLog = true }.buttonStyle(.plain).font(.system(size:10)).foregroundStyle(.white.opacity(0.5))
                }.padding(.vertical,20)
            }.padding(.horizontal,38)
        }.frame(width:920,height:700)
        .sheet(isPresented:$showLog) {
            VStack(alignment:.leading) {
                HStack { Text("Журнал установки").font(.headline); Spacer(); Button("Готово") { showLog = false } }
                ScrollView { Text(model.log.isEmpty ? "Установка ещё не запускалась." : model.log).font(.system(size:11,design:.monospaced)).textSelection(.enabled).frame(maxWidth:.infinity,alignment:.leading) }
                if !model.log.isEmpty { Button("Открыть файл журнала") { model.showLog() } }
            }.padding(24).frame(width:760,height:450)
        }
        .alert("Остановить установку?",isPresented:$confirmStop) {
            Button("Продолжить",role:.cancel) {}
            Button("Остановить",role:.destructive) { model.cancel() }
        } message: { Text("После остановки распаковки ноду нельзя запускать до повторной успешной установки. Кошельки и конфигурация сохраняются.") }
    }
    private func metric(_ label:String,_ value:String) -> some View {
        VStack(alignment:.leading,spacing:7) {
            Text(label).font(.system(size:8,weight:.medium,design:.monospaced)).tracking(1.4).foregroundStyle(.white.opacity(0.35))
            Text(value).font(.system(size:12,design:.monospaced)).foregroundStyle(.white.opacity(0.85))
        }
    }
}
