/// Decisions for restoring an independent local service after an updater
/// handoff. A rejected resume while the listener still exists must drain
/// before a replacement is launched.
public enum ServiceRecoveryPolicy {
    public enum Action: Equatable, Sendable {
        case alreadyRestored
        case waitForExit
        case launchRespectingPersistentState
    }

    public static func action(socketExists: Bool, resumeAccepted: Bool, healthy: Bool) -> Action {
        if resumeAccepted && healthy { return .alreadyRestored }
        return socketExists ? .waitForExit : .launchRespectingPersistentState
    }
}
