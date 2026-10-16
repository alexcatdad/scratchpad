import AppKit
import Combine
import Foundation
import ScratchpadNative
import Sparkle

@MainActor
final class UpdateCoordinator: NSObject, ObservableObject, SPUUpdaterDelegate {
    @Published private(set) var configured = false
    @Published private(set) var pendingVersion: String?
    @Published private(set) var operationMessage: String?

    private var updater: SPUUpdater?
    private let userDriver = SparkleApprovalDriver()
    private let supervisor = ServiceSupervisor()

    override init() {
        super.init()
        userDriver.onStatus = { [weak self] message in self?.operationMessage = message }
        userDriver.onVersion = { [weak self] version in self?.pendingVersion = version }
        userDriver.onQuiescenceFailure = { [weak self] message in
            self?.restoreServiceAfterQuiescenceFailure()
            self?.operationMessage = message
        }
        let feed = Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") as? String ?? ""
        let publicKey = Bundle.main.object(forInfoDictionaryKey: "SUPublicEDKey") as? String ?? ""
        guard !feed.isEmpty, !publicKey.isEmpty else { return }
        configured = true
        let sparkleUpdater = SPUUpdater(hostBundle: Bundle.main, applicationBundle: Bundle.main, userDriver: userDriver, delegate: self)
        updater = sparkleUpdater
        do {
            try sparkleUpdater.start()
        } catch {
            operationMessage = "Updater could not start: \(error.localizedDescription)"
        }
    }

    var automaticChecks: Bool {
        get { updater?.automaticallyChecksForUpdates ?? false }
        set { updater?.automaticallyChecksForUpdates = newValue }
    }

    func checkForUpdates() {
        updater?.checkForUpdates()
    }

    func updater(_ updater: SPUUpdater, shouldProceedWithUpdate updateItem: SUAppcastItem, updateCheck: SPUUpdateCheck) throws {
        let properties = updateItem.propertiesDictionary
        guard let schema = try? Self.readQueueSchema(),
              UpdateCompatibility.isCompatible(properties: properties, installedSchema: schema) else {
            throw updaterError("This update does not declare support for the local capture queue schema.")
        }
    }

    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        pendingVersion = item.displayVersionString
    }

    func updaterDidNotFindUpdate(_ updater: SPUUpdater) {
        pendingVersion = nil
    }

    func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
        // If a previous approved update had already quiesced the service,
        // restore it according to its persisted Pause/Stop state.
        if UserDefaults.standard.bool(forKey: "restartServiceAfterSparkleUpdate") {
            restoreServiceAfterFailedUpdate()
        }
        operationMessage = "Update could not be completed: \(error.localizedDescription)"
    }

    func restoreServiceAfterFailedUpdate() {
        UserDefaults.standard.set(false, forKey: "restartServiceAfterSparkleUpdate")
        restoreServiceRespectingPersistentState()
    }

    private func restoreServiceAfterQuiescenceFailure() {
        restoreServiceRespectingPersistentState()
    }

    private func restoreServiceRespectingPersistentState() {
        Task { @MainActor [weak self] in
            let failure = await Task.detached {
                Self.restoreServiceRespectingPersistentState()
            }.value
            if !UserDefaults.standard.bool(forKey: "restartServiceAfterSparkleUpdate") {
                Self.finishTemporaryAbsenceSuppression()
            }
            if let failure { self?.operationMessage = failure }
        }
    }

    nonisolated static func restoreServiceRespectingPersistentState() -> String? {
        restoreServiceRespectingPersistentStateImpl()
    }

    private static func finishTemporaryAbsenceSuppression() {
        var lifecycle = ServiceLifecycleObservationRevision(
            value: UserDefaults.standard.integer(forKey: "serviceLifecycleRevision"),
            serviceExpectedToRun: UserDefaults.standard.bool(forKey: "serviceExpectedToRun"),
            absenceSuppressed: UserDefaults.standard.bool(forKey: "serviceAbsenceSuppressed")
        )
        _ = lifecycle.finishUpdaterRecovery()
        UserDefaults.standard.set(lifecycle.value, forKey: "serviceLifecycleRevision")
        UserDefaults.standard.set(lifecycle.serviceExpectedToRun, forKey: "serviceExpectedToRun")
        UserDefaults.standard.set(lifecycle.absenceSuppressed, forKey: "serviceAbsenceSuppressed")
    }

    nonisolated private static func restoreServiceRespectingPersistentStateImpl() -> String? {
        let client = ServiceClient(timeout: 2)
        let socket = client.socketPath
        let socketExists = FileManager.default.fileExists(atPath: socket)
        var resumeAccepted = false
        var healthy = false
        if socketExists {
            let resumed = (try? client.call("resume_after_update", body: EmptyBody(), as: EmptyResponse.self)) != nil
            resumeAccepted = resumed
            healthy = resumed && waitForHealthyService(client, timeout: 5)
        }

        switch ServiceRecoveryPolicy.action(socketExists: socketExists, resumeAccepted: resumeAccepted, healthy: healthy) {
        case .alreadyRestored:
            return nil
        case .waitForExit:

            // A production quiesce may have acknowledged and committed exit
            // while its listener is still draining. A rejected resume cannot
            // bring that process back, so wait for its socket to disappear
            // before launching a replacement.
            guard waitForSocketToDisappear(socket, timeout: 30) else {
                return "The local service is still draining its update handoff. Retry recovery after it exits."
            }
        case .launchRespectingPersistentState:
            break
        }

        do {
            let process = try ServiceSupervisor().launchRespectingPersistentState()
            let deadline = Date().addingTimeInterval(15)
            while Date() < deadline {
                if waitForHealthyService(client, timeout: 0.25) { return nil }
                if !process.isRunning {
                    // A clean early exit is the expected persisted Stop state.
                    if process.terminationStatus == 0 { return nil }
                    if waitForHealthyService(client, timeout: 0.25) { return nil }
                    return "The local service could not be restored after the update. Start it from the Scratchpad menu."
                }
                Thread.sleep(forTimeInterval: 0.1)
            }
            return "The local service did not become available after the update. Start it from the Scratchpad menu."
        } catch {
            return "The local service could not be restored after the update. Start it from the Scratchpad menu."
        }
    }

    nonisolated private static func waitForHealthyService(_ client: ServiceClient, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if let status = try? client.call("status", body: EmptyBody(), as: ServiceStatus.self),
               status.lifecycle == "running" || status.lifecycle == "paused" {
                return true
            }
            Thread.sleep(forTimeInterval: 0.1)
        } while Date() < deadline
        return false
    }

    nonisolated private static func waitForSocketToDisappear(_ path: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while FileManager.default.fileExists(atPath: path) {
            guard Date() < deadline else { return false }
            Thread.sleep(forTimeInterval: 0.1)
        }
        return true
    }

    nonisolated private static func readQueueSchema() throws -> Int {
        let executable = ServiceSupervisor.serviceURL()
        let process = Process()
        let output = Pipe()
        process.executableURL = executable
        process.arguments = ["--schema-version"]
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        try process.run()
        let bytes = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0,
              let text = String(data: bytes, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
              let value = Int(text), value >= 0 else {
            throw updaterError("The local queue schema could not be inspected safely.")
        }
        if value > 0 { return value }
        guard let initialSchema = Bundle.main.object(forInfoDictionaryKey: "QueueSchemaVersion") as? Int,
              initialSchema > 0 else {
            throw updaterError("This app does not declare its initial queue schema.")
        }
        return initialSchema
    }
}

private func updaterError(_ message: String) -> NSError {
    NSError(domain: "com.alexcatdad.scratchpad.updater", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
}
