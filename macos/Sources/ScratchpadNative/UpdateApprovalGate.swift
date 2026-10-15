import Foundation

/// Sparkle's v2 stages are 0=not-downloaded, 1=downloaded, 2=installing.
/// Unknown future stages fail closed and require an owner choice/cancellation.
public enum UpdateStagePolicy {
    public enum Action: Equatable, Sendable {
        case downloadAutomatically
        case requestInstallApproval
        case requireInstallOrCancel
    }

    public static func action(sparkleStageRawValue: Int) -> Action {
        switch sparkleStageRawValue {
        case 0: .downloadAutomatically
        case 1: .requestInstallApproval
        default: .requireInstallOrCancel
        }
    }
}

/// Enforces that Sparkle's delayed-install callback is never invoked before
/// both an explicit user decision and successful service quiescence.
public struct UpdateApprovalGate: Sendable {
    public enum State: Equatable, Sendable {
        case idle
        case awaitingApproval
        case approved
        case quiescing
        case installAuthorized
        case declined
        case failed
    }

    public private(set) var state: State = .idle
    public init() {}

    public mutating func downloadedUpdateNeedsApproval() {
        guard state == .idle || state == .declined else { return }
        state = .awaitingApproval
    }

    public mutating func approve() {
        guard state == .awaitingApproval else { return }
        state = .approved
    }

    public mutating func decline() {
        guard state == .awaitingApproval else { return }
        state = .declined
    }

    public mutating func beginQuiescence() {
        guard state == .approved else { return }
        state = .quiescing
    }

    public mutating func quiescenceSucceeded() {
        guard state == .quiescing else { return }
        state = .installAuthorized
    }

    public mutating func quiescenceFailed() {
        guard state == .quiescing else { return }
        state = .failed
    }

    public var mayInvokeInstallHandler: Bool { state == .installAuthorized }
}
