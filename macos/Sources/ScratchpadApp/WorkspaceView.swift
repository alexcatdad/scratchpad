import SwiftUI
import ScratchpadNative

enum WorkspacePage: String, CaseIterable, Identifiable {
    case overview = "Overview"
    case captures = "Captures"
    case connection = "Connection"
    case tools = "AI Tools"
    case general = "General"
    var id: Self { self }
    var icon: String {
        switch self {
        case .overview: "house"
        case .captures: "tray.full"
        case .connection: "key"
        case .tools: "chevron.left.forwardslash.chevron.right"
        case .general: "gearshape"
        }
    }
}

struct WorkspaceView: View {
    @EnvironmentObject private var model: AppModel
    var body: some View {
        NavigationSplitView {
            List(WorkspacePage.allCases, selection: $model.workspacePage) { page in
                HStack {
                    Label(page.rawValue, systemImage: page.icon)
                    Spacer()
                    if page == .captures, attentionCount > 0 {
                        Text("\(attentionCount)").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                            .accessibilityLabel("\(attentionCount) need attention")
                    }
                }
                .tag(page as WorkspacePage?)
            }
            .navigationSplitViewColumnWidth(min: 190, ideal: 210)
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        StatusIcon(tone: model.presentation.tone, size: 12)
                        Text(model.presentation.word).font(.caption.weight(.semibold))
                    }
                    Text("Scratchpad \(AppIdentity.versionLabel)").font(.caption2).foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
            }
        } detail: {
            switch model.workspacePage ?? .overview {
            case .overview: OverviewPage()
            case .captures: CapturesPage()
            case .connection: ConnectionPage()
            case .tools: ToolsPage()
            case .general: GeneralPage()
            }
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button { model.refresh(); model.loadReceipts() } label: { Label("Refresh", systemImage: "arrow.clockwise") }
            }
        }
        .onAppear { model.refresh(); model.loadConfiguration(); model.loadReceipts() }
        .alert("Scratchpad", isPresented: Binding(get: { model.errorMessage != nil }, set: { if !$0 { model.errorMessage = nil } })) {
            Button("OK", role: .cancel) { model.errorMessage = nil }
        } message: { Text(model.errorMessage ?? "") }
    }
    private var attentionCount: Int {
        guard let s = model.status else { return 0 }
        return s.needsSelection + s.blocked + s.rejected
    }
}

