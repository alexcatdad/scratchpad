import Foundation
import Testing
@testable import ScratchpadNative

private struct SmokeRecord: Encodable, Sendable {
    let type: String
    let title: String
    let authority: String
    let confidence: String
    let payload: JSONValue
}

private struct SmokeScope: Encodable, Sendable {
    let workingDirectory: String
    let projectId: String
}

private struct SmokeBinding: Encodable, Sendable {
    let serverURL: String
    let credentialFingerprint: String
    let publicKeyPath: String
    let signingKeyPath: String
}

private struct SmokeCaptureRequest: Encodable, Sendable {
    let requestId: String
    let record: SmokeRecord
    let scope: SmokeScope
    let gitContext: JSONValue
    let clientSavedAt: String
    let binding: SmokeBinding
}

private struct SmokeCaptureOutcome: Decodable, Sendable {
    let outcome: String
    let receipt: Receipt
}

private struct SmokeEmptyResponse: Decodable, Sendable {}

@Test func swiftClientSpeaksToIsolatedGoServiceProcess() throws {
    guard let servicePath = ProcessInfo.processInfo.environment["SCRATCHPAD_TEST_SERVICE"],
          FileManager.default.isExecutableFile(atPath: servicePath)
    else { return }

    let root = URL(fileURLWithPath: "/private/tmp", isDirectory: true)
        .appendingPathComponent("scratchpad-native-smoke-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: root.path)

    let socketPath = root.appendingPathComponent("service.sock").path
    let databasePath = root.appendingPathComponent("captures.sqlite").path
    let helperPath = root.appendingPathComponent("synthetic-key-helper").path
    let helper = """
    #!/bin/sh
    [ "$#" -eq 2 ] || exit 1
    [ "$2" = "\(QueueKeyIdentifier.captureQueue)" ] || exit 1
    case "$1" in get|create) printf '0123456789abcdef0123456789abcdef' ;; *) exit 1 ;; esac
    """
    try Data(helper.utf8).write(to: URL(fileURLWithPath: helperPath), options: .atomic)
    try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: helperPath)

    let process = Process()
    process.executableURL = URL(fileURLWithPath: servicePath)
    process.arguments = ["--socket", socketPath, "--database", databasePath, "--key-helper", helperPath, "--explicit-start"]
    var environment = ProcessInfo.processInfo.environment
    environment["HOME"] = root.path
    environment["XDG_CONFIG_HOME"] = root.path
    process.environment = environment
    process.standardInput = FileHandle.nullDevice
    process.standardOutput = FileHandle.nullDevice
    process.standardError = FileHandle.nullDevice
    try process.run()
    defer { if process.isRunning { process.terminate(); process.waitUntilExit() } }

    let client = ServiceClient(socketPath: socketPath, timeout: 2)
    var status: ServiceStatus?
    for _ in 0..<60 {
        status = try? client.call("status", body: EmptyBody(), as: ServiceStatus.self)
        if status != nil { break }
        Thread.sleep(forTimeInterval: 0.05)
    }
    #expect(status?.lifecycle == "running")
    #expect(status?.keyAvailable == true)
    #expect(status?.storageAvailable == true)
    #expect(status?.queueSchemaVersion == 1)

    let initialConfiguration = try client.call("get_configuration", body: EmptyBody(), as: ConfigurationResult.self)
    #expect(initialConfiguration.configuration.serverURL.isEmpty)
    let configuration = ServiceConfiguration(
        serverURL: "http://127.0.0.1:1",
        credentialFingerprint: "SHA256:synthetic",
        publicKeyPath: "/private/tmp/scratchpad-synthetic.pub",
        signingKeyPath: "/private/tmp/scratchpad-synthetic-key"
    )
    try client.saveConfiguration(configuration)
    let savedConfiguration = try client.call("get_configuration", body: EmptyBody(), as: ConfigurationResult.self)
    #expect(savedConfiguration.configuration == configuration)

    var changedConfiguration = configuration
    changedConfiguration.serverURL = "http://127.0.0.1:2"
    try client.saveConfiguration(changedConfiguration)
    let changedResult = try client.call("get_configuration", body: EmptyBody(), as: ConfigurationResult.self)
    #expect(changedResult.configuration == changedConfiguration)
    try client.saveConfiguration(configuration)

    // The desktop's connect action sends proposed settings through the real
    // private socket. Failed possession must not replace a working binding.
    var proposed = configuration
    proposed.sshAgentSocket = root.appendingPathComponent("unavailable-agent.sock").path
    #expect(throws: (any Error).self) {
        try client.call("verify_connection", body: ConfigurationBody(configuration: proposed), as: VerifyResult.self)
    }
    let retained = try client.call("get_configuration", body: EmptyBody(), as: ConfigurationResult.self)
    #expect(retained.configuration == configuration)

    _ = try client.call("pause_delivery", body: EmptyBody(), as: SmokeEmptyResponse.self)
    status = try client.call("status", body: EmptyBody(), as: ServiceStatus.self)
    #expect(status?.lifecycle == "paused")

    let request = SmokeCaptureRequest(
        requestId: UUID().uuidString,
        record: SmokeRecord(type: "finding", title: "Synthetic service seam", authority: "observed", confidence: "high", payload: .object(["value": .number(Decimal(17)), "synthetic": .bool(true)])),
        scope: SmokeScope(workingDirectory: "/private/tmp/scratchpad-fixture", projectId: "proj_synthetic"),
        gitContext: .object(["repositoryIdentity": .string("example.invalid/synthetic")]),
        clientSavedAt: ISO8601DateFormatter().string(from: Date()),
        binding: SmokeBinding(serverURL: configuration.serverURL, credentialFingerprint: configuration.credentialFingerprint, publicKeyPath: configuration.publicKeyPath, signingKeyPath: configuration.signingKeyPath)
    )
    let saved = try client.call("capture", body: request, as: SmokeCaptureOutcome.self)
    #expect(saved.outcome == "saved_pending")
    #expect(saved.receipt.state == "pending")

    let list = try client.call("list_receipts", body: ListReceiptsBody(limit: 20, cursor: nil, state: nil), as: ReceiptList.self)
    #expect(list.receipts.contains(where: { $0.receiptId == saved.receipt.receiptId }))
    let detail = try client.call("lookup_receipt", body: ReceiptBody(receiptId: saved.receipt.receiptId), as: Receipt.self)
    #expect(detail.content?.record.title == "Synthetic service seam")
    #expect(detail.content?.record.payload.prettyPrinted()?.contains("17") == true)
    #expect(detail.content?.scope.projectId == "proj_synthetic")
    #expect(detail.content?.gitContext == .object(["repositoryIdentity": .string("example.invalid/synthetic")]))

    _ = try client.call("quiesce", body: EmptyBody(), as: SmokeEmptyResponse.self)
    let deadline = Date().addingTimeInterval(5)
    while process.isRunning && Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
    #expect(process.isRunning == false)
    process.waitUntilExit()
    #expect(FileManager.default.fileExists(atPath: socketPath) == false)
}
