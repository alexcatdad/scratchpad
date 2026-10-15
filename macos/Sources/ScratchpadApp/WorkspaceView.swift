import SwiftUI
import ScratchpadNative

private enum WorkspacePage: String, CaseIterable, Identifiable {
    case queue = "Pending captures"
    case projects = "Projects"
    case setup = "MCP setup"
    case authentication = "Authentication"
    case settings = "Settings"
    var id: String { rawValue }
    var icon: String {
        switch self {
        case .queue: "tray"
        case .projects: "folder"
        case .setup: "chevron.left.forwardslash.chevron.right"
        case .authentication: "key"
        case .settings: "gearshape"
        }
    }
}

struct WorkspaceView: View {
    @EnvironmentObject private var model: AppModel
    @State private var page: WorkspacePage? = .queue
    var body: some View {
        NavigationSplitView {
            List(WorkspacePage.allCases, selection: $page) { item in
                Label(item.rawValue, systemImage: item.icon).tag(item as WorkspacePage?)
            }
            .navigationTitle("Scratchpad")
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 4) {
                    Label(connectionLabel, systemImage: connectionIcon)
                        .font(.caption)
                    Text("Local capture \(localSaveReady ? "ready" : "not available")")
                        .font(.caption2).foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading).padding()
            }
        } detail: {
            switch page ?? .queue {
            case .queue: QueuePage()
            case .projects: ProjectsPage()
            case .setup: MCPSetupPage()
            case .authentication: AuthenticationPage()
            case .settings: SettingsPage()
            }
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) { Button { model.refresh() } label: { Label("Refresh", systemImage: "arrow.clockwise") } }
        }
        .onAppear { model.refresh(); model.loadConfiguration(); model.loadReceipts() }
        .onReceive(NotificationCenter.default.publisher(for: .showAuthenticationPage)) { _ in page = .authentication }
        .alert("Scratchpad", isPresented: Binding(get: { model.errorMessage != nil }, set: { if !$0 { model.errorMessage = nil } })) {
            Button("OK", role: .cancel) { model.errorMessage = nil }
        } message: { Text(model.errorMessage ?? "") }
    }
    private var connectionLabel: String {
        switch model.status?.connection?.effectiveState() {
        case "connected": "Server reachable"
        case "connectivity": "Server offline"
        case "authentication", "credential_changed": "Authentication needs attention"
        case "server_incompatible": "Server incompatible"
        default: "Server status not checked"
        }
    }
    private var connectionIcon: String { model.status?.connection?.effectiveState() == "connected" ? "circle.fill" : "circle" }
    private var localSaveReady: Bool {
        guard let status = model.status else { return false }
        return (status.lifecycle == "running" || status.lifecycle == "paused") && status.keyAvailable && status.storageAvailable != false
    }
}

struct QueuePage: View {
    @EnvironmentObject private var model: AppModel
    @State private var selection: String?
    @State private var discardTarget: Receipt?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 5) {
                Text("Pending captures").font(.largeTitle.bold())
                Text("Each capture keeps its original project and delivery identity.").foregroundStyle(.secondary)
            }
            if let status = model.status {
                HStack(spacing: 18) {
                    CountBadge(title: "Waiting", count: status.pending)
                    CountBadge(title: "Blocked", count: status.blocked)
                    CountBadge(title: "Rejected", count: status.rejected)
                    CountBadge(title: "Needs project", count: status.needsSelection)
                    Spacer()
                    Text(status.lifecycle.capitalized).font(.caption).padding(.horizontal, 9).padding(.vertical, 5).background(.quaternary, in: Capsule())
                }
            } else {
                EmptyStateView(title: "Service unavailable", symbol: "bolt.slash", detail: "Receipt and local-save status have not been confirmed.")
            }
            if model.receipts.isEmpty {
                EmptyStateView(title: "No retained receipts", symbol: "tray", detail: "Pending and recent delivery receipts appear here.")
                    .frame(maxHeight: .infinity)
            } else {
                List(model.receipts, selection: $selection) { receipt in
                    ReceiptRow(receipt: receipt)
                        .tag(receipt.receiptId)
                        .contextMenu {
                            if receipt.state != "delivered" { Button("Discard…", role: .destructive) { discardTarget = receipt } }
                        }
                }
                if model.hasMoreReceipts {
                    HStack {
                        Spacer()
                        Button(model.loadingMoreReceipts ? "Loading…" : "Load more captures") {
                            model.loadMoreReceipts()
                        }
                        .disabled(model.loadingMoreReceipts)
                        Spacer()
                    }
                }
                if let receipt = model.receipts.first(where: { $0.receiptId == selection }),
                   let selected = model.selectedReceipt, selected.receiptId == receipt.receiptId {
                    ReceiptDetail(receipt: selected) {
                        discardTarget = selected
                    }
                } else if let receipt = model.receipts.first(where: { $0.receiptId == selection }) {
                    ReceiptDetail(receipt: receipt) {
                        discardTarget = receipt
                    }
                }
            }
        }
        .padding(22)
        .onAppear { model.setQueueVisible(true) }
        .onDisappear { model.setQueueVisible(false) }
        .onChange(of: selection) { receiptID in
            model.selectReceipt(receiptID)
        }
        .onChange(of: model.selectedReceiptID) { receiptID in
            if selection != receiptID { selection = receiptID }
        }
        .confirmationDialog("Discard this undelivered capture?", isPresented: Binding(get: { discardTarget != nil }, set: { if !$0 { discardTarget = nil } }), titleVisibility: .visible) {
            Button("Discard capture", role: .destructive) { if let target = discardTarget { model.discard(target) }; discardTarget = nil }
            Button("Cancel", role: .cancel) { discardTarget = nil }
        } message: { Text("This permanently removes the retained local content. Delivered receipts cannot be discarded here.") }
    }
}

