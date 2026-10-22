import Foundation
import Testing
@testable import ScratchpadNative

private let syntheticKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

private final class DiscoveryProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let host = request.url!.host!
        let status: Int
        let body: String
        switch host {
        case "accepted.test":
            status = 200
            body = #"{"version":2,"recipient":"https://accepted.test","purpose":"ssh_login","namespace":"scratchpad-auth-v2"}"#
        case "denied.test":
            status = 401
            body = #"{"error":{"code":"AUTH_INVALID"}}"#
        case "limited.test":
            status = 429
            body = #"{"error":{"code":"RATE_LIMITED"}}"#
        default:
            status = 200
            body = #"{"version":2,"recipient":"https://other.test","purpose":"ssh_login","namespace":"scratchpad-auth-v2"}"#
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@Test func discoveryRejectsRemoteHTTPAndNonOriginServerURLs() throws {
    for value in ["http://memory.example.com", "https://example.com/path", "https://user:secret@example.com", "https://example.com?secret=1", "https://example.com/#fragment", "file:///tmp/server"] {
        #expect(throws: SSHKeyDiscoveryError.self) { try SSHKeyDiscovery.serverOrigin(value) }
    }
    #expect(try SSHKeyDiscovery.serverOrigin(" https://memory.example.test/ ").absoluteString == "https://memory.example.test")
    #expect(try SSHKeyDiscovery.serverOrigin("http://127.0.0.1:3000").port == 3000)
    #expect(try SSHKeyDiscovery.serverOrigin("https://MEMORY.example.test:443/").absoluteString == "https://memory.example.test")
    #expect(try SSHKeyDiscovery.serverOrigin("http://LOCALHOST:80/").absoluteString == "http://localhost")
}

@Test func discoveryOnlyReadsPublicFilesAndDeduplicatesKeys() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("scratchpad-discovery-\(UUID().uuidString)")
    let ssh = root.appendingPathComponent(".ssh")
    try FileManager.default.createDirectory(at: ssh, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    try Data((syntheticKey + " original-comment\n").utf8).write(to: ssh.appendingPathComponent("first.pub"))
    try Data((syntheticKey + " different-comment\n").utf8).write(to: ssh.appendingPathComponent("second.pub"))
    try Data("PRIVATE KEY CONTENT MUST NOT BE DISCOVERED".utf8).write(to: ssh.appendingPathComponent("id_ed25519"))
    try Data("invalid public key".utf8).write(to: ssh.appendingPathComponent("invalid.pub"))
    try FileManager.default.createSymbolicLink(at: ssh.appendingPathComponent("symlink.pub"), withDestinationURL: ssh.appendingPathComponent("id_ed25519"))
    let keys = try SSHKeyDiscovery.localCandidates(home: root, environment: [:])
    #expect(keys.count == 1)
    #expect(keys.first?.publicKey == syntheticKey)
    #expect(keys.first?.label == "first.pub (load in SSH agent)")
    #expect(keys.first?.fingerprint.hasPrefix("SHA256:") == true)
}

@Test func agentKeyConfigurationPersistsOnlyPublicMaterialAndSocket() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("scratchpad-public-config-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: root) }
    let key = try #require(SSHKeyCandidate.parse(syntheticKey, label: "Agent", agentSocket: "/tmp/synthetic-agent.sock"))
    let config = try key.configuration(serverURL: "https://memory.example.test/", directory: root)
    #expect(config.serverURL == "https://memory.example.test")
    #expect(config.sshAgentSocket == "/tmp/synthetic-agent.sock")
    #expect(config.signingKeyPath == config.publicKeyPath)
    #expect(try String(contentsOfFile: config.publicKeyPath, encoding: .utf8) == syntheticKey + "\n")
    let permissions = try FileManager.default.attributesOfItem(atPath: config.publicKeyPath)[.posixPermissions] as? NSNumber
    #expect(permissions?.intValue == 0o600)
    let old = Data(#"{"serverURL":"https://memory.test","credentialFingerprint":"SHA256:old","publicKeyPath":"/tmp/old.pub","signingKeyPath":"/tmp/old"}"#.utf8)
    #expect(try JSONDecoder().decode(ServiceConfiguration.self, from: old).sshAgentSocket.isEmpty)
    #expect(ServiceClient.timeout(for: "verify_connection") > 90)
}

@Test func discoveryUsesServerEligibilityWithoutTreatingItAsAuthentication() async throws {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [DiscoveryProtocol.self]
    let session = URLSession(configuration: configuration)
    defer { session.invalidateAndCancel() }
    let key = try #require(SSHKeyCandidate.parse(syntheticKey, label: "Synthetic"))
    #expect(try await SSHKeyDiscovery.eligibleCandidates([key], serverURL: "https://accepted.test", session: session) == [key])
    #expect(try await SSHKeyDiscovery.eligibleCandidates([key], serverURL: "https://denied.test", session: session).isEmpty)
    await #expect(throws: SSHKeyDiscoveryError.self) {
        try await SSHKeyDiscovery.eligibleCandidates([key], serverURL: "https://wrong-origin.test", session: session)
    }
    await #expect(throws: SSHKeyDiscoveryError.self) {
        try await SSHKeyDiscovery.eligibleCandidates([key], serverURL: "https://limited.test", session: session)
    }
}
