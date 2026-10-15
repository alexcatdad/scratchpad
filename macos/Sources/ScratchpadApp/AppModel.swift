import Foundation
import Combine
import ScratchpadNative

@MainActor
final class AppModel: ObservableObject {
    @Published private(set) var status: ServiceStatus?
    @Published private(set) var statusObservedAt: Date?
    @Published private(set) var receipts: [Receipt] = []
    @Published private(set) var selectedReceipt: Receipt?
    @Published private(set) var selectedReceiptID: String?
    @Published private(set) var hasMoreReceipts = false
    @Published private(set) var loadingMoreReceipts = false
    @Published private(set) var projects: [Project] = []
    @Published var configuration = ServiceConfiguration() { didSet { configurationRevision += 1 } }
    @Published private(set) var verified: VerifyResult?
    @Published var errorMessage: String?
    @Published private(set) var busy = false
    let client = ServiceClient()
    let supervisor = ServiceSupervisor()
    private var incidentTracker = IncidentTracker(
        localSaveIncidentAlreadyNotified: UserDefaults.standard.bool(forKey: "localSaveIncidentActive")
    )
    private var observationRevision = ServiceLifecycleObservationRevision(
        value: UserDefaults.standard.integer(forKey: "serviceLifecycleRevision"),
        serviceExpectedToRun: UserDefaults.standard.bool(forKey: "serviceExpectedToRun"),
        absenceSuppressed: UserDefaults.standard.bool(forKey: "serviceAbsenceSuppressed")
    )
    private var refreshTimer: Timer?
    private var startupProcess: Process?
    private var configurationRevision = 0
    private var queueVisible = false
    private var lastQueueRefresh = Date.distantPast
    private var queuedOperations: [QueuedOperation] = []
    private var receiptPageCursors: [String?] = [nil]
    private var nextReceiptCursor: String?

    private struct ReceiptPages: Decodable, Sendable {
        let pages: [[Receipt]]
        let cursors: [String?]
        let nextCursor: String?
    }

    private struct QueuedOperation {
        let key: String?
        let launch: @MainActor () -> Void
    }

