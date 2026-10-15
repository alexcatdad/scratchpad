import Foundation
import ServiceManagement

public struct ServiceSupervisor {
    public static let serviceExecutable = "scratchpad-service"
    public static let loginAgentPlist = "com.alexcatdad.scratchpad.service.plist"

    public init() {}

    public static func serviceURL(bundle: Bundle = .main) -> URL {
        bundle.bundleURL.appending(path: "Contents/Helpers/\(serviceExecutable)")
    }

    /// Start is deliberately an explicit UI action. It starts the bundled
    /// service with the one-shot flag that permits clearing its stopped state.
    @discardableResult
    public func start(bundle: Bundle = .main, onTermination: (@Sendable (Int32) -> Void)? = nil) throws -> Process {
        try launch(bundle: bundle, arguments: ["--explicit-start"], onTermination: onTermination)
    }

    /// Used only when restoring the service after an updater handoff. The
    /// service reads durable Pause/Stop state and cannot clear Stop here.
    @discardableResult
    public func launchRespectingPersistentState(bundle: Bundle = .main) throws -> Process {
        try launch(bundle: bundle, arguments: [])
    }

    @discardableResult
    private func launch(bundle: Bundle, arguments: [String], onTermination: (@Sendable (Int32) -> Void)? = nil) throws -> Process {
        let executable = Self.serviceURL(bundle: bundle)
        guard FileManager.default.isExecutableFile(atPath: executable.path) else { throw SupervisorError.serviceMissing }
        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        if let onTermination {
            process.terminationHandler = { child in onTermination(child.terminationStatus) }
        }
        try process.run()
        return process
    }

    public var loginItemStatus: SMAppService.Status { SMAppService.agent(plistName: Self.loginAgentPlist).status }
    public func setStartAtLogin(_ enabled: Bool) throws {
        let agent = SMAppService.agent(plistName: Self.loginAgentPlist)
        if enabled {
            if agent.status != .enabled { try agent.register() }
        } else if agent.status == .enabled {
            try agent.unregister()
        }
    }
}

public enum SupervisorError: Error, LocalizedError {
    case serviceMissing
    public var errorDescription: String? { "The bundled capture service is missing. Reinstall Scratchpad to repair this app." }
}