struct EmptyStateView: View {
    let title: String
    let symbol: String
    let detail: String
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: symbol).font(.largeTitle).foregroundStyle(.secondary)
            Text(title).font(.headline)
            Text(detail).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }.frame(maxWidth: .infinity, maxHeight: .infinity).padding()
    }
}

private struct CountBadge: View {
    let title: String
    let count: Int
    var body: some View { VStack(alignment: .leading, spacing: 3) { Text("\(count)").font(.title2.bold()); Text(title).font(.caption).foregroundStyle(.secondary) } }
}

private struct ReceiptRow: View {
    let receipt: Receipt
    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: receipt.state == "delivered" ? "checkmark.circle.fill" : (receipt.state == "blocked" || receipt.state == "rejected" ? "exclamationmark.circle.fill" : "clock"))
                .foregroundStyle(receipt.state == "delivered" ? .green : (receipt.state == "blocked" || receipt.state == "rejected" ? .orange : .secondary))
            VStack(alignment: .leading, spacing: 3) {
                Text(receipt.content?.record.title ?? (receipt.state == "delivered" ? "Delivered capture" : "Capture awaiting delivery"))
                    .lineLimit(1)
                Text("\(receipt.state.capitalized) · \(receipt.acceptedAt)").font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            if let code = receipt.deliveryCode { Text(code.replacingOccurrences(of: "_", with: " ")).font(.caption).foregroundStyle(.secondary) }
        }
        .padding(.vertical, 3)
    }
}

private struct ReceiptDetail: View {
    @EnvironmentObject private var model: AppModel
    let receipt: Receipt
    let discard: () -> Void
    @State private var selectedProject = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            HStack { Text(receipt.state == "delivered" ? "Server receipt" : "Local receipt").font(.headline); Spacer(); Text(receipt.receiptId).font(.caption.monospaced()).foregroundStyle(.secondary) }
            if let content = receipt.content {
                Text(content.record.title).font(.title3.weight(.semibold))
                Text(content.record.type.capitalized).font(.caption).foregroundStyle(.secondary)
                Text("Saved locally at \(receipt.clientSavedAt ?? receipt.acceptedAt)").font(.caption)
                Group {
                    LabeledContent("Authority", value: content.record.authority)
                    LabeledContent("Confidence", value: content.record.confidence)
                    if let reason = content.record.confidenceReason, !reason.isEmpty {
                        LabeledContent("Confidence reason", value: reason)
                    }
                    if let happenedAt = content.record.happenedAt, !happenedAt.isEmpty {
                        LabeledContent("Happened at", value: happenedAt)
                    }
                    LabeledContent("Project", value: content.scope.projectId ?? "Not assigned")
                    LabeledContent("Working directory", value: content.scope.workingDirectory ?? "Not provided")
                    LabeledContent("Request", value: content.requestId)
                }
                .font(.caption)
                DisclosureGroup("Captured payload") {
                    ScrollView([.horizontal, .vertical]) {
                        Text(content.record.payload.prettyPrinted() ?? "Payload could not be displayed.")
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(8)
                    }
                    .frame(maxHeight: 220)
                    .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 6))
                }
                if let gitContext = content.gitContext {
                    DisclosureGroup("Git provenance") {
                        ScrollView([.horizontal, .vertical]) {
                            Text(gitContext.prettyPrinted() ?? "Git provenance could not be displayed.")
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(8)
                        }
                        .frame(maxHeight: 150)
                        .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 6))
                    }
                }
                if receipt.state == "needs_selection" {
                    HStack {
                        Picker("Project", selection: $selectedProject) {
                            Text("Choose a project").tag("")
                            ForEach(model.projects) { Text($0.name).tag($0.id) }
                        }
                        Button("Assign") { model.selectProject(receiptID: receipt.receiptId, projectID: selectedProject) }.disabled(selectedProject.isEmpty)
                    }
                } else {
                    Text("This capture is saved locally. It has not been confirmed by the Scratchpad server.").font(.caption).foregroundStyle(.secondary)
                }
                HStack { Spacer(); Button("Discard…", role: .destructive, action: discard) }
            } else if let id = receipt.centralRecordId {
                Text("The server confirmed this capture.").font(.callout)
                Text("Record ID: \(id)").font(.caption.monospaced())
                if let when = receipt.serverRecordedAt { Text("Server recorded at \(when)").font(.caption).foregroundStyle(.secondary) }
            } else {
                Text("No capture content is retained for this receipt.").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(14).background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 10))
        .onAppear { if receipt.state == "needs_selection" { model.loadProjects() } }
    }
}
