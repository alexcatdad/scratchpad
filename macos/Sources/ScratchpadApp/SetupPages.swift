import SwiftUI
import AppKit
import ScratchpadNative

struct ConnectionPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var showAdvanced = false

    var body: some View {
        Form {
            Section {
                TextField("Address", text: Binding(get: { model.configuration.serverURL }, set: {
                    guard $0 != model.configuration.serverURL else { return }
                    model.configuration.serverURL = $0
                    model.clearDiscoveredKeys()
                }), prompt: Text("https://scratchpad.example.com"))
                LabeledContent("Status") {
                    let server = model.presentation.rows.first { $0.title == "Server" }
                    HStack(spacing: 6) {
                        StatusIcon(tone: server?.tone ?? .off)
                        Text([server?.value, model.status?.serverVersion.map { "server \($0)" }].compactMap { $0 }.joined(separator: " · "))
                    }
                }
            } header: {
                Text("Server")
            } footer: {
                Text("Link GitHub in the server dashboard first. Scratchpad then finds keys on this Mac that the server accepts.")
            }

            Section {
                if model.sshKeys.isEmpty {
                    HStack {
                        Text(model.keyDiscoveryMessage ?? "Look for SSH keys on this Mac that this server accepts.")
                            .foregroundStyle(.secondary)
                        Spacer()
                        Button("Find Keys") { model.discoverSSHKeys() }
                            .disabled(model.configuration.serverURL.isEmpty || model.busy)
                    }
                } else {
                    Picker("Key", selection: $model.selectedSSHKey) {
                        ForEach(model.sshKeys) { key in
                            VStack(alignment: .leading, spacing: 1) {
                                Text(key.label)
                                Text(key.fingerprint).font(.caption.monospaced()).foregroundStyle(.secondary)
                            }
                            .tag(Optional(key.id))
                        }
                    }
                    .pickerStyle(.radioGroup)
                    HStack {
                        Button("Find Again") { model.discoverSSHKeys() }.disabled(model.busy)
                        Spacer()
                        Button("Connect") { model.connectSSHKey() }
                            .buttonStyle(ProminentButtonStyle())
                            .disabled(model.selectedSSHKey == nil || model.busy)
                    }
                }
                if model.busy { ProgressView("Checking with the server…").controlSize(.small) }
            } header: {
                Text("Sign-in key")
            } footer: {
                Text("Your SSH agent or 1Password approves each signature. Private keys never leave the agent.")
            }

            Section("Account") {
                if let verified = model.verified {
                    LabeledContent("Signed in") {
                        HStack(spacing: 6) { StatusIcon(tone: .ok); Text("Verified \(ReceiptLabels.when(verified.verifiedAt))") }
                    }
                    LabeledContent("Credential", value: verified.credentialId)
                } else {
                    LabeledContent("Signed in", value: model.configuration.credentialFingerprint.isEmpty ? "Not connected" : "Not checked yet")
                }
                HStack {
                    Spacer()
                    Button("Check Again") { model.verifyConnection() }
                        .disabled(model.configuration.serverURL.isEmpty || model.configuration.credentialFingerprint.isEmpty || model.busy)
                }
            }

            Section {
                DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
                    TextField("Key fingerprint", text: $model.configuration.credentialFingerprint)
                    TextField("Public key file", text: $model.configuration.publicKeyPath)
                    TextField("Signing key reference", text: $model.configuration.signingKeyPath)
                    TextField("SSH agent socket", text: Binding(get: { model.configuration.sshAgentSocket }, set: {
                        guard $0 != model.configuration.sshAgentSocket else { return }
                        model.configuration.sshAgentSocket = $0
                        model.clearDiscoveredKeys()
                    }), prompt: Text("Default agent"))
                    HStack {
                        Text("The same settings as scratchpad-mcp connect in Terminal.").font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        Button("Save") { model.saveConfiguration() }.disabled(model.busy)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Connection")
        .onAppear { model.loadConfiguration() }
    }
}

struct ToolsPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var reviewing: Review?
    @State private var genericText: String?
    @State private var message: String?
    private let executable = Bundle.main.bundleURL.appending(path: "Contents/Helpers/scratchpad-mcp").path

    struct Review: Identifiable {
        let kind: MCPClientKind
        let path: String
        let before: String
        let after: String
        let file: ReviewedConfigFile
        var id: String { path }
    }

    var body: some View {
        Form {
            Section {
                ForEach([MCPClientKind.claudeCode, .codex], id: \.self) { kind in
                    let path = defaultPath(kind)
                    let installed = isInstalled(path)
                    HStack {
                        VStack(alignment: .leading, spacing: 1) {
                            Text(kind.rawValue)
                            Text(abbreviated(path)).font(.caption.monospaced()).foregroundStyle(.secondary)
                        }
                        Spacer()
                        HStack(spacing: 5) {
                            StatusIcon(tone: installed ? .ok : .off, size: 11)
                            Text(installed ? "Connected" : "Not set up")
                        }
                        .font(.callout).foregroundStyle(.secondary)
                        if installed {
                            Button("Review…") { prepare(kind, path: path) }
                        } else {
                            Button("Set Up…") { prepare(kind, path: path) }.buttonStyle(ProminentButtonStyle())
                        }
                    }
                    .accessibilityElement(children: .contain)
                }
                HStack {
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Another tool")
                        Text("Any MCP client that runs a command").font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Copy Config") { copyGeneric() }
                }
            } header: {
                Text("AI tools on this Mac")
            } footer: {
                Text("Scratchpad adds one entry to each tool’s configuration and shows you the change first. Restart the tool afterwards.")
            }
            if let message {
                Section { Text(message).foregroundStyle(.secondary) }
            }
            if let genericText {
                Section("Configuration copied") {
                    Text(genericText).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("AI Tools")
        .sheet(item: $reviewing) { review in
            ReviewSheet(review: review, onApply: { apply(review) }, onCancel: { reviewing = nil })
        }
    }

    private func defaultPath(_ kind: MCPClientKind) -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        switch kind {
        case .codex: return "\(home)/.codex/config.toml"
        case .claudeCode: return "\(home)/.claude.json"
        case .generic: return ""
        }
    }
    private func abbreviated(_ path: String) -> String { (path as NSString).abbreviatingWithTildeInPath }
    private func isInstalled(_ path: String) -> Bool {
        guard let data = try? ClientConfiguration.readConfigFile(at: URL(fileURLWithPath: path)) else { return false }
        return String(decoding: data, as: UTF8.self).contains("scratchpad-mcp")
    }
    private func prepare(_ kind: MCPClientKind, path: String) {
        do {
            let url = URL(fileURLWithPath: path)
            let original = try ClientConfiguration.readConfigFile(at: url)
            let data = original ?? Data()
            let before = String(decoding: data, as: UTF8.self)
            let after: String
            switch kind {
            case .codex:
                after = try ClientConfiguration.codexPreview(existing: before, executable: executable, configuration: model.configuration)
            case .claudeCode:
                after = String(decoding: try ClientConfiguration.jsonPreview(existing: data.isEmpty ? Data("{}".utf8) : data, executable: executable, configuration: model.configuration), as: UTF8.self)
            case .generic:
                return
            }
            guard after != before else {
                message = "\(kind.rawValue) already uses this Scratchpad. Nothing to change."
                return
            }
            reviewing = Review(kind: kind, path: path, before: before, after: after,
                               file: ReviewedConfigFile(path: url.path, originalContents: original, proposedContents: Data(after.utf8)))
            message = nil
        } catch {
            message = "Could not prepare the change: \(error.localizedDescription)"
        }
    }
    private func apply(_ review: Review) {
        do {
            try ClientConfiguration.applyReviewedFile(review.file, selectedURL: URL(fileURLWithPath: review.path))
            message = "Added Scratchpad to \(review.kind.rawValue). Restart it to load the new tools."
        } catch {
            message = "Could not apply the change: \(error.localizedDescription)"
        }
        reviewing = nil
    }
    private func copyGeneric() {
        do {
            let text = String(decoding: try ClientConfiguration.genericEntry(executable: executable, configuration: model.configuration), as: UTF8.self)
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            genericText = text
        } catch {
            message = "Could not create the configuration: \(error.localizedDescription)"
        }
    }
}

/// Shows only what changes, as added and removed lines.
private struct ReviewSheet: View {
    let review: ToolsPage.Review
    let onApply: () -> Void
    let onCancel: () -> Void

    private var changes: [(String, Bool)] {
        let before = review.before.components(separatedBy: "\n")
        let after = review.after.components(separatedBy: "\n")
        let removed = before.filter { line in !after.contains(line) && !line.trimmingCharacters(in: .whitespaces).isEmpty }
        let added = after.filter { line in !before.contains(line) && !line.trimmingCharacters(in: .whitespaces).isEmpty }
        return removed.map { ($0, false) } + added.map { ($0, true) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Add Scratchpad to \(review.kind.rawValue)?").font(.title3.weight(.bold))
            Text("These lines change in \((review.path as NSString).abbreviatingWithTildeInPath). Nothing else in the file is touched.")
                .foregroundStyle(.secondary)
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(changes.enumerated()), id: \.offset) { _, change in
                        Text((change.1 ? "+ " : "− ") + change.0)
                            .foregroundStyle(change.1 ? Color.green : Color.red)
                            .accessibilityLabel((change.1 ? "Added: " : "Removed: ") + change.0)
                    }
                }
                .font(.system(.callout, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(10)
            }
            .frame(minHeight: 120, maxHeight: 260)
            .background(.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 10))
            HStack {
                Spacer()
                Button("Cancel", role: .cancel, action: onCancel).keyboardShortcut(.cancelAction)
                Button("Apply Change", action: onApply).buttonStyle(ProminentButtonStyle()).keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 560)
    }
}

struct GeneralPage: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("incidentNotificationsEnabled") private var notificationsEnabled = false
    @State private var loginEnabled = false
    @State private var notificationPermission: IncidentNotifier.PermissionState = .notDetermined
    @State private var confirmStop = false

    var body: some View {
        Form {
            Section("Background") {
                Toggle(isOn: $loginEnabled) {
                    Text("Start capture service at login")
                    Text("If you stop it yourself, it stays stopped.")
                }
                .onChange(of: loginEnabled) { value in model.setStartAtLogin(value) }
                if model.supervisor.loginItemStatus == .requiresApproval {
                    Label("Approve Scratchpad in System Settings › General › Login Items.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                }
                Toggle(isOn: $notificationsEnabled) {
                    Text("Notify me about problems")
                    Text("Long outages, refused captures and saving failures. Counts only, never capture content.")
                }
                .onChange(of: notificationsEnabled) { enabled in
                    Task {
                        notificationPermission = enabled
                            ? await IncidentNotifier.requestPermission()
                            : await IncidentNotifier.permissionState()
                    }
                }
                if notificationsEnabled, notificationPermission == .denied {
                    Label("macOS blocked notifications. Allow them in System Settings › Notifications › Scratchpad.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                }
            }

            Section("About") {
                LabeledContent("Scratchpad", value: AppIdentity.versionLabel)
                LabeledContent("Capture service") {
                    HStack(spacing: 6) {
                        if case .restartNeeded = model.serviceUpdate { StatusIcon(tone: .update) }
                        Text(serviceVersionLabel)
                    }
                }
                LabeledContent("Server", value: serverVersionLabel)
                LabeledContent("Updates") {
                    Text("brew upgrade --cask scratchpad").font(.callout.monospaced()).textSelection(.enabled)
                }
                HStack {
                    Link("Documentation", destination: URL(string: "https://alexcatdad.github.io/scratchpad/")!)
                    Spacer()
                    if case .restartNeeded = model.serviceUpdate {
                        Button("Restart Capture Service") { model.restartService() }
                            .buttonStyle(ProminentButtonStyle())
                            .disabled(model.activity != nil)
                    }
                    Button("Copy Diagnostics") { copyDiagnostics() }
                }
            }

            Section {
                if model.status?.lifecycle == "stopped" || model.status == nil {
                    HStack {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("Capture service is stopped")
                            Text("AI tools cannot save memory until you start it.").font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Start") { model.startService() }.buttonStyle(ProminentButtonStyle())
                    }
                } else {
                    HStack {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("Stop capture service")
                            Text("AI tools cannot save memory until you start it again.").font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Stop…", role: .destructive) { confirmStop = true }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("General")
        .onAppear { loginEnabled = model.supervisor.loginItemStatus == .enabled }
        .task { notificationPermission = await IncidentNotifier.permissionState() }
        .confirmationDialog("Stop the capture service?", isPresented: $confirmStop) {
            Button("Stop Capture Service", role: .destructive) { model.setLifecycle("stop") }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("It stays stopped, even after you log in again, until you start it.")
        }
    }

    private var serviceVersionLabel: String {
        guard let version = model.status?.serviceVersion else { return "Not reported" }
        let label = ServiceUpdateState.label(version: version, build: model.status?.serviceBuild)
        if case .restartNeeded = model.serviceUpdate { return "\(label), restart to update" }
        return label
    }
    private var serverVersionLabel: String {
        guard let version = model.status?.serverVersion, !version.isEmpty else { return "Not reported" }
        guard let commit = model.status?.serverCommit, !commit.isEmpty else { return version }
        return "\(version) · \(commit)"
    }
    private func copyDiagnostics() {
        let lines = [
            "Scratchpad \(AppIdentity.versionLabel)",
            "Capture service: \(serviceVersionLabel)",
            "Server: \(serverVersionLabel)",
            "Status: \(model.presentation.word)",
            "Lifecycle: \(model.status?.lifecycle ?? "unavailable")",
            "Connection: \(model.status?.connection?.effectiveState() ?? "unknown")",
            "Waiting: \(model.status.map { $0.pending + $0.blocked + $0.rejected + $0.needsSelection } ?? 0)",
            "macOS: \(ProcessInfo.processInfo.operatingSystemVersionString)",
        ]
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(lines.joined(separator: "\n"), forType: .string)
    }
}
