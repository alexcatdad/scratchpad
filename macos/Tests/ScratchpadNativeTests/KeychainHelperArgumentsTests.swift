import Testing
@testable import ScratchpadNative

@Test func acceptsOnlyRatifiedQueueKeyOperationsAndIdentifier() {
    let read = KeychainHelperArguments(arguments: ["get", QueueKeyIdentifier.captureQueue])
    #expect(read?.operation == .get)
    #expect(read?.identifier == QueueKeyIdentifier.captureQueue)

    let create = KeychainHelperArguments(arguments: ["create", QueueKeyIdentifier.captureQueue])
    #expect(create?.operation == .create)
    #expect(create?.identifier == QueueKeyIdentifier.captureQueue)
}

@Test func rejectsMalformedOperationsArityAndOtherIdentifiers() {
    #expect(KeychainHelperArguments(arguments: []) == nil)
    #expect(KeychainHelperArguments(arguments: ["get"]) == nil)
    #expect(KeychainHelperArguments(arguments: ["delete", QueueKeyIdentifier.captureQueue]) == nil)
    #expect(KeychainHelperArguments(arguments: ["get", "other-key"]) == nil)
    #expect(KeychainHelperArguments(arguments: ["get", QueueKeyIdentifier.captureQueue, "extra"]) == nil)
}

@Test func duplicateCreateLoadsExistingKeyInsteadOfReplacingIt() {
    #expect(KeychainHelperArguments.createFailureDisposition(isDuplicateItem: true) == .loadExisting)
    #expect(KeychainHelperArguments.createFailureDisposition(isDuplicateItem: false) == .fail)
}
