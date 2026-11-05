import Foundation
import MenubarKit

public enum IncidentEvent: Equatable, Sendable {
    case outage
    case blocked(count: Int)
    case localSavingUnavailable
    case recovered

    /// The notification posted for this event. Counts only, never capture content.
    public var notification: MenubarNotification {
        switch self {
        case .outage:
            MenubarNotification(
                title: "Scratchpad server is offline",
                body: "Capture service is still available. Pending captures are waiting for the server.")
        case .blocked(let count):
            MenubarNotification(
                title: "Scratchpad delivery needs attention",
                body: "\(count) capture\(count == 1 ? "" : "s") need review. Open Scratchpad to inspect them.")
        case .localSavingUnavailable:
            MenubarNotification(
                title: "Scratchpad cannot confirm local saves",
                body: "The capture service is unavailable. New captures may not be saved locally.")
        case .recovered:
            MenubarNotification(
                title: "Scratchpad recovered",
                body: "The previously reported issue has cleared.")
        }
    }
}

/// Maps capture-service observations onto MenubarKit's de-duplicator:
/// one incident and one recovery per condition.
public struct IncidentTracker: Sendable {
    enum Incident: Hashable, Sendable { case outage, blocked, localSave }

    private var incidents: IncidentDeduplicator<Incident>

    public init(localSaveIncidentAlreadyNotified: Bool = false) {
        incidents = IncidentDeduplicator(
            delays: [.outage: 60],
            reportedIncidents: localSaveIncidentAlreadyNotified ? [.localSave] : []
        )
    }

    public var isLocalSaveIncidentActive: Bool { incidents.isReported(.localSave) }

    public mutating func resetLocalSaveIncident() {
        incidents.reset(.localSave)
    }

    public mutating func observe(
        _ status: ServiceStatus?,
        serviceExpected: Bool = false,
        absenceSuppressed: Bool = false,
        at now: Date = .now
    ) -> [IncidentEvent] {
        guard let status else {
            guard serviceExpected, !absenceSuppressed else { return [] }
            return incidents.observe(.localSave, isActive: true, at: now).map { _ in [.localSavingUnavailable] } ?? []
        }
        var events: [IncidentEvent] = []

        // Only a fresh "connectivity" or "connected" probe is conclusive about an outage.
        let connection = status.connection?.effectiveState(at: now)
        if connection == "connectivity" || connection == "connected" {
            if let change = incidents.observe(.outage, isActive: connection == "connectivity", at: now) {
                events.append(change == .incident(.outage) ? .outage : .recovered)
            }
        }

        let blockedCount = status.blocked + status.rejected + status.needsSelection
        if let change = incidents.observe(.blocked, isActive: blockedCount > 0, at: now) {
            events.append(change == .incident(.blocked) ? .blocked(count: blockedCount) : .recovered)
        }

        let serviceAcceptingCaptures = status.lifecycle == "running" || status.lifecycle == "paused"
        let canSaveLocally = status.keyAvailable && status.storageAvailable != false
        if serviceAcceptingCaptures {
            if let change = incidents.observe(.localSave, isActive: !canSaveLocally, at: now) {
                events.append(change == .incident(.localSave) ? .localSavingUnavailable : .recovered)
            }
        } else if status.lifecycle == "stopped" {
            // The user stopped the service: forget the incident without a recovery notice.
            incidents.reset(.localSave)
        }
        return events
    }
}
