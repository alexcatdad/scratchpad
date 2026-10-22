import SwiftUI
import ScratchpadNative

struct ProjectsPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var filter = ""
    private var filtered: [Project] { model.projects.filter { filter.isEmpty || $0.name.localizedCaseInsensitiveContains(filter) || $0.id.localizedCaseInsensitiveContains(filter) } }
    var body: some View {
        VStack(alignment: .leading, spacing: 15) {
            Text("Projects").font(.largeTitle.bold())
            Text("Projects are loaded from the configured Scratchpad server. Each capture keeps its own project.").foregroundStyle(.secondary)
            TextField("Search projects", text: $filter).textFieldStyle(.roundedBorder)
            if model.projects.isEmpty {
                EmptyStateView(title: "No projects loaded", symbol: "folder", detail: "Connect and verify the Scratchpad server, then refresh projects.")
                    .frame(maxHeight: .infinity)
            } else {
                List(filtered) { project in
                    VStack(alignment: .leading, spacing: 3) {
                        Text(project.name).font(.headline)
                        Text(project.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    }.padding(.vertical, 3)
                }
            }
        }
        .padding(22)
        .toolbar { ToolbarItem { Button("Refresh projects") { model.loadProjects() } } }
        .onAppear { model.loadProjects() }
    }
}

