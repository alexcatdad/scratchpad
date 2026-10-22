import CryptoKit
import Foundation
import Darwin

public struct SSHKeyCandidate: Identifiable, Equatable, Sendable {
    public var id: String { fingerprint }
    public let publicKey: String
    public let fingerprint: String
    public let label: String
    public let agentSocket: String
    public let publicKeyPath: String?

    public static func parse(_ line: String, label: String, agentSocket: String = "", publicKeyPath: String? = nil) -> SSHKeyCandidate? {
        let fields = line.split(whereSeparator: \.isWhitespace)
        guard fields.count >= 2,
              ["ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com"].contains(String(fields[0])),
              let blob = Data(base64Encoded: String(fields[1])), !blob.isEmpty, blob.count <= 16_384
        else { return nil }
        let fingerprint = "SHA256:" + Data(SHA256.hash(data: blob)).base64EncodedString().replacingOccurrences(of: "=", with: "")
        return SSHKeyCandidate(publicKey: "\(fields[0]) \(fields[1])", fingerprint: fingerprint, label: label, agentSocket: agentSocket, publicKeyPath: publicKeyPath)
    }

    /// Persist only a public key for agent-backed signing. No private key is opened.
    public func configuration(serverURL: String, directory: URL) throws -> ServiceConfiguration {
        let origin = try SSHKeyDiscovery.serverOrigin(serverURL)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let path = directory.appendingPathComponent(Data(SHA256.hash(data: Data(publicKey.utf8))).map { String(format: "%02x", $0) }.joined() + ".pub")
        try Data((publicKey + "\n").utf8).write(to: path, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
        return ServiceConfiguration(serverURL: origin.absoluteString, credentialFingerprint: fingerprint, publicKeyPath: path.path, signingKeyPath: path.path, sshAgentSocket: agentSocket)
    }
}

private final class NoSSHDiscoveryRedirects: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

public enum SSHKeyDiscovery {
    public static let maximumCandidates = 16

    public static func serverOrigin(_ value: String) throws -> URL {
        guard var parts = URLComponents(string: value.trimmingCharacters(in: .whitespacesAndNewlines)),
              let host = parts.host, !host.isEmpty,
              parts.user == nil, parts.password == nil, parts.query == nil, parts.fragment == nil,
              parts.path.isEmpty || parts.path == "/",
              parts.scheme == "https" || (parts.scheme == "http" && ["localhost", "127.0.0.1", "[::1]", "::1"].contains(host.lowercased()))
        else { throw SSHKeyDiscoveryError.invalidServer }
        parts.scheme = parts.scheme?.lowercased()
        parts.host = host.lowercased()
        if (parts.scheme == "https" && parts.port == 443) || (parts.scheme == "http" && parts.port == 80) { parts.port = nil }
        parts.path = ""
        guard let result = parts.url else { throw SSHKeyDiscoveryError.invalidServer }
        return result
    }

