import Foundation

/// Whether the running capture service matches the installed app. Homebrew
/// replaces the bundle in place, but the service keeps running the binary it
/// started with until it is restarted.
public enum ServiceUpdateState: Equatable, Sendable {
    /// The service runs the installed version, or either side is a development build.
    case current
    /// The service runs an older (or different) build than the installed app.
    case restartNeeded(running: String)
    /// The service did not report its version, so nothing can be compared.
    case unknown

    public init(status: ServiceStatus?, appVersion: String?, appBuild: String?) {
        guard let version = status?.serviceVersion, !version.isEmpty,
              let appVersion, !appVersion.isEmpty else {
            self = .unknown
            return
        }
        if version == "dev" || appVersion == "dev" {
            self = .current
            return
        }
        let build = status?.serviceBuild
        if version == appVersion && (build == nil || appBuild == nil || build == appBuild) {
            self = .current
        } else {
            self = .restartNeeded(running: Self.label(version: version, build: build))
        }
    }

    public static func label(version: String, build: String?) -> String {
        guard let build, !build.isEmpty, build != version else { return version }
        return "\(version) (\(build))"
    }
}