struct AuthenticationPage: View {
    @EnvironmentObject private var model: AppModel
    var body: some View {
        Form {
            Section("Scratchpad connection") {
                TextField("Server URL", text: Binding(get: { model.configuration.serverURL }, set: {
                    model.configuration.serverURL = $0
                    model.clearDiscoveredKeys()
                }))
                Button("Find SSH keys") { model.discoverSSHKeys() }
                    .disabled(model.configuration.serverURL.isEmpty)
                Text("Link GitHub in the server dashboard first. Scratchpad checks your local public keys with that server, then asks your SSH signer to prove possession.")
                    .font(.caption).foregroundStyle(.secondary)
                if let message = model.keyDiscoveryMessage { Text(message).font(.caption).foregroundStyle(.secondary) }
                if !model.sshKeys.isEmpty {
                    Picker("SSH key", selection: $model.selectedSSHKey) {
                        Text("Choose a key").tag(String?.none)
                        ForEach(model.sshKeys) { key in
                            Text("\(key.label) — \(key.fingerprint)").tag(Optional(key.id))
                        }
                    }
                    Button("Connect with selected key") { model.connectSSHKey() }
                        .disabled(model.selectedSSHKey == nil)
                }
                DisclosureGroup("Advanced connection settings") {
                    TextField("Public key fingerprint", text: $model.configuration.credentialFingerprint)
                    TextField("Public key file", text: $model.configuration.publicKeyPath)
                    TextField("SSH signing key reference", text: $model.configuration.signingKeyPath)
                    TextField("SSH agent socket (optional)", text: Binding(get: { model.configuration.sshAgentSocket }, set: {
                        model.configuration.sshAgentSocket = $0
                        model.clearDiscoveredKeys()
                    }))
                    Text("Keys found as public files must be loaded into your SSH agent before connecting. Your agent handles unlock and approval prompts. Private key files and passphrases are never copied into Scratchpad.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            Section {
                HStack {
                    Button("Save connection") { model.saveConfiguration() }
                    Button("Verify saved connection") { model.verifyConnection() }
                        .disabled(model.configuration.serverURL.isEmpty || model.configuration.credentialFingerprint.isEmpty)
                    if model.busy { ProgressView().controlSize(.small) }
                }
            }
            if let verified = model.verified {
                Section("Authenticated verification") {
                    Label("Server proof succeeded", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                    LabeledContent("Server", value: verified.serverURL)
                    LabeledContent("Credential", value: verified.credentialId)
                    LabeledContent("Verified", value: verified.verifiedAt)
                    Text("This confirms authenticated read access. It does not confirm capture-write permission.").font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .formStyle(.grouped)
        .disabled(model.busy)
        .navigationTitle("Authentication")
        .onAppear { model.loadConfiguration() }
    }
}

struct SettingsPage: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("incidentNotificationsEnabled") private var notificationsEnabled = false
    @State private var loginEnabled = false
    @State private var notificationPermission: IncidentNotifier.PermissionState = .notDetermined

    var body: some View {
        Form {
            Section("Server connection") {
                LabeledContent("Server", value: model.configuration.serverURL.isEmpty ? "Not configured" : model.configuration.serverURL)
                LabeledContent("Local capture", value: LocalCaptureReadiness(status: model.status, observedAt: model.statusObservedAt).label)
                LabeledContent("Connection", value: connectionName)
                Text("Pending captures stay bound to their original server and credential.").font(.caption).foregroundStyle(.secondary)
                Button("View authentication") { NotificationCenter.default.post(name: .showAuthenticationPage, object: nil) }
            }
            Section("Background behavior") {
                Toggle("Start capture service at login", isOn: $loginEnabled)
                    .onChange(of: loginEnabled) { value in model.setStartAtLogin(value) }
                if model.supervisor.loginItemStatus == .requiresApproval {
                    Label("Login service needs approval in System Settings → General → Login Items.", systemImage: "exclamationmark.triangle")
                        .font(.caption).foregroundStyle(.orange)
                }
                Text("A deliberately stopped service stays stopped until you start it.").font(.caption).foregroundStyle(.secondary)
                Toggle("Incident notifications", isOn: $notificationsEnabled)
                    .onChange(of: notificationsEnabled) { enabled in
                        Task {
                            notificationPermission = enabled
                                ? await IncidentNotifier.requestPermission()
                                : await IncidentNotifier.permissionState()
                        }
                    }
                if notificationsEnabled {
                    switch notificationPermission {
                    case .authorized:
                        Text("Notifications are authorized. You can change delivery in System Settings → Notifications.")
                            .font(.caption).foregroundStyle(.secondary)
                    case .denied:
                        Text("macOS denied notifications. Enable them for Scratchpad in System Settings → Notifications.")
                            .font(.caption).foregroundStyle(.orange)
                    case .notDetermined:
                        Text("macOS has not confirmed notification permission yet.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                } else {
                    Text("Notification permission is requested only when you enable incident notifications.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Text("Alerts cover sustained outages, blocked delivery and local-save failures. They include counts and safe descriptions only.").font(.caption).foregroundStyle(.secondary)
            }
            Section("App maintenance") {
                Text("Install and update Scratchpad through Homebrew using the project’s tap.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Settings")
        .onAppear { loginEnabled = model.supervisor.loginItemStatus == .enabled; model.loadConfiguration() }
        .task { notificationPermission = await IncidentNotifier.permissionState() }
    }
    private var connectionName: String {
        switch model.status?.connection?.effectiveState() {
        case "connected": "Reachable"
        case "connectivity": "Offline"
        case "authentication", "credential_changed": "Authentication needed"
        case "server_incompatible": "Incompatible server"
        default: "Not checked"
        }
    }
}

extension Notification.Name { static let showAuthenticationPage = Notification.Name("scratchpad.showAuthenticationPage") }

struct MCPSetupPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var kind: MCPClientKind = .codex
    @State private var configPath = ""
    @State private var before = ""
    @State private var preview = ""
    @State private var reviewedFile: ReviewedConfigFile?
    @State private var problem: String?
    @State private var showingApproval = false
    @State private var genericText = ""
    private let executable = Bundle.main.bundleURL.appending(path: "Contents/Helpers/scratchpad-mcp").path
    private var isGeneric: Bool { kind == .generic }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("MCP setup").font(.largeTitle.bold())
            Text("Preview the exact client change before applying it. Existing unrelated configuration is preserved.").foregroundStyle(.secondary)
            Text("Launching a configured client asks its bundled Scratchpad MCP adapter to ensure the shared local collector is running. Persistent Stop is respected and must be cleared with Start in Scratchpad; closing a client leaves collector delivery running.")
                .font(.caption).foregroundStyle(.secondary)
            GroupBox("Scratchpad app status") {
                VStack(alignment: .leading, spacing: 6) {
                    LabeledContent("Local capture", value: LocalCaptureReadiness(status: model.status, observedAt: model.statusObservedAt).label)
                    LabeledContent("Server connection", value: remoteConnectionName)
                    Text("These statuses are checked separately. Local capture readiness describes the bundled collector; server connection reflects its latest connection observation.")
                        .font(.caption).foregroundStyle(.secondary)
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
            Picker("Client", selection: $kind) { ForEach(MCPClientKind.allCases) { Text($0.rawValue).tag($0) } }.pickerStyle(.segmented)
            if !isGeneric {
                TextField("Configuration file", text: $configPath).textFieldStyle(.roundedBorder)
                HStack {
                    Button("Load and preview") { makePreview() }
                    Button("Apply this preview…") { showingApproval = true }.disabled(reviewedFile == nil || preview.isEmpty || preview == before)
                }
            } else {
                Button("Generate generic stdio configuration") { makePreview() }
                if !genericText.isEmpty {
                    Text("Pass this entry to a compatible MCP client:").font(.subheadline.weight(.semibold))
                    ScrollView { Text(genericText).font(.system(.caption, design: .monospaced)).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding(10) }
                        .frame(maxHeight: 230).background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
                }
            }
            if !isGeneric && !preview.isEmpty {
                HStack(spacing: 12) {
                    ConfigColumn(title: "Current", text: before)
                    ConfigColumn(title: "After applying", text: preview)
                }
            }
            if let problem { Text(problem).foregroundStyle(.orange).font(.caption) }
            Spacer()
        }
        .padding(22)
        .navigationTitle("MCP setup")
        .onAppear { setDefaultPath() }
        .onChange(of: kind) { _ in setDefaultPath(); before = ""; preview = ""; reviewedFile = nil; genericText = ""; problem = nil }
        .confirmationDialog("Apply this exact configuration?", isPresented: $showingApproval, titleVisibility: .visible) {
            Button("Apply preview") { applyPreview() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This will update only the Scratchpad MCP entry in the selected client file. Review both columns before continuing.")
        }
    }

    private func setDefaultPath() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        switch kind {
        case .codex: configPath = "\(home)/.codex/config.toml"
        case .claudeCode: configPath = "\(home)/.claude.json"
        case .generic: configPath = ""
        }
    }
    private var remoteConnectionName: String {
        switch model.status?.connection?.effectiveState() {
        case "connected": "Reachable"
        case "connectivity": "Offline"
        case "authentication", "credential_changed": "Authentication needed"
        case "server_incompatible": "Incompatible server"
        default: "Not checked"
        }
    }
    private func makePreview() {
        do {
            if isGeneric {
                guard !model.configuration.serverURL.isEmpty, !model.configuration.publicKeyPath.isEmpty, !model.configuration.signingKeyPath.isEmpty else {
                    throw SetupError.connectionNotConfigured
                }
                let data = try ClientConfiguration.genericEntry(executable: executable, configuration: model.configuration)
                genericText = String(decoding: data, as: UTF8.self)
                return
            }
            guard !model.configuration.serverURL.isEmpty, !model.configuration.publicKeyPath.isEmpty, !model.configuration.signingKeyPath.isEmpty else {
                throw SetupError.connectionNotConfigured
            }
            let url = URL(fileURLWithPath: configPath)
            let original = try ClientConfiguration.readConfigFile(at: url)
            let data = original ?? Data()
            before = String(decoding: data, as: UTF8.self)
            switch kind {
            case .codex:
                preview = try ClientConfiguration.codexPreview(existing: before, executable: executable, configuration: model.configuration)
            case .claudeCode:
                let source = data.isEmpty ? Data("{}".utf8) : data
                preview = String(decoding: try ClientConfiguration.jsonPreview(existing: source, executable: executable, configuration: model.configuration), as: UTF8.self)
            case .generic: break
            }
            reviewedFile = ReviewedConfigFile(path: url.path, originalContents: original, proposedContents: Data(preview.utf8))
            problem = nil
        } catch { reviewedFile = nil; problem = "Could not prepare a configuration preview: \(error.localizedDescription)" }
    }
    private func applyPreview() {
        do {
            guard let reviewedFile else { throw ClientConfigurationError.contentsChanged }
            try ClientConfiguration.applyReviewedFile(reviewedFile, selectedURL: URL(fileURLWithPath: configPath))
            before = preview
            preview = ""
            self.reviewedFile = nil
            problem = "Applied the reviewed configuration. Restart the coding tool to load Scratchpad MCP."
        } catch { problem = "Could not apply the reviewed configuration: \(error.localizedDescription)" }
    }
}

private enum SetupError: Error, LocalizedError {
    case connectionNotConfigured
    var errorDescription: String? { "Configure the server URL and SSH key references in Authentication before generating client setup." }
}

private struct ConfigColumn: View {
    let title: String
    let text: String
    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(title).font(.caption.weight(.semibold))
            ScrollView([.horizontal, .vertical]) { Text(text.isEmpty ? "(empty file)" : text).font(.system(.caption2, design: .monospaced)).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding(8) }
                .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
        }.frame(maxWidth: .infinity)
    }
}
