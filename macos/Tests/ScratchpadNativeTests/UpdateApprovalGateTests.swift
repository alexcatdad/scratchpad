import Testing
@testable import ScratchpadNative

@Test func installationRequiresOwnerApprovalAndSuccessfulQuiescence() {
    var gate = UpdateApprovalGate()
    gate.downloadedUpdateNeedsApproval()
    #expect(!gate.mayInvokeInstallHandler)
    gate.approve()
    #expect(!gate.mayInvokeInstallHandler)
    gate.beginQuiescence()
    #expect(!gate.mayInvokeInstallHandler)
    gate.quiescenceSucceeded()
    #expect(gate.mayInvokeInstallHandler)
}

@Test func declineAndQuiescenceFailureKeepInstallBlocked() {
    var declined = UpdateApprovalGate()
    declined.downloadedUpdateNeedsApproval()
    declined.decline()
    #expect(!declined.mayInvokeInstallHandler)

    var failed = UpdateApprovalGate()
    failed.downloadedUpdateNeedsApproval()
    failed.approve()
    failed.beginQuiescence()
    failed.quiescenceFailed()
    #expect(!failed.mayInvokeInstallHandler)
}
