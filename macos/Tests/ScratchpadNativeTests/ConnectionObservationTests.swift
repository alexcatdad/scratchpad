import Foundation
import Testing
@testable import ScratchpadNative

@Test func staleOrUnparseableConnectionObservationsAreUnknown() {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    let formatter = ISO8601DateFormatter()
    let stale = ConnectionObservation(state: "connected", checkedAt: formatter.string(from: now.addingTimeInterval(-61)))
    let malformed = ConnectionObservation(state: "connected", checkedAt: "not-a-date")
    let missing = ConnectionObservation(state: "connected", checkedAt: nil)
    let current = ConnectionObservation(state: "connected", checkedAt: formatter.string(from: now.addingTimeInterval(-30)))

    #expect(stale.effectiveState(at: now) == "unknown")
    #expect(malformed.effectiveState(at: now) == "unknown")
    #expect(missing.effectiveState(at: now) == "unknown")
    #expect(current.effectiveState(at: now) == "connected")
}

@Test func localCaptureReadinessDoesNotTreatUnknownAsUnavailableOrRemoteStateAsLocalReadiness() {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    #expect(LocalCaptureReadiness(status: nil, observedAt: now, now: now) == .unconfirmed)

    let status = ServiceStatus(
        lifecycle: "running", pending: 0, blocked: 0, rejected: 0, needsSelection: 0,
        keyAvailable: true, storageAvailable: true, lastDelivery: nil,
        connection: ConnectionObservation(state: "connectivity", checkedAt: "2026-10-15T10:00:00Z"),
        queueSchemaVersion: 1
    )
    #expect(LocalCaptureReadiness(status: status, observedAt: now, now: now) == .ready)
    #expect(LocalCaptureReadiness(status: status, observedAt: now.addingTimeInterval(-31), now: now) == .unconfirmed)
}

@Test func localCaptureReadinessRespectsExplicitStopPauseAndLocalFailures() {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    func readiness(_ lifecycle: String, key: Bool = true, storage: Bool? = true) -> LocalCaptureReadiness {
        let status = makeStatus(lifecycle, key: key, storage: storage)
        return LocalCaptureReadiness(status: status, observedAt: now, now: now)
    }
    func makeStatus(_ lifecycle: String, key: Bool = true, storage: Bool? = true) -> ServiceStatus {
        ServiceStatus(
            lifecycle: lifecycle, pending: 0, blocked: 0, rejected: 0, needsSelection: 0,
            keyAvailable: key, storageAvailable: storage, lastDelivery: nil,
            connection: nil, queueSchemaVersion: 1
        )
    }

    #expect(readiness("stopped") == .stopped)
    #expect(readiness("paused") == .readyWithDeliveryPaused)
    #expect(readiness("running", key: false) == .queueKeyUnavailable)
    #expect(readiness("running", storage: false) == .storageUnavailable)
    #expect(readiness("running", storage: nil) == .unconfirmed)
}
