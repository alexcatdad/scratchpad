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

@Test func newerStartInvalidatesPendingOperations() {
    var policy = ServiceLifecycleObservationRevision()
    let stop = policy.beginLifecycleIntent(operation: "stop")
    let start = policy.beginStartIntent()

    #expect(!policy.acceptsLifecycleCompletion(stop))
    #expect(policy.acceptsStartStatus(start))

}

@Test func failedStopRestoresSuppressionAndFailedStartKeepsRunIntent() {
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