    /// Read public identities only. Candidate order prefers the selected agent,
    /// then the inherited agent and 1Password, then ordinary public-key files.
    public static func localCandidates(home: URL = FileManager.default.homeDirectoryForCurrentUser, preferredAgentSocket: String = "", environment: [String: String] = ProcessInfo.processInfo.environment) throws -> [SSHKeyCandidate] {
        var candidates: [SSHKeyCandidate] = []
        var sockets: [String] = []
        for socket in [preferredAgentSocket, environment["SSH_AUTH_SOCK"] ?? "", home.appendingPathComponent("Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock").path] {
            if !socket.isEmpty && !sockets.contains(socket) { sockets.append(socket) }
        }
        for socket in sockets {
            guard FileManager.default.fileExists(atPath: socket),
                  let output = try? publicAgentKeys(socket: socket, environment: environment)
            else { continue }
            for line in output.split(separator: "\n") {
                if let key = SSHKeyCandidate.parse(String(line), label: "SSH agent", agentSocket: socket) { candidates.append(key) }
            }
        }
        let directory = home.appendingPathComponent(".ssh", isDirectory: true)
        let files = (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: [.isRegularFileKey, .isSymbolicLinkKey, .fileSizeKey])) ?? []
        for file in files.sorted(by: { $0.lastPathComponent < $1.lastPathComponent }).prefix(128) where file.pathExtension == "pub" {
            guard let values = try? file.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey, .fileSizeKey]),
                  values.isRegularFile == true, values.isSymbolicLink != true, (values.fileSize ?? Int.max) <= 32_768,
                  let contents = try? String(contentsOf: file, encoding: .utf8),
                  let key = SSHKeyCandidate.parse(contents, label: "\(file.lastPathComponent) (load in SSH agent)", agentSocket: sockets.first ?? "", publicKeyPath: file.path)
            else { continue }
            candidates.append(key)
        }
        var fingerprints: Set<String> = []
        return Array(candidates.filter { fingerprints.insert($0.fingerprint).inserted }.prefix(maximumCandidates))
    }

    /// Eligibility is a hint from the existing public-key challenge route.
    /// Authentication still requires a fresh recipient-bound signature in Go.
    public static func eligibleCandidates(_ candidates: [SSHKeyCandidate], serverURL: String, session suppliedSession: URLSession? = nil) async throws -> [SSHKeyCandidate] {
        let origin = try serverOrigin(serverURL)
        let session = suppliedSession ?? URLSession(configuration: .ephemeral, delegate: NoSSHDiscoveryRedirects(), delegateQueue: nil)
        defer { if suppliedSession == nil { session.invalidateAndCancel() } }
        var accepted: [SSHKeyCandidate] = []
        for candidate in candidates.prefix(maximumCandidates) {
            try Task.checkCancellation()
            var request = URLRequest(url: origin.appendingPathComponent("api/v1/auth/mcp/challenge"))
            request.httpMethod = "POST"
            request.timeoutInterval = 5
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(["publicKey": candidate.publicKey])
            let (data, response) = try await session.data(for: request)
            guard let response = response as? HTTPURLResponse, data.count <= 65_536 else { throw SSHKeyDiscoveryError.serverUnavailable }
            if response.statusCode == 200 {
                struct Challenge: Decodable { let version: Int; let recipient: String; let purpose: String; let namespace: String }
                guard let challenge = try? JSONDecoder().decode(Challenge.self, from: data), challenge.version == 2,
                      challenge.recipient == origin.absoluteString, challenge.purpose == "ssh_login", challenge.namespace == "scratchpad-auth-v2"
                else { throw SSHKeyDiscoveryError.serverUnavailable }
                accepted.append(candidate)
            } else {
                struct Failure: Decodable { struct Detail: Decodable { let code: String }; let error: Detail }
                let code = (try? JSONDecoder().decode(Failure.self, from: data))?.error.code
                if response.statusCode == 401 && code == "AUTH_INVALID" { continue }
                if response.statusCode == 429 { throw SSHKeyDiscoveryError.rateLimited }
                throw SSHKeyDiscoveryError.serverUnavailable
            }
        }
        return accepted
    }

    private static func publicAgentKeys(socket: String, environment: [String: String]) throws -> String {
        let outputURL = FileManager.default.temporaryDirectory.appendingPathComponent("scratchpad-public-keys-\(UUID().uuidString)")
        guard FileManager.default.createFile(atPath: outputURL.path, contents: nil, attributes: [.posixPermissions: 0o600]) else { throw SSHKeyDiscoveryError.agentUnavailable }
        defer { try? FileManager.default.removeItem(at: outputURL) }
        let output = try FileHandle(forWritingTo: outputURL)
        defer { try? output.close() }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/ssh-add")
        process.arguments = ["-L"]
        var environment = environment
        environment["SSH_AUTH_SOCK"] = socket
        process.environment = environment
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        let finished = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in finished.signal() }
        try process.run()
        if finished.wait(timeout: .now() + 5) == .timedOut {
            process.terminate()
            if finished.wait(timeout: .now() + 1) == .timedOut { kill(process.processIdentifier, SIGKILL); process.waitUntilExit() }
            throw SSHKeyDiscoveryError.agentUnavailable
        }
        guard process.terminationStatus == 0,
              let size = try outputURL.resourceValues(forKeys: [.fileSizeKey]).fileSize, size <= 262_144
        else { throw SSHKeyDiscoveryError.agentUnavailable }
        return try String(contentsOf: outputURL, encoding: .utf8)
    }
}

public enum SSHKeyDiscoveryError: Error, LocalizedError {
    case invalidServer, agentUnavailable, serverUnavailable, rateLimited
    public var errorDescription: String? {
        switch self {
        case .invalidServer: "Enter the server's HTTPS origin without a path. HTTP is supported only on localhost."
        case .agentUnavailable: "The SSH agent is unavailable. Unlock it and retry."
        case .serverUnavailable: "Could not check keys with this Scratchpad server. Check the URL, network access and server version."
        case .rateLimited: "The server is limiting authentication attempts. Wait a minute before checking keys again."
        }
    }
}
