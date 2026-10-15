import Foundation
import Darwin

public struct ServiceError: Error, LocalizedError, Decodable, Sendable {
    public let code: String
    public let message: String
    public var errorDescription: String? { message }
}

private struct RequestEnvelope<Body: Encodable>: Encodable {
    let version = 1
    let id: String
    let op: String
    let body: Body
}

private struct ResponseEnvelope<Result: Decodable>: Decodable {
    let version: Int
    let id: String
    let result: Result?
    let error: ServiceError?
}

public struct EmptyBody: Encodable, Sendable { public init() {} }
public struct ServiceStatus: Decodable, Sendable {
    public let lifecycle: String
    public let pending: Int
    public let blocked: Int
    public let rejected: Int
    public let needsSelection: Int
    public let keyAvailable: Bool
    public let storageAvailable: Bool?
    public let lastDelivery: DeliverySummary?
    public let connection: ConnectionObservation?
    public let queueSchemaVersion: Int?
}
public struct ConnectionObservation: Decodable, Sendable {
    public let state: String
    public let checkedAt: String?

    /// Service connection states are cached between serialized probes. Treat
    /// old or unparseable observations as unknown so UI never shows stale green.
    public func effectiveState(at now: Date = .now, maxAge: TimeInterval = 60) -> String {
        guard let checkedAt, let observedAt = Self.parse(checkedAt) else { return "unknown" }
        let age = now.timeIntervalSince(observedAt)
        guard age >= -30, age <= maxAge else { return "unknown" }
        return state
    }

    private static func parse(_ value: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: value) { return date }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: value)
    }
}
public struct DeliverySummary: Decodable, Sendable {
    public let code: String
    public let at: String
    public let retryable: Bool
}
public struct Receipt: Decodable, Identifiable, Sendable {
    public var id: String { receiptId }
    public let receiptId: String
    public let state: String
    public let acceptedAt: String
    public let clientSavedAt: String?
    public let centralRecordId: String?
    public let centralProjectId: String?
    public let serverRecordedAt: String?
    public let deliveryCode: String?
    public let nextAttemptAt: String?
    public let content: ReceiptContent?
}
public struct ReceiptContent: Decodable, Sendable {
    public let requestId: String
    public let record: CapturedRecord
    public let scope: CaptureScope
    public let gitContext: JSONValue?
    public let clientSavedAt: String
}
public struct CapturedRecord: Decodable, Sendable {
    public let type: String
    public let title: String
    public let authority: String
    public let confidence: String
    public let confidenceReason: String?
    public let happenedAt: String?
    public let payload: JSONValue
}
public struct CaptureScope: Decodable, Sendable {
    public let workingDirectory: String?
    public let projectId: String?
}

