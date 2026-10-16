import Foundation
import Testing
@testable import ScratchpadNative

private func serviceStatus(keyAvailable: Bool = true, storageAvailable: Bool? = true, lifecycle: String = "running", connection: ConnectionObservation? = nil) -> ServiceStatus {
    ServiceStatus(
        lifecycle: lifecycle,
        pending: 0,
        blocked: 0,
        rejected: 0,
        needsSelection: 0,
        keyAvailable: keyAvailable,
        storageAvailable: storageAvailable,
        lastDelivery: nil,
        connection: connection,
        queueSchemaVersion: 1
    )
}

private func iso8601(_ date: Date) -> String {
    ISO8601DateFormatter().string(from: date)
}

@Test func storageFailureWithAvailableKeyNotifiesOnceAndRecoversOnce() {
    var tracker = IncidentTracker()
    let storageFailure = serviceStatus(keyAvailable: true, storageAvailable: false)
    #expect(tracker.observe(storageFailure) == [.localSavingUnavailable])
    #expect(tracker.observe(storageFailure).isEmpty)

    #expect(tracker.observe(serviceStatus(keyAvailable: true, storageAvailable: true)) == [.recovered])
    #expect(tracker.observe(serviceStatus(keyAvailable: true, storageAvailable: true)).isEmpty)
}

@Test func missingStorageObservationRemainsBackwardCompatible() {
    var tracker = IncidentTracker()
    #expect(tracker.observe(serviceStatus(keyAvailable: true, storageAvailable: nil)).isEmpty)
}

@Test func expectedServiceDisappearanceNotifiesOnceAndRecoversOnce() {
    var tracker = IncidentTracker()
    #expect(tracker.observe(nil).isEmpty) // Initial absence is unknown.
    #expect(tracker.observe(serviceStatus()).isEmpty)
    #expect(tracker.observe(nil, serviceExpected: true) == [.localSavingUnavailable])
    #expect(tracker.observe(nil, serviceExpected: true).isEmpty)
    #expect(tracker.observe(serviceStatus()) == [.recovered])
    #expect(tracker.observe(serviceStatus()).isEmpty)
}

@Test func deliberateStopAndUpdaterQuiescenceDoNotReportLocalSaveFailure() {
    var tracker = IncidentTracker()
    #expect(tracker.observe(serviceStatus()).isEmpty)
    #expect(tracker.observe(nil, serviceExpected: true, absenceSuppressed: true).isEmpty)
    #expect(tracker.observe(serviceStatus(lifecycle: "stopped"), serviceExpected: true).isEmpty)
    #expect(tracker.observe(nil, serviceExpected: false).isEmpty)
}

@Test func definitiveStartupFailureNotifiesAndHealthyServiceRecovers() {
    var tracker = IncidentTracker()
    #expect(tracker.observe(nil, serviceExpected: true) == [.localSavingUnavailable])
    #expect(tracker.observe(nil, serviceExpected: true).isEmpty)
    #expect(tracker.observe(serviceStatus()) == [.recovered])
}

@Test func localSaveIncidentDeduplicationSurvivesTrayRelaunch() {
    var firstRun = IncidentTracker()
    #expect(firstRun.observe(nil, serviceExpected: true) == [.localSavingUnavailable])

    var relaunched = IncidentTracker(localSaveIncidentAlreadyNotified: firstRun.isLocalSaveIncidentActive)
    #expect(relaunched.observe(nil, serviceExpected: true) == [])
    #expect(relaunched.observe(serviceStatus()) == [.recovered])
    #expect(!relaunched.isLocalSaveIncidentActive)
}

@Test func stoppingAfterLocalSaveIncidentDoesNotClaimRecovery() {
    var tracker = IncidentTracker()
    #expect(tracker.observe(nil, serviceExpected: true) == [.localSavingUnavailable])
    #expect(tracker.observe(serviceStatus(lifecycle: "stopped"), absenceSuppressed: true).isEmpty)
    #expect(tracker.observe(serviceStatus(lifecycle: "stopped"), absenceSuppressed: true).isEmpty)
}

@Test func connectionIncidentsUseFreshServiceProbeTransitions() {
    var tracker = IncidentTracker()
    let start = Date(timeIntervalSince1970: 1_800_000_000)
    let staleFailure = ConnectionObservation(state: "connectivity", checkedAt: iso8601(start.addingTimeInterval(-120)))
    #expect(tracker.observe(serviceStatus(connection: staleFailure), at: start).isEmpty)

    let firstFailure = ConnectionObservation(state: "connectivity", checkedAt: iso8601(start))
    #expect(tracker.observe(serviceStatus(connection: firstFailure), at: start).isEmpty)
    let laterFailure = ConnectionObservation(state: "connectivity", checkedAt: iso8601(start.addingTimeInterval(30)))
    #expect(tracker.observe(serviceStatus(connection: laterFailure), at: start.addingTimeInterval(61)) == [.outage])

    let recovered = ConnectionObservation(state: "connected", checkedAt: iso8601(start.addingTimeInterval(90)))
    #expect(tracker.observe(serviceStatus(connection: recovered), at: start.addingTimeInterval(91)) == [.recovered])
}
