import Testing
@testable import ScratchpadNative

@Test func acceptsInstalledSchemaInDeclaredRange() {
    let properties: [AnyHashable: Any] = [
        UpdateCompatibility.minimumKey: "1",
        UpdateCompatibility.maximumKey: 3,
    ]
    #expect(UpdateCompatibility.isCompatible(properties: properties, installedSchema: 1))
    #expect(UpdateCompatibility.isCompatible(properties: properties, installedSchema: 2))
    #expect(UpdateCompatibility.isCompatible(properties: properties, installedSchema: 3))
}

@Test func rejectsMissingMalformedOrIncompatibleRange() {
    #expect(UpdateCompatibility.isCompatible(properties: [:], installedSchema: 1) == false)
    #expect(UpdateCompatibility.isCompatible(properties: [UpdateCompatibility.minimumKey: "1"], installedSchema: 1) == false)
    #expect(UpdateCompatibility.isCompatible(properties: [
        UpdateCompatibility.minimumKey: 4,
        UpdateCompatibility.maximumKey: 2,
    ], installedSchema: 3) == false)
    #expect(UpdateCompatibility.isCompatible(properties: [
        UpdateCompatibility.minimumKey: "next",
        UpdateCompatibility.maximumKey: "5",
    ], installedSchema: 4) == false)
    #expect(UpdateCompatibility.isCompatible(properties: [
        UpdateCompatibility.minimumKey: 2,
        UpdateCompatibility.maximumKey: 4,
    ], installedSchema: 1) == false)
}

@Test func updateStagePolicyDownloadsBeforeSeekingInstallApproval() {
    #expect(UpdateStagePolicy.action(sparkleStageRawValue: 0) == .downloadAutomatically)
    #expect(UpdateStagePolicy.action(sparkleStageRawValue: 1) == .requestInstallApproval)
    #expect(UpdateStagePolicy.action(sparkleStageRawValue: 2) == .requireInstallOrCancel)
    #expect(UpdateStagePolicy.action(sparkleStageRawValue: 99) == .requireInstallOrCancel)
}
