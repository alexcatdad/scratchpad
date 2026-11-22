import Darwin
import Foundation
import Testing

@testable import ScratchpadNative

/// A one-connection stand-in for the capture service socket: it reads one
/// framed request and answers with `respond(requestID, operation)`.
private final class FakeService: @unchecked Sendable {
    let path: String
    private let listener: Int32
    private let done = DispatchSemaphore(value: 0)

    init(respond: @escaping @Sendable (String, String) -> Data) throws {
        path = "/tmp/sp-\(UUID().uuidString.prefix(8)).sock"
        listener = socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8) + [0]
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: bytes) }
        let length = socklen_t(MemoryLayout<sockaddr_un>.size)
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(listener, $0, length) }
        }
        guard bound == 0, listen(listener, 1) == 0 else { throw LocalClientError.unavailable }
        let listener = listener
        let done = done
        Thread {
            defer { done.signal() }
            let connection = accept(listener, nil, nil)
            guard connection >= 0 else { return }
            defer { _ = close(connection) }
            guard let header = Self.read(connection, count: 4) else { return }
            let size = header.reduce(0) { ($0 << 8) | Int($1) }
            guard let body = Self.read(connection, count: size),
                let request = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
                let id = request["id"] as? String, let op = request["op"] as? String
            else { return }
            let reply = respond(id, op)
            var frame = UInt32(reply.count).bigEndian
            let out = withUnsafeBytes(of: &frame) { Data($0) } + reply
            _ = out.withUnsafeBytes { send(connection, $0.baseAddress, out.count, 0) }
        }.start()
    }

    private static func read(_ fd: Int32, count: Int) -> Data? {
        var data = Data(count: count)
        let received = data.withUnsafeMutableBytes { recv(fd, $0.baseAddress, count, MSG_WAITALL) }
        return received == count ? data : nil
    }

    func finish() {
        done.wait()
        _ = close(listener)
        unlink(path)
    }
}

private func reply(_ object: [String: Any]) -> Data {
    (try? JSONSerialization.data(withJSONObject: object)) ?? Data()
}

@Test func serviceClientDecodesAResult() throws {
    let service = try FakeService { id, op in
        reply([
            "version": 1, "id": id,
            "result": [
                "lifecycle": op == "status" ? "running" : "wrong", "pending": 2, "blocked": 0, "rejected": 0,
                "needsSelection": 1, "keyAvailable": true,
            ],
        ])
    }
    defer { service.finish() }
    let status = try ServiceClient(socketPath: service.path).call("status", body: EmptyBody(), as: ServiceStatus.self)
    #expect(status.lifecycle == "running")
    #expect(status.pending == 2)
    #expect(status.storageAvailable == nil)
}

@Test func serviceClientSavesConfiguration() throws {
    let service = try FakeService { id, op in
        reply(op == "set_configuration" ? ["version": 1, "id": id, "result": [:]] : ["version": 1, "id": id])
    }
    defer { service.finish() }
    try ServiceClient(socketPath: service.path).saveConfiguration(
        ServiceConfiguration(serverURL: "https://memory.test"))
}

@Test func serviceClientSurfacesServiceErrors() throws {
    let service = try FakeService { id, _ in
        reply(["version": 1, "id": id, "error": ["code": "CONFLICT", "message": "Already configured."]])
    }
    defer { service.finish() }
    #expect(throws: ServiceError.self) {
        try ServiceClient(socketPath: service.path).call("status", body: EmptyBody(), as: ServiceStatus.self)
    }
}

@Test func serviceClientRejectsAMismatchedResponse() throws {
    let service = try FakeService { _, _ in reply(["version": 1, "id": "someone-else", "result": ["projects": []]]) }
    defer { service.finish() }
    #expect(throws: LocalClientError.self) {
        try ServiceClient(socketPath: service.path).call("status", body: EmptyBody(), as: ProjectList.self)
    }
}

@Test func serviceClientReportsAMissingOrInvalidSocket() {
    #expect(throws: LocalClientError.self) {
        try ServiceClient(socketPath: "/tmp/sp-missing-\(UUID().uuidString.prefix(8)).sock")
            .call("status", body: EmptyBody(), as: ProjectList.self)
    }
    #expect(throws: LocalClientError.self) {
        try ServiceClient(socketPath: "/tmp/" + String(repeating: "x", count: 200))
            .call("status", body: EmptyBody(), as: ProjectList.self)
    }
    #expect(LocalClientError.unavailable.errorDescription == "Capture service is unavailable.")
    #expect(LocalClientError.frameTooLarge.errorDescription?.isEmpty == false)
    #expect(LocalClientError.invalidResponse.errorDescription?.isEmpty == false)
}

@Test func operationTimeoutsMatchTheServiceContract() {
    #expect(ServiceClient.timeout(for: "stop") == ServiceClient.stopTimeout)
    #expect(ServiceClient.timeout(for: "verify_connection") == 95)
    #expect(ServiceClient.timeout(for: "status") == ServiceClient.defaultTimeout)
}

@Test func configurationDecodesWithoutAnAgentSocket() throws {
    let data = Data(
        #"{"serverURL":"https://memory.test","credentialFingerprint":"SHA256:x","publicKeyPath":"/k.pub","signingKeyPath":"/k.pub"}"#
            .utf8)
    let configuration = try JSONDecoder().decode(ServiceConfiguration.self, from: data)
    #expect(configuration.sshAgentSocket.isEmpty)
    let encoded = try JSONEncoder().encode(configuration)
    #expect(try JSONDecoder().decode(ServiceConfiguration.self, from: encoded) == configuration)
}

@Test func jsonValueRoundTripsAndPrettyPrints() throws {
    let data = Data(#"{"a":[1,"two",true,null],"b":{"c":2.5}}"#.utf8)
    let value = try JSONDecoder().decode(JSONValue.self, from: data)
    let again = try JSONDecoder().decode(JSONValue.self, from: try JSONEncoder().encode(value))
    #expect(again == value)
    #expect(value.prettyPrinted()?.contains("\"two\"") == true)
}