struct OverviewPage: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.openWindow) private var openWindow
    var body: some View {
        let p = model.presentation
        Form {
            Section {
                HStack(spacing: 16) {
                    Image(systemName: p.tone.symbol)
                        .font(.system(size: 40))
                        .foregroundStyle(p.tone.color)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(p.headline).font(.title2.weight(.bold))
                        Text(model.activity ?? p.detail).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    Spacer()
                    if let action = p.action, let title = p.actionTitle {
                        Button(title) { runStatusAction(action, model: model, openWindow: openWindow) }
                            .buttonStyle(ProminentButtonStyle())
                            .disabled(model.activity != nil)
                    }
                }
                .padding(.vertical, 6)
            }
            Section("Status") {
                ForEach(p.rows, id: \.title) { row in
                    LabeledContent {
                        HStack(spacing: 6) { StatusIcon(tone: row.tone); Text(row.value) }
                    } label: { Text(row.title) }
                }
                if let server = model.configuration.serverURL.nilIfEmpty {
                    LabeledContent("Server address", value: server)
                }
            }
            Section {
                if model.receipts.isEmpty {
                    Text("Captures from your AI tools appear here.").foregroundStyle(.secondary)
                } else {
                    ForEach(model.receipts.prefix(6)) { receipt in
                        HStack(spacing: 10) {
                            Text(ReceiptLabels.type(receipt)).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                                .frame(width: 76, alignment: .leading)
                            Text(ReceiptLabels.title(receipt)).lineLimit(1)
                            Spacer()
                            HStack(spacing: 5) {
                                StatusIcon(tone: ReceiptLabels.tone(receipt.state), size: 11)
                                Text(ReceiptLabels.state(receipt.state))
                            }
                            .font(.caption).foregroundStyle(.secondary)
                        }
                        .accessibilityElement(children: .combine)
                    }
                }
            } header: {
                HStack {
                    Text("Recent activity")
                    Spacer()
                    Button("Show All") { model.workspacePage = .captures }.buttonStyle(.link)
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Overview")
    }
}

private enum CaptureFilter: String, CaseIterable, Identifiable {
    case all = "All", attention = "Needs You", waiting = "Waiting", synced = "Synced"
    var id: Self { self }
    func includes(_ state: String) -> Bool {
        switch self {
        case .all: true
        case .attention: ["needs_selection", "blocked", "rejected"].contains(state)
        case .waiting: state == "pending"
        case .synced: state == "delivered"
        }
    }
}

struct CapturesPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var selection: String?
    @State private var filter: CaptureFilter = .all
    @State private var discardTarget: Receipt?

    private var filtered: [Receipt] { model.receipts.filter { filter.includes($0.state) } }

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 8) {
                Picker("Show", selection: $filter) {
                    ForEach(CaptureFilter.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .padding(.horizontal, 10)
                .padding(.top, 10)
                if filtered.isEmpty {
                    EmptyStateView(title: filter == .all ? "No captures yet" : "Nothing here", symbol: "tray", detail: filter == .all ? "Captures from your AI tools appear here." : "No captures match this filter.")
                } else {
                    List(filtered, selection: $selection) { receipt in
                        VStack(alignment: .leading, spacing: 3) {
                            Text(ReceiptLabels.title(receipt)).font(.body.weight(.medium)).lineLimit(2)
                            HStack(spacing: 5) {
                                StatusIcon(tone: ReceiptLabels.tone(receipt.state), size: 10)
                                Text("\(ReceiptLabels.type(receipt)) · \(ReceiptLabels.state(receipt.state)) · \(ReceiptLabels.when(receipt.acceptedAt))")
                            }
                            .font(.caption).foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 3)
                        .tag(receipt.receiptId)
                        .accessibilityElement(children: .combine)
                        .contextMenu {
                            if receipt.state != "delivered" { Button("Discard…", role: .destructive) { discardTarget = receipt } }
                        }
                    }
                    if model.hasMoreReceipts {
                        Button(model.loadingMoreReceipts ? "Loading…" : "Load More") { model.loadMoreReceipts() }
                            .disabled(model.loadingMoreReceipts)
                            .padding(.bottom, 8)
                    }
                }
            }
            .frame(width: 340)
            Divider()
            Group {
                if let receipt = currentReceipt {
                    CaptureDetail(receipt: receipt) { discardTarget = receipt }
                } else {
                    EmptyStateView(title: "Select a capture", symbol: "doc.text", detail: "Details and actions appear here.")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .navigationTitle("Captures")
        .onAppear { model.setQueueVisible(true) }
        .onDisappear { model.setQueueVisible(false) }
        .onChange(of: selection) { receiptID in model.selectReceipt(receiptID) }
        .onChange(of: model.selectedReceiptID) { receiptID in
            if selection != receiptID { selection = receiptID }
        }
        .confirmationDialog("Discard this capture?", isPresented: Binding(get: { discardTarget != nil }, set: { if !$0 { discardTarget = nil } }), titleVisibility: .visible) {
            Button("Discard Capture", role: .destructive) { if let target = discardTarget { model.discard(target) }; discardTarget = nil }
            Button("Cancel", role: .cancel) { discardTarget = nil }
        } message: { Text("It has not reached the server. Discarding removes it from this Mac permanently.") }
    }

    private var currentReceipt: Receipt? {
        guard let selection else { return nil }
        if let detail = model.selectedReceipt, detail.receiptId == selection { return detail }
        return model.receipts.first { $0.receiptId == selection }
    }
}

private struct CaptureDetail: View {
    @EnvironmentObject private var model: AppModel
    let receipt: Receipt
    let discard: () -> Void
    @State private var selectedProject = ""

    var body: some View {
        Form {
            Section {
                VStack(alignment: .leading, spacing: 4) {
                    Text(ReceiptLabels.type(receipt)).font(.callout.weight(.semibold)).foregroundStyle(.secondary)
                    Text(ReceiptLabels.title(receipt)).font(.title3.weight(.bold)).textSelection(.enabled)
                }
                .padding(.vertical, 4)
            }
            Section {
                HStack(alignment: .top, spacing: 10) {
                    StatusIcon(tone: ReceiptLabels.tone(receipt.state), size: 16)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(ReceiptLabels.state(receipt.state)).font(.body.weight(.semibold))
                        Text(explanation).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    }
                }
                .accessibilityElement(children: .combine)
                if receipt.state == "needs_selection" {
                    HStack {
                        Picker("Project", selection: $selectedProject) {
                            Text("Choose a project").tag("")
                            ForEach(model.projects) { Text($0.name).tag($0.id) }
                        }
                        Button("Assign") { model.selectProject(receiptID: receipt.receiptId, projectID: selectedProject) }
                            .buttonStyle(ProminentButtonStyle())
                            .disabled(selectedProject.isEmpty)
                    }
                }
            }
            if let content = receipt.content {
                Section("Details") {
                    LabeledContent("Project", value: projectName(content.scope.projectId))
                    LabeledContent("Saved on this Mac", value: ReceiptLabels.when(receipt.clientSavedAt ?? receipt.acceptedAt))
                    LabeledContent("Source", value: "\(content.record.authority.capitalized), \(content.record.confidence) confidence")
                    if let directory = content.scope.workingDirectory, !directory.isEmpty {
                        LabeledContent("Working directory", value: directory)
                    }
                }
                Section {
                    DisclosureGroup("Technical details") {
                        VStack(alignment: .leading, spacing: 6) {
                            LabeledContent("Receipt", value: receipt.receiptId)
                            LabeledContent("Request", value: content.requestId)
                            Text(content.record.payload.prettyPrinted() ?? "Payload could not be displayed.")
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                            if let git = content.gitContext?.prettyPrinted() {
                                Text(git).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                            }
                        }
                        .font(.caption)
                    }
                }
            } else if let id = receipt.centralRecordId {
                Section("Details") {
                    LabeledContent("Record", value: id)
                    if let when = receipt.serverRecordedAt { LabeledContent("Synced", value: ReceiptLabels.when(when)) }
                }
            }
            if receipt.state != "delivered", receipt.content != nil {
                Section {
                    Button("Discard…", role: .destructive, action: discard)
                }
            }
        }
        .formStyle(.grouped)
        .onAppear { if receipt.state == "needs_selection" { model.loadProjects() } }
    }

    private var explanation: String {
        switch receipt.state {
        case "delivered": "The server has this capture."
        case "pending": "Saved on this Mac. It syncs automatically when the server is reachable."
        case "needs_selection": "Scratchpad could not tell which project this came from. Choose one and it syncs."
        case "blocked": "Syncing is blocked: \(code). It retries once the cause is fixed."
        case "rejected": "The server refused this capture: \(code). Review it, then discard or keep it."
        default: "Saved on this Mac."
        }
    }
    private var code: String { receipt.deliveryCode?.replacingOccurrences(of: "_", with: " ").lowercased() ?? "no reason given" }
    private func projectName(_ id: String?) -> String {
        guard let id else { return "Not assigned" }
        return model.projects.first { $0.id == id }?.name ?? id
    }
}

extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