    init() {
        let defaults = UserDefaults.standard
        if defaults.bool(forKey: "serviceAbsenceSuppressed"),
           !defaults.bool(forKey: "restartServiceAfterSparkleUpdate"),
           defaults.bool(forKey: "serviceExpectedToRun") {
            defaults.set(false, forKey: "serviceAbsenceSuppressed")
            defaults.set(defaults.integer(forKey: "serviceLifecycleRevision") + 1, forKey: "serviceLifecycleRevision")
        }
        if UserDefaults.standard.bool(forKey: "restartServiceAfterSparkleUpdate") {
            Task { @MainActor [weak self] in
                await self?.recoverServiceAfterUpdaterRestart()
            }
        }
        refreshTimer = Timer.scheduledTimer(withTimeInterval: 10, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                self.refresh()
                guard self.queueVisible, Date().timeIntervalSince(self.lastQueueRefresh) >= 30 else { return }
                self.lastQueueRefresh = Date()
                self.loadReceipts()
            }
        }
    }

    func refresh() {
        _ = currentObservationRevision()
        guard observationRevision.canRefresh else { return }
        let revision = currentObservationRevision()
        Task { @MainActor [weak self] in
            let outcome: OperationOutcome<ServiceStatus> = await Task.detached {
                do { return .success(try ServiceClient().call("status", body: EmptyBody(), as: ServiceStatus.self)) }
                catch { return .failure(error.localizedDescription) }
            }.value
            guard let self, self.isCurrentObservation(revision) else { return }
            switch outcome {
            case .success(let value):
                self.acceptServiceStatus(value)
            case .failure:
                self.status = nil
                self.statusObservedAt = nil
                guard !self.observationRevision.isStartPending else { return }
                self.recordIncidentEvents(self.incidentTracker.observe(
                    nil,
                    serviceExpected: UserDefaults.standard.bool(forKey: "serviceExpectedToRun"),
                    absenceSuppressed: UserDefaults.standard.bool(forKey: "serviceAbsenceSuppressed")
                ))
            }
        }
    }

    func loadReceipts() {
        let client = self.client
        let pageCount = receiptPageCursors.count
        perform({
            var pages: [[Receipt]] = []
            var cursors: [String?] = []
            var cursor: String?
            for _ in 0..<pageCount {
                cursors.append(cursor)
                let page = try client.call("list_receipts", body: ListReceiptsBody(limit: 100, cursor: cursor, state: nil), as: ReceiptList.self)
                pages.append(page.receipts)
                cursor = page.nextCursor
                if cursor == nil { break }
            }
            return ReceiptPages(pages: pages, cursors: cursors, nextCursor: cursor)
        }, queueKey: "receipt-pages") { (result: ReceiptPages) in
            self.receiptPageCursors = result.cursors
            self.nextReceiptCursor = result.nextCursor
            self.hasMoreReceipts = result.nextCursor != nil
            self.receipts = result.pages.flatMap { $0 }
            guard let selected = self.selectedReceiptID,
                  self.receipts.contains(where: { $0.receiptId == selected })
            else {
                self.selectedReceiptID = nil
                self.selectedReceipt = nil
                return
            }
            self.loadReceiptDetail(selected)
        }
    }
    func loadMoreReceipts() {
        guard !loadingMoreReceipts, let cursor = nextReceiptCursor else { return }
        loadingMoreReceipts = true
        let client = self.client
        perform({
            try client.call("list_receipts", body: ListReceiptsBody(limit: 100, cursor: cursor, state: nil), as: ReceiptList.self)
        }, queueKey: "receipt-page-\(cursor)") { (result: ReceiptList) in
            self.loadingMoreReceipts = false
            self.receiptPageCursors.append(cursor)
            let knownIDs = Set(self.receipts.map(\.receiptId))
            self.receipts.append(contentsOf: result.receipts.filter { !knownIDs.contains($0.receiptId) })
            self.nextReceiptCursor = result.nextCursor
            self.hasMoreReceipts = result.nextCursor != nil
        } onFailure: { _ in
            self.loadingMoreReceipts = false
        }
    }
    func loadReceiptDetail(_ receiptID: String) {
        let client = self.client
        perform({ try client.call("lookup_receipt", body: ReceiptBody(receiptId: receiptID), as: Receipt.self) }, queueKey: "receipt-detail") { value in
            guard self.selectedReceiptID == receiptID else { return }
            self.selectedReceipt = value
        }
    }
    func selectReceipt(_ receiptID: String?) {
        selectedReceiptID = receiptID
        selectedReceipt = nil
        if let receiptID { loadReceiptDetail(receiptID) }
    }
    func setQueueVisible(_ visible: Bool) {
        queueVisible = visible
        if visible {
            lastQueueRefresh = Date()
            loadReceipts()
        }
    }
    func loadProjects() {
        let client = self.client
        perform({ try client.call("list_projects", body: EmptyBody(), as: ProjectList.self) }) { self.projects = $0.projects }
    }
    func loadConfiguration() {
        let client = self.client
        let revision = configurationRevision
        perform({ try client.call("get_configuration", body: EmptyBody(), as: ConfigurationResult.self) }, queueKey: "configuration-read") { result in
            guard self.configurationRevision == revision else { return }
            self.configuration = result.configuration
        }
    }
    func saveConfiguration() {
        let client = self.client
        let configuration = self.configuration
        perform({ try client.call("set_configuration", body: ConfigurationBody(configuration: configuration), as: ConfigurationResult.self) }) { _ in
            self.verified = nil
            self.errorMessage = "Connection settings saved. Verify the connection before relying on delivery."
        }
    }
    func verifyConnection() {
        let client = self.client
        verified = nil
        perform({ try client.call("verify_connection", body: EmptyBody(), as: VerifyResult.self) }, completion: { result in
            self.verified = result
            self.refresh()
        }, onFailure: { _ in
            self.refresh()
        })
    }
    func setLifecycle(_ operation: String) {
        let revision = beginLifecycleIntent(operation: operation)
        let client = ServiceClient(timeout: ServiceClient.timeout(for: operation))
        perform({ try client.call(operation, body: EmptyBody(), as: EmptyResponse.self) }, completion: { _ in
            guard self.finishLifecycleIntent(revision, operation: operation, succeeded: true) else { return }
            if operation == "stop" {
                self.incidentTracker.resetLocalSaveIncident()
                self.recordIncidentEvents([])
            }
            self.refresh()
        }, onFailure: { error in
            guard self.finishLifecycleIntent(revision, operation: operation, succeeded: false) else { return }
            self.errorMessage = error
            self.refresh()
        })
    }
    func startService() {
        let revision = beginStartIntent()
        do {
            startupProcess = try supervisor.start { [weak self] exitCode in
                Task { @MainActor [weak self] in
                    guard let self, self.isCurrentStartIntent(revision) else { return }
                    _ = self.observationRevision.finishStartFailure(revision)
                    self.persistLifecyclePolicy()
                    self.startupProcess = nil
                    self.errorMessage = exitCode == 1
                        ? "The capture service exited during startup (code 1). Unlock the queue Keychain and check local queue access before retrying."
                        : "The capture service exited during startup (code \(exitCode)). Check its local configuration before retrying."
                    self.recordLocalSaveUnavailable()
                }
            }
            errorMessage = "Starting the capture service…"
        } catch {
            _ = observationRevision.finishStartFailure(revision)
            persistLifecyclePolicy()
            startupProcess = nil
            errorMessage = error.localizedDescription
            recordLocalSaveUnavailable()
            return
        }

        Task { @MainActor [weak self] in
            for _ in 0..<20 {
                let outcome: OperationOutcome<ServiceStatus> = await Task.detached {
                    do { return .success(try ServiceClient().call("status", body: EmptyBody(), as: ServiceStatus.self)) }
                    catch { return .failure(error.localizedDescription) }
                }.value
                guard let self, self.isCurrentStartIntent(revision) else { return }
                if case .success(let status) = outcome {
                    if status.lifecycle == "running" || status.lifecycle == "paused" {
                        self.acceptServiceStatus(status)
                        _ = self.observationRevision.finishStart(revision)
                        self.persistLifecyclePolicy()
                        self.startupProcess = nil
                        self.errorMessage = nil
                    }
                    return
                }
                if case .failure = outcome {
                    try? await Task.sleep(for: .milliseconds(250))
                }
            }
            guard let self, self.isCurrentStartIntent(revision) else { return }
            _ = self.observationRevision.finishStartFailure(revision)
            self.persistLifecyclePolicy()
            self.startupProcess = nil
            self.errorMessage = "The capture service has not become available yet. Check queue Keychain unlock and local storage status."
            self.recordLocalSaveUnavailable()
        }
    }

    private func recordLocalSaveUnavailable() {
        recordIncidentEvents(incidentTracker.observe(nil, serviceExpected: true))
    }

    private func acceptServiceStatus(_ value: ServiceStatus) {
        status = value
        statusObservedAt = .now
        observationRevision.observeLifecycle(value.lifecycle)
        persistLifecyclePolicy()
        recordIncidentEvents(incidentTracker.observe(
            value,
            serviceExpected: UserDefaults.standard.bool(forKey: "serviceExpectedToRun"),
            absenceSuppressed: UserDefaults.standard.bool(forKey: "serviceAbsenceSuppressed")
        ))
    }

    private func recordIncidentEvents(_ events: [IncidentEvent]) {
        UserDefaults.standard.set(incidentTracker.isLocalSaveIncidentActive, forKey: "localSaveIncidentActive")
        let notificationsEnabled = UserDefaults.standard.bool(forKey: "incidentNotificationsEnabled")
        for event in events {
            if notificationsEnabled { IncidentNotifier.deliver(event) }
        }
    }

    private func currentObservationRevision() -> Int {
        let defaults = UserDefaults.standard
        observationRevision.synchronize(
            to: defaults.integer(forKey: "serviceLifecycleRevision"),
            serviceExpectedToRun: defaults.bool(forKey: "serviceExpectedToRun"),
            absenceSuppressed: defaults.bool(forKey: "serviceAbsenceSuppressed")
        )
        return observationRevision.value
    }

    private func beginLifecycleIntent(operation: String) -> Int {
        _ = currentObservationRevision()
        let revision = observationRevision.beginLifecycleIntent(operation: operation)
        persistLifecyclePolicy()
        return revision
    }

    private func beginStartIntent() -> Int {
        _ = currentObservationRevision()
        let revision = observationRevision.beginStartIntent()
        persistLifecyclePolicy()
        return revision
    }

    private func isCurrentObservation(_ revision: Int) -> Bool {
        _ = currentObservationRevision()
        return observationRevision.accepts(revision)
    }

    private func isCurrentStartIntent(_ revision: Int) -> Bool {
        _ = currentObservationRevision()
        return observationRevision.acceptsStartStatus(revision)
    }

    private func finishLifecycleIntent(_ revision: Int, operation: String, succeeded: Bool) -> Bool {
        _ = currentObservationRevision()
        let finished = observationRevision.finishLifecycle(revision, operation: operation, succeeded: succeeded)
        if finished { persistLifecyclePolicy() }
        return finished
    }

    private func persistLifecyclePolicy() {
        let defaults = UserDefaults.standard
        defaults.set(observationRevision.value, forKey: "serviceLifecycleRevision")
        defaults.set(observationRevision.serviceExpectedToRun, forKey: "serviceExpectedToRun")
        defaults.set(observationRevision.absenceSuppressed, forKey: "serviceAbsenceSuppressed")
    }

    private func recoverServiceAfterUpdaterRestart() async {
        let failure = await Task.detached {
            UpdateCoordinator.restoreServiceRespectingPersistentState()
        }.value
        UserDefaults.standard.set(false, forKey: "restartServiceAfterSparkleUpdate")
        _ = currentObservationRevision()
        _ = observationRevision.finishUpdaterRecovery()
        persistLifecyclePolicy()
        if let failure { errorMessage = failure }
        refresh()
    }
    func discard(_ receipt: Receipt) {
        let client = self.client
        perform({ try client.call("discard_receipt", body: ReceiptBody(receiptId: receipt.receiptId), as: Receipt.self) }) { _ in
            self.selectedReceipt = nil
            self.selectedReceiptID = nil
            self.loadReceipts()
        }
    }
    func selectProject(receiptID: String, projectID: String) {
        let client = self.client
        let body = ProjectSelectionBody(receiptId: receiptID, projectId: projectID)
        perform({ try client.call("select_project", body: body, as: Receipt.self) }) { _ in
            self.loadReceipts()
        }
    }
    func setStartAtLogin(_ value: Bool) {
        do { try supervisor.setStartAtLogin(value) }
        catch { errorMessage = error.localizedDescription }
    }

    private func perform<T: Decodable & Sendable>(
        _ operation: @escaping @Sendable () throws -> T,
        queueKey: String? = nil,
        completion: @escaping @MainActor (T) -> Void,
        onFailure: @escaping @MainActor (String) -> Void = { _ in }
    ) {
        guard !busy else {
            if let queueKey, let pending = queuedOperations.firstIndex(where: { $0.key == queueKey }) {
                queuedOperations.remove(at: pending)
            }
            queuedOperations.append(QueuedOperation(key: queueKey) { [weak self] in
                self?.perform(operation, queueKey: queueKey, completion: completion, onFailure: onFailure)
            })
            return
        }
        busy = true
        errorMessage = nil
        Task { @MainActor [weak self] in
            let outcome: OperationOutcome<T> = await Task.detached {
                do { return .success(try operation()) }
                catch { return .failure(error.localizedDescription) }
            }.value
            guard let self else { return }
            switch outcome {
            case .success(let value): completion(value)
            case .failure(let message): self.errorMessage = message
                onFailure(message)
            }
            self.busy = false
            self.runNextOperation()
        }
    }

    private func runNextOperation() {
        guard !busy, !queuedOperations.isEmpty else { return }
        let next = queuedOperations.removeFirst()
        next.launch()
    }
}

struct EmptyResponse: Decodable, Sendable {}

private enum OperationOutcome<T: Sendable>: Sendable {
    case success(T)
    case failure(String)
}
