import AppKit
import Foundation
import ScratchpadNative
import Sparkle

/// A deliberately small Sparkle driver with separate download and install
/// decisions. The install reply remains pending until the local service has
/// drained and removed its socket.
@MainActor
final class SparkleApprovalDriver: NSObject, SPUUserDriver {
    var onStatus: ((String?) -> Void)?
    var onVersion: ((String?) -> Void)?
    var onQuiescenceFailure: ((String) -> Void)?

    private var cancellation: (() -> Void)?
    private var approvalGate = UpdateApprovalGate()

    func show(_ request: SPUUpdatePermissionRequest, reply: @escaping (SUUpdatePermissionResponse) -> Void) {
        reply(SUUpdatePermissionResponse(automaticUpdateChecks: true, automaticUpdateDownloading: NSNumber(value: false), sendSystemProfile: false))
    }

    func showUserInitiatedUpdateCheck(cancellation: @escaping () -> Void) {
        self.cancellation = cancellation
        onStatus?("Checking for updates…")
    }

    func showUpdateFound(with item: SUAppcastItem, state: SPUUserUpdateState, reply: @escaping (SPUUserUpdateChoice) -> Void) {
        cancellation = nil
        onVersion?(item.displayVersionString)
        guard !item.isInformationOnlyUpdate else {
            if let url = item.infoURL { NSWorkspace.shared.open(url) }
            reply(.dismiss)
            return
        }

        switch UpdateStagePolicy.action(sparkleStageRawValue: state.stage.rawValue) {
        case .downloadAutomatically:
            // Sparkle's supported .install choice starts the download when
            // the item is not yet downloaded. Installation is gated later by
            // showReady(toInstallAndRelaunch:).
            onStatus?("Downloading compatible Scratchpad update…")
            reply(.install)
        case .requestInstallApproval:
            let choice = alert(
                title: "Downloaded update ready",
                message: "Version \(item.displayVersionString) is downloaded. Installing it will first drain local saves, then restart Scratchpad.",
                primary: "Install and restart…",
                secondary: "Later"
            )
            if choice == .alertFirstButtonReturn {
                installAfterApproval(reply)
            } else {
                reply(.dismiss)
            }
        case .requireInstallOrCancel:
            // Sparkle documents that dismissing an installing update still
            // installs it on app termination. Offer explicit install or skip,
            // never a passive dismissal, for this legacy/already-staged state.
            let choice = alert(
                title: "A Scratchpad update is staged",
                message: "If left staged, Sparkle may install this update when Scratchpad quits. Install now after in-flight local saves finish, or cancel this staged update.",
                primary: "Install and restart…",
                secondary: "Cancel this staged update"
            )
            if choice == .alertFirstButtonReturn {
                installAfterApproval(reply)
            } else {
                reply(.skip)
            }
        }
    }

    func showUpdateReleaseNotes(with downloadData: SPUDownloadData) {}
    func showUpdateReleaseNotesFailedToDownloadWithError(_ error: Error) {}

    func showUpdateNotFoundWithError(_ error: Error) async {
        onStatus?(nil)
        let alert = NSAlert()
        alert.messageText = "Scratchpad is up to date"
        alert.informativeText = error.localizedDescription
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    func showUpdaterError(_ error: Error) async {
        onStatus?("Update could not be completed: \(error.localizedDescription)")
        let alert = NSAlert()
        alert.messageText = "Scratchpad update failed"
        alert.informativeText = error.localizedDescription
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    func showDownloadInitiated(cancellation: @escaping () -> Void) {
        self.cancellation = cancellation
        onStatus?("Downloading Scratchpad update…")
    }

    func showDownloadDidReceiveExpectedContentLength(_ expectedContentLength: UInt64) {}
    func showDownloadDidReceiveData(ofLength length: UInt64) {}
    func showDownloadDidStartExtractingUpdate() { onStatus?("Preparing the update…") }
    func showExtractionReceivedProgress(_ progress: Double) {}

    func showReady(toInstallAndRelaunch reply: @escaping (SPUUserUpdateChoice) -> Void) {
        let choice = alert(
            title: "Install Scratchpad update?",
            message: "Scratchpad will finish any in-flight local save before installing and restarting. Queue data and Pause/Stop state remain in place.",
            primary: "Install and restart",
            secondary: "Later"
        )
        guard choice == .alertFirstButtonReturn else {
            reply(.skip)
            onStatus?("Update kept for a later check.")
            return
        }
        installAfterApproval(reply)
    }

    func showInstallingUpdate(withApplicationTerminated applicationTerminated: Bool, retryTerminatingApplication: @escaping () -> Void) {
        onStatus?("Installing Scratchpad update…")
    }

    func showUpdateInstalledAndRelaunched(_ relaunched: Bool) async {
        onStatus?(nil)
    }

    func dismissUpdateInstallation() {
        cancellation = nil
        onStatus?(nil)
    }

    func showUpdateInFocus() {}

    private func alert(title: String, message: String, primary: String, secondary: String) -> NSApplication.ModalResponse {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.alertStyle = .informational
        alert.addButton(withTitle: primary)
        alert.addButton(withTitle: secondary)
        NSApp.activate(ignoringOtherApps: true)
        return alert.runModal()
    }

    private func installAfterApproval(_ reply: @escaping (SPUUserUpdateChoice) -> Void) {
        approvalGate.downloadedUpdateNeedsApproval()
        approvalGate.approve()
        approvalGate.beginQuiescence()
        onStatus?("Waiting for the local queue to finish its current save…")
        var lifecycle = ServiceLifecycleObservationRevision(
            value: UserDefaults.standard.integer(forKey: "serviceLifecycleRevision"),
            serviceExpectedToRun: UserDefaults.standard.bool(forKey: "serviceExpectedToRun"),
            absenceSuppressed: UserDefaults.standard.bool(forKey: "serviceAbsenceSuppressed")
        )
        _ = lifecycle.beginUpdaterQuiescence()
        UserDefaults.standard.set(lifecycle.value, forKey: "serviceLifecycleRevision")
        UserDefaults.standard.set(lifecycle.serviceExpectedToRun, forKey: "serviceExpectedToRun")
        UserDefaults.standard.set(lifecycle.absenceSuppressed, forKey: "serviceAbsenceSuppressed")
        Task {
            do {
                try await Task.detached { try Self.quiesceServiceIfPresent() }.value
                approvalGate.quiescenceSucceeded()
                guard approvalGate.mayInvokeInstallHandler else {
                    reply(.skip)
                    return
                }
                UserDefaults.standard.set(true, forKey: "restartServiceAfterSparkleUpdate")
                onStatus?("Installing the approved update and restarting Scratchpad…")
                reply(.install)
            } catch {
                approvalGate.quiescenceFailed()
                onStatus?("Update installation was held because the local service could not be prepared safely.")
                onQuiescenceFailure?(error.localizedDescription)
                reply(.skip)
            }
        }
    }

    nonisolated private static func quiesceServiceIfPresent() throws {
        let client = ServiceClient(timeout: 120)
        guard FileManager.default.fileExists(atPath: client.socketPath) else { return }
        _ = try client.call("quiesce", body: EmptyBody(), as: EmptyResponse.self)
        let deadline = Date().addingTimeInterval(30)
        while FileManager.default.fileExists(atPath: client.socketPath) {
            guard Date() < deadline else { throw UpdatePreparationError.serviceDidNotExit }
            Thread.sleep(forTimeInterval: 0.1)
        }
    }
}

private enum UpdatePreparationError: Error, LocalizedError {
    case serviceDidNotExit
    var errorDescription: String? { "The local service did not finish its update handoff." }
}
