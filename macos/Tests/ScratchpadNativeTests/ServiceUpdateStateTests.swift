import Foundation
import Testing
@testable import ScratchpadNative

private func status(_ fields: String) throws -> ServiceStatus {
    let json = """
    {"lifecycle":"running","pending":0,"blocked":0,"rejected":0,"needsSelection":0,"keyAvailable":true,"storageAvailable":true,"queueSchemaVersion":1\(fields)}
    """
    return try JSONDecoder().decode(ServiceStatus.self, from: Data(json.utf8))
}

@Test func decodesVersionFieldsWhenPresent() throws {
    let value = try status(#","serviceVersion":"2026.10.2","serviceBuild":"11","serverVersion":"2026.10.31","serverCommit":"abc1234""#)
    #expect(value.serviceVersion == "2026.10.2")
    #expect(value.serviceBuild == "11")
    #expect(value.serverVersion == "2026.10.31")
    #expect(value.serverCommit == "abc1234")
}

@Test func olderServicesDecodeWithoutVersions() throws {
    let value = try status("")
    #expect(value.serviceVersion == nil)
    #expect(ServiceUpdateState(status: value, appVersion: "2026.10.2", appBuild: "11") == .unknown)
}

@Test func matchingServiceIsCurrent() throws {
    let value = try status(#","serviceVersion":"2026.10.2","serviceBuild":"11""#)
    #expect(ServiceUpdateState(status: value, appVersion: "2026.10.2", appBuild: "11") == .current)
}

@Test func olderServiceNeedsRestart() throws {
    let value = try status(#","serviceVersion":"2026.10.1","serviceBuild":"10""#)
    #expect(ServiceUpdateState(status: value, appVersion: "2026.10.2", appBuild: "11") == .restartNeeded(running: "2026.10.1 (10)"))
}

@Test func sameVersionDifferentBuildNeedsRestart() throws {
    let value = try status(#","serviceVersion":"2026.10.2","serviceBuild":"10""#)
    #expect(ServiceUpdateState(status: value, appVersion: "2026.10.2", appBuild: "11") == .restartNeeded(running: "2026.10.2 (10)"))
}

@Test func developmentBuildsAreNeverFlagged() throws {
    let value = try status(#","serviceVersion":"dev","serviceBuild":"dev""#)
    #expect(ServiceUpdateState(status: value, appVersion: "2026.10.2", appBuild: "11") == .current)
}
