import MenubarKit
import OSLog
import ScratchpadNative
import SwiftUI

private let windowLog = Logger(subsystem: "com.alexcatdad.scratchpad", category: "Windowing")

enum AppIdentity {
    static let isDevelopment = Bundle.main.bundleIdentifier == "com.alexcatdad.scratchpad.dev"
    static let displayName = isDevelopment ? "Scratchpad DEV" : "Scratchpad"
    // Release builds carry the full CalVer version in ReleaseVersion; development builds fall back.
    static let version =
        (Bundle.main.object(forInfoDictionaryKey: "ReleaseVersion") as? String)
        ?? (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String)
    static let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String
    // The release tag people see, the same label the other menu-bar apps show.
    static var versionLabel: String { VersionLabel.of(.main) }
}

@main
struct ScratchpadApp: App {
    @StateObject private var model = AppModel()
    @Environment(\.openWindow) private var openWindow
    private let trayIcon = TrayIconLoader.load(
        resourcesDirectory: Bundle.main.resourceURL, baseName: "ScratchpadTrayIcon")

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
                .onAppear {
                    model.refresh(); model.loadReceipts()
                }
        } label: {
            TrayLabel(
                icon: trayIcon,
                fallbackSymbol: "note.text",
                status: model.presentation.status,
                appName: "Scratchpad",
                isDevelopment: AppIdentity.isDevelopment
            )
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
    windowLog.info(
        "\(event, privacy: .public): found=\(window != nil) visible=\(window?.isVisible ?? false) key=\(window?.isKeyWindow ?? false) active=\(NSApp.isActive) policy=\(NSApp.activationPolicy().rawValue)"
    )
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
        MenubarPanel {
            StatusHeader(p.status, detailOverride: model.activity)
            StatusRowsTile(p.rows)

            if !model.receipts.isEmpty {
                VStack(alignment: .leading, spacing: 0) {
                    TileHeader("Recent captures", linkTitle: "Show All") {
                        showWorkspace(.captures, model: model, openWindow: openWindow)
                    }
                    ForEach(model.receipts.prefix(3)) { receipt in
                        HStack(spacing: 8) {
                            Text(ReceiptLabels.type(receipt))
                                .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                                .frame(width: 64, alignment: .leading)
                            Text(ReceiptLabels.title(receipt)).lineLimit(1).truncationMode(.tail)
                            Spacer(minLength: 4)
                            Text(
                                receipt.state == "delivered"
                                    ? ReceiptLabels.when(receipt.acceptedAt) : ReceiptLabels.state(receipt.state)
                            )
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
                PrimaryActionButton(title) { runStatusAction(action, model: model, openWindow: openWindow) }
                    .disabled(model.activity != nil)
            }
            if let message = model.errorMessage {
                PanelNotice(message)
            }

            MenuSection(
                quitTitle: "Quit Scratchpad",
                quitHelp: "Closes the menu bar app. The capture service keeps saving captures."
            ) {
                Button("Open Scratchpad") { showWorkspace(.overview, model: model, openWindow: openWindow) }
                if model.status?.lifecycle == "running" {
                    Button("Pause Syncing") { model.setLifecycle("pause_delivery") }
                } else if model.status?.lifecycle == "paused", p.action != .resumeSyncing {
                    Button("Resume Syncing") { model.setLifecycle("resume_delivery") }
                }
                Button("Settings…") { showWorkspace(.general, model: model, openWindow: openWindow) }
            }

            VersionFooter()
        }
    }
}
