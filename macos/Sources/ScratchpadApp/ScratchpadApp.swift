import SwiftUI
import ScratchpadNative
import OSLog

private let windowLog = Logger(subsystem: "com.alexcatdad.scratchpad", category: "Windowing")

enum AppIdentity {
    static let isDevelopment = Bundle.main.bundleIdentifier == "com.alexcatdad.scratchpad.dev"
    static let displayName = isDevelopment ? "Scratchpad DEV" : "Scratchpad"
    static let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
    static let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String
    static var versionLabel: String { ServiceUpdateState.label(version: version ?? "unknown", build: build) }
}

@main
struct ScratchpadApp: App {
    @StateObject private var model = AppModel()
    @Environment(\.openWindow) private var openWindow
    private let trayIcon = TrayIconLoader.load(resourcesDirectory: Bundle.main.resourceURL)

    var body: some Scene {
        Window(AppIdentity.displayName, id: "workspace") {
            WorkspaceView()
                .environmentObject(model)
                .frame(minWidth: 820, minHeight: 540)
                .onAppear {
                    DispatchQueue.main.async {
                        WorkspaceWindowPresentation.present()
                        logWorkspaceState("workspace appeared")
                    }
                }
                .onDisappear { logWorkspaceState("workspace disappeared") }
                .onReceive(NotificationCenter.default.publisher(for: NSApplication.didResignActiveNotification)) { _ in
                    logWorkspaceState("app resigned active")
                }
                .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                    logWorkspaceState("app became active")
                }
        }
        .defaultSize(width: 980, height: 660)
        .commands {
            CommandGroup(replacing: .appSettings) {
                Button("Settings…") { showWorkspace(.general, model: model, openWindow: openWindow) }
                    .keyboardShortcut(",")
            }
        }

        MenuBarExtra {
            MenuContent()
                .environmentObject(model)
                .onAppear { model.refresh(); model.loadReceipts() }
        } label: {
            HStack(spacing: 3) {
                if let image = trayIcon {
                    Image(nsImage: image)
                        .renderingMode(.template)
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 18, height: 18)
                } else {
                    Image(systemName: "note.text")
                }
                // A visible mark, not just a colour change, when the state needs attention.
                if model.presentation.tone != .ok {
                    Image(systemName: "exclamationmark")
                        .font(.system(size: 9, weight: .heavy))
                }
                if AppIdentity.isDevelopment {
                    Text("DEV").font(.caption2.weight(.semibold))
                }
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("\(AppIdentity.displayName), \(model.presentation.word)")
            .onAppear { windowLog.info("Tray label appeared") }
            .onDisappear { windowLog.info("Tray label disappeared") }
        }
        .menuBarExtraStyle(.window)
    }
}

@MainActor
func showWorkspace(_ page: WorkspacePage, model: AppModel, openWindow: OpenWindowAction) {
    model.workspacePage = page
    openWindow(id: "workspace")
    // Let the tray button finish before assigning key-window status to the workspace.
    DispatchQueue.main.async {
        WorkspaceWindowPresentation.present()
        logWorkspaceState("workspace requested")
    }
}

@MainActor
private func logWorkspaceState(_ event: String) {
    let window = NSApp.windows.first { $0.identifier == WorkspaceWindowPresentation.identifier }
    windowLog.info("\(event, privacy: .public): found=\(window != nil) visible=\(window?.isVisible ?? false) key=\(window?.isKeyWindow ?? false) active=\(NSApp.isActive) policy=\(NSApp.activationPolicy().rawValue)")
}

/// Runs a status action from any surface, so the popover and the window behave the same.
@MainActor
func runStatusAction(_ action: StatusPresentation.Action, model: AppModel, openWindow: OpenWindowAction) {
    switch action {
    case .startService: model.startService()
    case .restartService: model.restartService()
    case .resumeSyncing: model.setLifecycle("resume_delivery")
    case .checkAgain: model.verifyConnection()
    case .reconnect: showWorkspace(.connection, model: model, openWindow: openWindow)
    case .reviewCaptures: showWorkspace(.captures, model: model, openWindow: openWindow)
    }
}

struct MenuContent: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        let p = model.presentation
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 12) {
                Image(systemName: p.tone.symbol)
                    .font(.system(size: 30))
                    .foregroundStyle(p.tone.color)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text(p.headline).font(.headline)
                    Text(model.activity ?? p.detail)
                        .font(.callout).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .accessibilityElement(children: .combine)
            .padding(.horizontal, 4)

            VStack(spacing: 0) {
                ForEach(p.rows, id: \.title) { row in
                    HStack(spacing: 8) {
                        StatusIcon(tone: row.tone)
                        Text(row.title)
                        Spacer()
                        Text(row.value).foregroundStyle(.secondary)
                    }
                    .font(.callout)
                    .padding(.horizontal, 12)
                    .frame(minHeight: 28)
                    .accessibilityElement(children: .combine)
                }
            }
            .groupedTile()

            if !model.receipts.isEmpty {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        Text("Recent captures").font(.callout.weight(.semibold)).foregroundStyle(.secondary)
                        Spacer()
                        Button("Show All") { showWorkspace(.captures, model: model, openWindow: openWindow) }
                            .buttonStyle(.link).font(.callout)
                    }
                    .padding(.horizontal, 12).padding(.vertical, 4)
                    ForEach(model.receipts.prefix(3)) { receipt in
                        HStack(spacing: 8) {
                            Text(ReceiptLabels.type(receipt))
                                .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                                .frame(width: 64, alignment: .leading)
                            Text(ReceiptLabels.title(receipt)).lineLimit(1).truncationMode(.tail)
                            Spacer(minLength: 4)
                            Text(receipt.state == "delivered" ? ReceiptLabels.when(receipt.acceptedAt) : ReceiptLabels.state(receipt.state))
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        .font(.callout)
                        .padding(.horizontal, 12)
                        .frame(minHeight: 24)
                        .accessibilityElement(children: .combine)
                    }
                }
                .groupedTile()
            }

            if let action = p.action, let title = p.actionTitle {
                Button(title) { runStatusAction(action, model: model, openWindow: openWindow) }
                    .buttonStyle(ProminentButtonStyle(fullWidth: true))
                    .disabled(model.activity != nil)
            }
            if let message = model.errorMessage {
                Label(message, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            VStack(spacing: 0) {
                Button("Open Scratchpad") { showWorkspace(.overview, model: model, openWindow: openWindow) }
                if model.status?.lifecycle == "running" {
                    Button("Pause Syncing") { model.setLifecycle("pause_delivery") }
                } else if model.status?.lifecycle == "paused", p.action != .resumeSyncing {
                    Button("Resume Syncing") { model.setLifecycle("resume_delivery") }
                }
                Button("Settings…") { showWorkspace(.general, model: model, openWindow: openWindow) }
                Divider().padding(.vertical, 4).padding(.horizontal, 10)
                Button("Quit Scratchpad") { NSApp.terminate(nil) }
                    .help("Closes the menu bar app. The capture service keeps saving captures.")
            }
            .buttonStyle(MenuRowButtonStyle())

            Text("Version \(AppIdentity.versionLabel)")
                .font(.caption).foregroundStyle(.secondary)
                .padding(.horizontal, 10)
        }
        .padding(12)
        .frame(width: 340)
    }
}
