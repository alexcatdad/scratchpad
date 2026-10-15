import Foundation

/// Describes what the last local service observation proves about capture intake.
/// It is intentionally independent of remote server connectivity.
public enum LocalCaptureReadiness: Equatable, Sendable {
    case unconfirmed
    case ready
    case readyWithDeliveryPaused
    case stopped
    case queueKeyUnavailable
    case storageUnavailable
    case restarting

    public init(status: ServiceStatus?, observedAt: Date?, now: Date = .now, maxAge: TimeInterval = 30) {
        guard let observedAt, now.timeIntervalSince(observedAt) >= -5,
              now.timeIntervalSince(observedAt) <= maxAge else {
            self = .unconfirmed
            return
        }
        guard let status else {
            self = .unconfirmed
            return
        }

        switch status.lifecycle {
        case "stopped":
            self = .stopped
        case "quiescing":
            self = .restarting
        case "running", "paused":
            if !status.keyAvailable {
                self = .queueKeyUnavailable
            } else if status.storageAvailable == false {
                self = .storageUnavailable
            } else if status.storageAvailable == nil {
                self = .unconfirmed
            } else if status.lifecycle == "paused" {
                self = .readyWithDeliveryPaused
            } else {
                self = .ready
            }
        default:
            self = .unconfirmed
        }
    }

    public var label: String {
        switch self {
        case .unconfirmed: "Local service status not confirmed"
        case .ready: "Ready for local capture"
        case .readyWithDeliveryPaused: "Ready for local capture · delivery paused"
        case .stopped: "Stopped · new captures unavailable"
        case .queueKeyUnavailable: "Queue key unavailable"
        case .storageUnavailable: "Local storage unavailable"
        case .restarting: "Local service preparing to stop · capture not confirmed"
        }
    }
}
