import Testing
@testable import ScratchpadNative

@Test func updateRecoveryWaitsForCommittedExitBeforeReplacement() {
    #expect(ServiceRecoveryPolicy.action(socketExists: true, resumeAccepted: true, healthy: true) == .alreadyRestored)
    #expect(ServiceRecoveryPolicy.action(socketExists: true, resumeAccepted: false, healthy: false) == .waitForExit)
    #expect(ServiceRecoveryPolicy.action(socketExists: true, resumeAccepted: true, healthy: false) == .waitForExit)
    #expect(ServiceRecoveryPolicy.action(socketExists: false, resumeAccepted: false, healthy: false) == .launchRespectingPersistentState)
}
