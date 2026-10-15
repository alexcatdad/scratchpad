import Foundation

public enum IncidentEvent: Equatable, Sendable {
    case outage
    case blocked(count: Int)
    case localSavingUnavailable
    case recovered
}

/// Coalesces repeated service observations into one incident and one recovery.
public struct IncidentTracker: Sendable {
    private var outageStartedAt: Date?
    private var outageNotified = false
    private var blockedNotified = false
    private var localSaveNotified = false

    public init(localSaveIncidentAlreadyNotified: Bool = false) {
        localSaveNotified = localSaveIncidentAlreadyNotified
    }

    public var isLocalSaveIncidentActive: Bool { localSaveNotified }

    public mutating func resetLocalSaveIncident() {
        localSaveNotified = false
    }

    public mutating func observe(
        _ status: ServiceStatus?,
        serviceExpected: Bool = false,
        absenceSuppressed: Bool = false,
        at now: Date = .now
    ) -> [IncidentEvent] {
        guard let status else {
            guard serviceExpected, !absenceSuppressed, !localSaveNotified else { return [] }
            localSaveNotified = true
            return [.localSavingUnavailable]
        }
        var events: [IncidentEvent] = []
        let connection = status.connection?.effectiveState(at: now)
        if connection == "connectivity" {
            outageStartedAt = outageStartedAt ?? now
            if !outageNotified, now.timeIntervalSince(outageStartedAt!) > 60 {
                outageNotified = true
                events.append(.outage)
            }
        } else if connection == "connected" {
            let outageWasNotified = outageNotified
            outageStartedAt = nil
            outageNotified = false
            if outageWasNotified {
                events.append(.recovered)
            }
        }

        let blockedCount = status.blocked + status.rejected + status.needsSelection
        if blockedCount > 0 && !blockedNotified {
            blockedNotified = true
            events.append(.blocked(count: blockedCount))
        } else if blockedCount == 0 && blockedNotified {
            blockedNotified = false
            events.append(.recovered)
        }

        let serviceAcceptingCaptures = status.lifecycle == "running" || status.lifecycle == "paused"
        let localSavingAvailable = status.keyAvailable && status.storageAvailable != false && serviceAcceptingCaptures
        let localSavingUnavailable = (!status.keyAvailable || status.storageAvailable == false) && serviceAcceptingCaptures
        if localSavingUnavailable && !localSaveNotified {
            localSaveNotified = true
            events.append(.localSavingUnavailable)
        } else if localSavingAvailable && localSaveNotified {
            localSaveNotified = false
            events.append(.recovered)
        } else if status.lifecycle == "stopped" && localSaveNotified {
            localSaveNotified = false
        }
        return events
    }
}
