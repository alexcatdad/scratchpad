import AppKit
import MenubarKit
import ScratchpadNative
import SwiftUI

struct ConnectionPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var showAdvanced = false

    var body: some View {
        Form {
            Section {
                TextField(
                    "Address",
                    text: Binding(
                        get: { model.configuration.serverURL },
                        set: {
                            guard $0 != model.configuration.serverURL else { return }
                            model.configuration.serverURL = $0
                            model.clearDiscoveredKeys()
                        }), prompt: Text("https://scratchpad.example.com"))
                LabeledContent("Status") {
                    let server = model.presentation.rows.first { $0.title == "Server" }
                    HStack(spacing: 6) {
                        StatusIcon(tone: server?.tone ?? .off)
                        Text(
                            [server?.value, model.status?.serverVersion.map { "server \($0)" }].compactMap { $0 }
                                .joined(separator: " · "))
                    }
                }
            } header: {
                Text("Server")
            } footer: {
                Text(
                    "Link GitHub in the server dashboard first. Scratchpad then finds keys on this Mac that the server accepts."
                )
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
                        HStack(spacing: 6) {
                            StatusIcon(tone: .ok); Text("Verified \(ReceiptLabels.when(verified.verifiedAt))")
                        }
                    }
                    LabeledContent("Credential", value: verified.credentialId)
                } else {
                    LabeledContent(
                        "Signed in",
                        value: model.configuration.credentialFingerprint.isEmpty ? "Not connected" : "Not checked yet")
                }
                HStack {
                    Spacer()
                    Button("Check Again") { model.verifyConnection() }
                        .disabled(
                            model.configuration.serverURL.isEmpty || model.configuration.credentialFingerprint.isEmpty
                                || model.busy)
                }
            }

            Section {
                DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
                    TextField("Key fingerprint", text: $model.configuration.credentialFingerprint)
                    TextField("Public key file", text: $model.configuration.publicKeyPath)
                    TextField("Signing key reference", text: $model.configuration.signingKeyPath)
                    TextField(
                        "SSH agent socket",
                        text: Binding(
                            get: { model.configuration.sshAgentSocket },
                            set: {
                                guard $0 != model.configuration.sshAgentSocket else { return }
                                model.configuration.sshAgentSocket = $0
                                model.clearDiscoveredKeys()
                            }), prompt: Text("Default agent"))
                    HStack {
                        Text("The same settings as scratchpad connect in Terminal.").font(.caption).foregroundStyle(
                            .secondary)
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

struct GeneralPage: View {
    @EnvironmentObject private var model: AppModel
    /// Where the version label is read from; fixtures pass their own.
    var bundle: Bundle = .main
    @State private var loginEnabled = false
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
                    Label(
                        "Approve Scratchpad in System Settings › General › Login Items.",
                        systemImage: "exclamationmark.triangle.fill"
                    )
                    .foregroundStyle(.orange)
                }
            }

            NotificationSettingsSection(
                notifier: model.notifier,
                toggleTitle: "Notify me about problems",
                explanation: "Long outages, refused captures and saving failures. Counts only, never capture content.",
                offersSound: true,
                enabledKey: IncidentNotificationSettings.enabledKey,
                soundEnabledKey: IncidentNotificationSettings.soundEnabledKey
            )

            Section("Capture service") {
                LabeledContent("Version") {
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
                            Text("AI tools cannot save memory until you start it.").font(.caption).foregroundStyle(
                                .secondary)
                        }
                        Spacer()
                        Button("Start") { model.startService() }.buttonStyle(ProminentButtonStyle())
                    }
                } else {
                    HStack {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("Stop capture service")
                            Text("AI tools cannot save memory until you start it again.").font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Stop…", role: .destructive) { confirmStop = true }
                    }
                }
            }

            SettingsAboutSection(bundle: bundle, repository: URL(string: "https://github.com/alexcatdad/scratchpad"))
        }
        .formStyle(.grouped)
        .navigationTitle("General")
        .onAppear { loginEnabled = model.supervisor.loginItemStatus == .enabled }
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
