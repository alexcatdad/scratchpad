import SwiftUI
import ScratchpadNative

@main
struct ScratchpadApp: App {
    @StateObject private var model = AppModel()
    @StateObject private var updates = UpdateCoordinator()
    @Environment(\.openWindow) private var openWindow

    var body: some Scene {
        MenuBarExtra {
            MenuContent()
                .environmentObject(model)
                .environmentObject(updates)
                .onAppear { model.refresh() }
        } label: {
            Label("Scratchpad", systemImage: "tray.full")
        }
        .menuBarExtraStyle(.window)

        WindowGroup("Scratchpad", id: "workspace") {
            WorkspaceView()
                .environmentObject(model)
                .environmentObject(updates)
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
                Text("Scratchpad").font(.headline)
                Spacer()
                if totalWaiting > 0 { Text("\(totalWaiting) saved").font(.caption).foregroundStyle(.secondary) }
            }
            statusGlance
            Divider()
            Button { openWindow(id: "workspace") } label: {
                Label("Capture queue \(totalWaiting)", systemImage: "list.bullet")
            }
            .disabled(model.status == nil)
            if model.status?.lifecycle == "paused" {
                Button { model.setLifecycle("resume_delivery") } label: { Label("Resume delivery", systemImage: "play") }
            } else if model.status?.lifecycle == "running" {
                Button { model.setLifecycle("pause_delivery") } label: { Label("Pause delivery", systemImage: "pause") }
            }
            Button { openWindow(id: "workspace") } label: { Label("Projects, settings & connections…", systemImage: "slider.horizontal.3") }
            if model.status?.lifecycle == "stopped" || model.status == nil {
                Button { model.startService() } label: { Label("Start service", systemImage: "play.fill") }
            } else {
                Button(role: .destructive) { confirmStop = true } label: { Label("Stop service…", systemImage: "stop.fill") }
            }
            Divider()
            Button("Quit tray interface") { NSApp.terminate(nil) }
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
                Label(statusHeadline(status), systemImage: status.lifecycle == "running" ? "circle.fill" : "circle")
                    .font(.subheadline.weight(.semibold))
                LabeledStatus(title: "Capture service", value: status.lifecycle.capitalized, good: status.lifecycle == "running" || status.lifecycle == "paused")
                LabeledStatus(title: "Local saving", value: localSave(status), good: (status.lifecycle == "running" || status.lifecycle == "paused") && status.keyAvailable && status.storageAvailable != false)
                let connection = status.connection?.effectiveState() ?? "unknown"
                LabeledStatus(title: "Scratchpad server", value: connectionText(connection), good: connection == "connected")
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
        if status.lifecycle == "quiescing" { return "Preparing update" }
        if !status.keyAvailable || status.storageAvailable == false { return "Local saving needs attention" }
        if status.lifecycle == "paused" { return "Delivery paused · local capture active" }
        switch status.connection?.effectiveState() {
        case "connectivity": return "Server offline · captures remain local"
        case "authentication", "credential_changed": return "Authentication needs attention"
        case "server_incompatible": return "Server version needs attention"
        case "connected": return totalWaiting > 0 ? "Captures are saved on this Mac" : "Scratchpad is ready"
        default: return "Service running"
        }
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
