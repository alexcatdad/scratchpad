import Testing
@testable import ScratchpadNative

@Test func lifecycleIntentRejectsOlderSuccessAndFailureObservations() {
    var policy = ServiceLifecycleObservationRevision(serviceExpectedToRun: true)
    let preStopPoll = policy.value
    let stop = policy.beginLifecycleIntent(operation: "stop")

    #expect(policy.absenceSuppressed)
    #expect(policy.serviceExpectedToRun)
    #expect(!policy.accepts(preStopPoll))
    #expect(!policy.canRefresh)
    #expect(policy.acceptsLifecycleCompletion(stop))
    let didStop = policy.finishLifecycle(stop, operation: "stop", succeeded: true)
    #expect(didStop)
    #expect(!policy.serviceExpectedToRun)
    #expect(policy.accepts(stop))

    let start = policy.beginStartIntent()
    #expect(policy.serviceExpectedToRun)
    #expect(!policy.absenceSuppressed)
    #expect(!policy.accepts(stop))
    #expect(policy.acceptsStartStatus(start))
    #expect(!policy.accepts(start))
    let didStart = policy.finishStart(start)
    #expect(didStart)
    #expect(policy.accepts(start))
}

@Test func newerStartAndUpdaterTransitionsInvalidatePendingOperations() {
    var policy = ServiceLifecycleObservationRevision()
    let stop = policy.beginLifecycleIntent(operation: "stop")
    let start = policy.beginStartIntent()

    #expect(!policy.acceptsLifecycleCompletion(stop))
    #expect(policy.acceptsStartStatus(start))

    let updater = policy.beginUpdaterQuiescence()
    #expect(!policy.acceptsStartStatus(start))
    #expect(policy.accepts(updater))
    #expect(policy.canRefresh)
}

@Test func failedStopRestoresUpdaterSuppressionAndFailedStartKeepsRunIntent() {
    var policy = ServiceLifecycleObservationRevision(serviceExpectedToRun: true, absenceSuppressed: true)
    let stop = policy.beginLifecycleIntent(operation: "stop")
    let didFailStop = policy.finishLifecycle(stop, operation: "stop", succeeded: false)
    #expect(didFailStop)
    #expect(policy.serviceExpectedToRun)
    #expect(policy.absenceSuppressed)

    let start = policy.beginStartIntent()
    let didFailStart = policy.finishStartFailure(start)
    #expect(didFailStart)
    #expect(policy.serviceExpectedToRun)
    #expect(!policy.absenceSuppressed)
}

@Test func updaterRestoreFailureCanReleaseSuppressionWithoutChangingStopIntent() {
    var policy = ServiceLifecycleObservationRevision(serviceExpectedToRun: true)
    var tracker = IncidentTracker()
    let stalePoll = policy.value

    _ = policy.beginUpdaterQuiescence()
    #expect(policy.absenceSuppressed)
    #expect(tracker.observe(nil, serviceExpected: policy.serviceExpectedToRun, absenceSuppressed: policy.absenceSuppressed).isEmpty)

    // Terminal recovery failure releases temporary suppression; the last
    // confirmed running intent now produces the local-save incident.
    let recovery = policy.finishUpdaterRecovery()
    #expect(!policy.accepts(stalePoll))
    #expect(policy.accepts(recovery))
    #expect(policy.serviceExpectedToRun)
    #expect(!policy.absenceSuppressed)
    #expect(tracker.observe(nil, serviceExpected: policy.serviceExpectedToRun, absenceSuppressed: policy.absenceSuppressed) == [.localSavingUnavailable])

    // A previously confirmed deliberate Stop remains stopped after recovery.
    var stopped = ServiceLifecycleObservationRevision(serviceExpectedToRun: true)
    let stop = stopped.beginLifecycleIntent(operation: "stop")
    let didStop = stopped.finishLifecycle(stop, operation: "stop", succeeded: true)
    #expect(didStop)
    _ = stopped.beginUpdaterQuiescence()
    let stoppedRecovery = stopped.finishUpdaterRecovery()
    #expect(!stopped.serviceExpectedToRun)
    #expect(!stopped.absenceSuppressed)
    #expect(stopped.accepts(stoppedRecovery))
    var stoppedTracker = IncidentTracker()
    #expect(stoppedTracker.observe(nil, serviceExpected: stopped.serviceExpectedToRun, absenceSuppressed: stopped.absenceSuppressed).isEmpty)
}
