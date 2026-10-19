import SwiftUI
import ScratchpadNative

enum AppIdentity {
    static let isDevelopment = Bundle.main.bundleIdentifier == "com.alexcatdad.scratchpad.dev"
    static let displayName = isDevelopment ? "Scratchpad DEV" : "Scratchpad"
}

@main
struct ScratchpadApp: App {
    @StateObject private var model = AppModel()
    @Environment(\.openWindow) private var openWindow
    private let trayIcon = TrayIconLoader.load(resourcesDirectory: Bundle.main.resourceURL)

    var body: some Scene {
        MenuBarExtra {
            MenuContent()
                .environmentObject(model)
                .onAppear { model.refresh() }
        } label: {
            HStack(spacing: 4) {
                if let image = trayIcon {
                    Image(nsImage: image)
                        .renderingMode(.template)
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 18, height: 18)
                        .accessibilityLabel(AppIdentity.displayName)
                } else {
                    Image(systemName: "pawprint.fill")
                        .accessibilityLabel(AppIdentity.displayName)
                }
                if AppIdentity.isDevelopment {
                    Text("DEV").font(.caption2.weight(.semibold))
                }
            }
        }
        .menuBarExtraStyle(.window)

        WindowGroup(AppIdentity.displayName, id: "workspace") {
            WorkspaceView()
                .environmentObject(model)
                .frame(minWidth: 740, minHeight: 480)
        }
        .defaultSize(width: 860, height: 590)
    }
}

struct MenuContent: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.openWindow) private var openWindow
    @State private var confirmStop = false

    private var totalWaiting: Int {
        (model.status?.pending ?? 0) + (model.status?.blocked ?? 0)
            + (model.status?.rejected ?? 0) + (model.status?.needsSelection ?? 0)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(AppIdentity.displayName).font(.headline)
                Spacer()
                if totalWaiting > 0 { Text("\(totalWaiting) waiting").font(.caption).foregroundStyle(.secondary) }
            }
            statusGlance
            Divider()
            Button { openWindow(id: "workspace") } label: {
                Label("Pending captures \(totalWaiting)", systemImage: "list.bullet")
            }
            .disabled(model.status == nil)
            if model.status?.lifecycle == "paused" {
                Button { model.setLifecycle("resume_delivery") } label: { Label("Resume delivery", systemImage: "play") }
            } else if model.status?.lifecycle == "running" {
                Button { model.setLifecycle("pause_delivery") } label: { Label("Pause delivery", systemImage: "pause") }
            }
            Button { openWindow(id: "workspace") } label: { Label("Settings & connections…", systemImage: "slider.horizontal.3") }
            if model.status?.lifecycle == "stopped" || model.status == nil {
                Button { model.startService() } label: { Label("Start service", systemImage: "play.fill") }
            } else {
                Button(role: .destructive) { confirmStop = true } label: { Label("Stop service…", systemImage: "stop.fill") }
            }
            Divider()
            Button { NSApp.terminate(nil) } label: {
                HStack {
                    Text("Quit tray interface")
                    Spacer()
                    Text("Service unchanged").font(.caption2).foregroundStyle(.tertiary)
                }
            }
            .font(.callout)
        }
        .padding(16)
        .frame(width: 310)
        .confirmationDialog("Stop Scratchpad service?", isPresented: $confirmStop) {
            Button("Stop service", role: .destructive) { model.setLifecycle("stop") }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Capture tools will not be able to save until you explicitly start the service again.")
        }
    }

    @ViewBuilder private var statusGlance: some View {
        if let status = model.status {
            VStack(alignment: .leading, spacing: 7) {
                HStack(alignment: .top, spacing: 8) {
                    Circle().fill(statusAccent(status)).frame(width: 7, height: 7).padding(.top, 5)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(statusHeadline(status)).font(.subheadline.weight(.semibold))
                        Text(statusSubtitle(status)).font(.caption).foregroundStyle(.secondary)
                    }
                }
                LabeledStatus(title: "Capture service", value: status.lifecycle.capitalized, good: status.lifecycle == "running" || status.lifecycle == "paused")
                let connection = status.connection?.effectiveState() ?? "unknown"
                LabeledStatus(title: "Scratchpad server", value: connectionText(connection), good: connection == "connected")
                LabeledStatus(title: "Local saving", value: localSave(status), good: localSaveIsReady(status))
            }
            .padding(11)
            .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 10))
        } else {
            Label("Capture service unavailable", systemImage: "exclamationmark.circle.fill")
                .foregroundStyle(.orange)
                .font(.subheadline.weight(.semibold))
            Text("Local saving has not been confirmed.").font(.caption).foregroundStyle(.secondary)
        }
        if let message = model.errorMessage { Text(message).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true) }
    }
    private func statusHeadline(_ status: ServiceStatus) -> String {
        if status.lifecycle == "stopped" { return "Service stopped" }
        if status.lifecycle == "quiescing" { return "Service shutting down" }
        if !status.keyAvailable || status.storageAvailable == false { return "Local saving unavailable" }
        if status.lifecycle == "paused" { return "Delivery paused" }
        switch status.connection?.effectiveState() {
        case "connectivity": return "Server offline"
        case "authentication", "credential_changed": return "Authentication needed"
        case "server_incompatible": return "Server incompatible"
        case "connected": return totalWaiting > 0 ? "Captures waiting" : "Server connected"
        default: return "Server not verified"
        }
    }
    private func statusSubtitle(_ status: ServiceStatus) -> String {
        if status.lifecycle == "stopped" || status.lifecycle == "quiescing" { return "Local saving is unavailable." }
        if !status.keyAvailable { return "The queue key is unavailable." }
        if status.storageAvailable == false { return "Local queue storage is unavailable." }
        if status.lifecycle == "running" || status.lifecycle == "paused" { return "Captures are saved on this Mac." }
        return "Local saving has not been confirmed."
    }
    private func localSaveIsReady(_ status: ServiceStatus) -> Bool {
        (status.lifecycle == "running" || status.lifecycle == "paused") && status.keyAvailable && status.storageAvailable != false
    }
    private func statusAccent(_ status: ServiceStatus) -> Color {
        guard localSaveIsReady(status) else { return .orange }
        return status.connection?.effectiveState() == "connected" ? .green : .orange
    }
    private func localSave(_ status: ServiceStatus) -> String {
        if status.lifecycle == "stopped" || status.lifecycle == "quiescing" { return "Unavailable" }
        if !status.keyAvailable { return "Key unavailable" }
        return status.storageAvailable == false ? "Storage unavailable" : "Ready"
    }
    private func connectionText(_ state: String?) -> String {
        switch state {
        case "connected": "Connected"
        case "connectivity": "Offline"
        case "authentication", "credential_changed": "Authentication needed"
        case "server_incompatible": "Incompatible server"
        default: "Check pending"
        }
    }
}

struct LabeledStatus: View {
    let title: String
    let value: String
    let good: Bool
    var body: some View {
        HStack {
            Text(title).foregroundStyle(.secondary)
            Spacer()
            Circle().fill(good ? Color.green : Color.orange).frame(width: 7, height: 7)
            Text(value).font(.caption)
        }
    }
}