/// A JSON value that retains object/array structure, strings, booleans, nulls,
/// and decimal numbers when showing a pending capture's original payload.
public enum JSONValue: Codable, Equatable, Sendable {
    case object([String: JSONValue])
    case array([JSONValue])
    case string(String)
    case number(Decimal)
    case bool(Bool)
    case null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null }
        else if let value = try? container.decode(Bool.self) { self = .bool(value) }
        else if let value = try? container.decode(String.self) { self = .string(value) }
        else if let value = try? container.decode(Decimal.self) { self = .number(value) }
        else if let value = try? container.decode([JSONValue].self) { self = .array(value) }
        else if let value = try? container.decode([String: JSONValue].self) { self = .object(value) }
        else { throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value") }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .object(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .null: try container.encodeNil()
        }
    }

    public func prettyPrinted() -> String? {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        guard let data = try? encoder.encode(self) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
public struct ReceiptList: Decodable, Sendable {
    public let receipts: [Receipt]
    public let nextCursor: String?
}
public struct Project: Decodable, Identifiable, Sendable {
    public let id: String
    public let name: String
}
public struct ProjectList: Decodable, Sendable { public let projects: [Project] }
public struct ServiceConfiguration: Codable, Equatable, Sendable {
    public var serverURL: String
    public var credentialFingerprint: String
    public var publicKeyPath: String
    public var signingKeyPath: String
    public init(serverURL: String = "", credentialFingerprint: String = "", publicKeyPath: String = "", signingKeyPath: String = "") {
        self.serverURL = serverURL
        self.credentialFingerprint = credentialFingerprint
        self.publicKeyPath = publicKeyPath
        self.signingKeyPath = signingKeyPath
    }
}
public struct ConfigurationResult: Decodable, Sendable { public let configuration: ServiceConfiguration }
public struct VerifyResult: Decodable, Sendable {
    public let serverURL: String
    public let credentialId: String
    public let verifiedAt: String
}
public struct ConfigurationBody: Encodable, Sendable {
    public let configuration: ServiceConfiguration
    public init(configuration: ServiceConfiguration) { self.configuration = configuration }
}
public struct ReceiptBody: Encodable, Sendable {
    public let receiptId: String
    public init(receiptId: String) { self.receiptId = receiptId }
}
public struct ListReceiptsBody: Encodable, Sendable {
    public let limit: Int
    public let cursor: String?
    public let state: String?
    public init(limit: Int, cursor: String?, state: String?) { self.limit = limit; self.cursor = cursor; self.state = state }
}
public struct ProjectSelectionBody: Encodable, Sendable {
    public let receiptId: String
    public let projectId: String
    public init(receiptId: String, projectId: String) { self.receiptId = receiptId; self.projectId = projectId }
}
public struct BindingBody: Encodable, Sendable { public let binding: ServiceBinding }
public struct ServiceBinding: Encodable, Sendable {
    public let serverURL: String
    public let credentialFingerprint: String
    public let publicKeyPath: String
    public let signingKeyPath: String
}

/// Version-one client for the private, per-user Scratchpad Unix socket.
public struct ServiceClient: Sendable {
    public let socketPath: String
    public let timeout: TimeInterval
    public static let defaultTimeout: TimeInterval = 5
    public static let stopTimeout: TimeInterval = 120

    public static func timeout(for operation: String) -> TimeInterval {
        operation == "stop" ? stopTimeout : defaultTimeout
    }

    public init(socketPath: String = "/tmp/scratchpad-\(getuid())/service.sock", timeout: TimeInterval = 5) {
        self.socketPath = socketPath
        self.timeout = timeout
    }

    public func call<Body: Encodable & Sendable, Result: Decodable & Sendable>(_ operation: String, body: Body, as resultType: Result.Type = Result.self) throws -> Result {
        let requestID = UUID().uuidString.lowercased()
        let request = try JSONEncoder().encode(RequestEnvelope(id: requestID, op: operation, body: body))
        guard request.count <= 4 * 1024 * 1024 else { throw LocalClientError.frameTooLarge }
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw LocalClientError.unavailable }
        defer { _ = close(fd) }
        var noSignal: Int32 = 1
        _ = setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &noSignal, socklen_t(MemoryLayout<Int32>.size))
        var sendTimeout = timeval(tv_sec: Int(timeout), tv_usec: Int32(timeout.truncatingRemainder(dividingBy: 1) * 1_000_000))
        _ = setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &sendTimeout, socklen_t(MemoryLayout<timeval>.size))
        _ = setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &sendTimeout, socklen_t(MemoryLayout<timeval>.size))

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8) + [0]
        guard pathBytes.count <= MemoryLayout.size(ofValue: address.sun_path) else { throw LocalClientError.invalidSocketPath }
        withUnsafeMutableBytes(of: &address.sun_path) { buffer in
            buffer.copyBytes(from: pathBytes)
        }
        guard let pathOffset = MemoryLayout<sockaddr_un>.offset(of: \.sun_path) else {
            throw LocalClientError.invalidSocketPath
        }
        let addressLength = socklen_t(pathOffset + pathBytes.count)
        guard addressLength <= socklen_t(UInt8.max) else { throw LocalClientError.invalidSocketPath }
        address.sun_len = UInt8(addressLength)
        let connected = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, addressLength) }
        }
        guard connected == 0 else { throw LocalClientError.unavailable }

        var frame = UInt32(request.count).bigEndian
        try writeAll(fd, bytes: withUnsafeBytes(of: &frame) { Data($0) } + request)
        let header = try readExact(fd, count: 4)
        let length = header.reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
        guard length <= 4 * 1024 * 1024 else { throw LocalClientError.frameTooLarge }
        let responseData = try readExact(fd, count: Int(length))
        let response = try JSONDecoder().decode(ResponseEnvelope<Result>.self, from: responseData)
        guard response.version == 1, response.id == requestID else { throw LocalClientError.invalidResponse }
        if let error = response.error { throw error }
        guard let result = response.result else { throw LocalClientError.invalidResponse }
        return result
    }

    private func writeAll(_ fd: Int32, bytes: Data) throws {
        try bytes.withUnsafeBytes { raw in
            guard let base = raw.baseAddress else { return }
            var sent = 0
            while sent < raw.count {
                let count = send(fd, base.advanced(by: sent), raw.count - sent, 0)
                guard count > 0 else { throw LocalClientError.unavailable }
                sent += count
            }
        }
    }
    private func readExact(_ fd: Int32, count: Int) throws -> Data {
        var data = Data(count: count)
        try data.withUnsafeMutableBytes { raw in
            guard let base = raw.baseAddress else { return }
            var received = 0
            while received < raw.count {
                let amount = recv(fd, base.advanced(by: received), raw.count - received, 0)
                guard amount > 0 else { throw LocalClientError.unavailable }
                received += amount
            }
        }
        return data
    }
}

public enum LocalClientError: Error, LocalizedError {
    case unavailable, frameTooLarge, invalidSocketPath, invalidResponse
    public var errorDescription: String? {
        switch self {
        case .unavailable: "Capture service is unavailable."
        case .frameTooLarge: "Service response exceeded the supported size."
        case .invalidSocketPath, .invalidResponse: "Capture service returned an invalid response."
        }
    }
}
