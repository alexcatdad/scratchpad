import Foundation
import Testing
@testable import ScratchpadNative

private let now = Date(timeIntervalSince1970: 1_800_000_000)

private func status(lifecycle: String = "running", pending: Int = 0, blocked: Int = 0, needsSelection: Int = 0,
                    keyAvailable: Bool = true, storageAvailable: Bool? = true, connection: String? = "connected") -> ServiceStatus {
    let checkedAt = ISO8601DateFormatter().string(from: now.addingTimeInterval(-5))
    return ServiceStatus(
        lifecycle: lifecycle, pending: pending, blocked: blocked, rejected: 0, needsSelection: needsSelection,
        keyAvailable: keyAvailable, storageAvailable: storageAvailable, lastDelivery: nil,
        connection: connection.map { ConnectionObservation(state: $0, checkedAt: checkedAt) }, queueSchemaVersion: 1
    )
}

private func present(_ value: ServiceStatus?, update: ServiceUpdateState = .current) -> StatusPresentation {
    StatusPresentation(status: value, update: update, now: now)
}

@Test func healthyServiceIsConnected() {
    let p = present(status())
    #expect(p.tone == .ok)
    #expect(p.word == "Connected")
    #expect(p.action == nil)
    #expect(p.rows.map(\.value) == ["Running", "Connected", "None"])
}

@Test func missingServiceOffersStart() {
    let p = present(nil)
    #expect(p.tone == .off)
    #expect(p.action == .startService)
}

@Test func stoppedServiceOffersStart() {
    #expect(present(status(lifecycle: "stopped")).action == .startService)
}

@Test func offlineServerKeepsSavingAndOffersRetry() {
    let p = present(status(pending: 2, connection: "connectivity"))
    #expect(p.tone == .warning)
    #expect(p.word == "Unreachable")
    #expect(p.detail.contains("2 captures"))
    #expect(p.action == .checkAgain)
}

@Test func rejectedCredentialAsksToReconnect() {
    let p = present(status(connection: "credential_changed"))
    #expect(p.tone == .error)
    #expect(p.word == "Sign-in needed")
    #expect(p.action == .reconnect)
}

@Test func outdatedServiceOffersRestartBeforeConnectionDetail() {
    let p = present(status(connection: "connectivity"), update: .restartNeeded(running: "2026.10.1 (10)"))
    #expect(p.tone == .update)
    #expect(p.action == .restartService)
    #expect(p.detail.contains("2026.10.1 (10)"))
}

@Test func localStorageFailureOutranksEverythingButStop() {
    let p = present(status(keyAvailable: false), update: .restartNeeded(running: "x"))
    #expect(p.tone == .error)
    #expect(p.word == "Cannot save")
}

@Test func capturesWithoutProjectNeedReview() {
    let p = present(status(needsSelection: 1))
    #expect(p.tone == .warning)
    #expect(p.headline == "1 capture needs you")
    #expect(p.action == .reviewCaptures)
}

@Test func staleConnectionIsShownAsChecking() {
    let old = ServiceStatus(lifecycle: "running", pending: 0, blocked: 0, rejected: 0, needsSelection: 0, keyAvailable: true,
                            storageAvailable: true, lastDelivery: nil,
                            connection: ConnectionObservation(state: "connected", checkedAt: "2020-01-01T00:00:00Z"), queueSchemaVersion: 1)
    #expect(present(old).word == "Checking")
}

@Test func everyToneHasADistinctSymbol() {
    let tones: [StatusPresentation.Tone] = [.ok, .warning, .error, .off, .update]
    #expect(Set(tones.map(\.symbol)).count == tones.count)
}
