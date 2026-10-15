import Foundation
import UserNotifications

public enum IncidentNotifier {
    public enum PermissionState: Equatable, Sendable {
        case notDetermined
        case authorized
        case denied
    }

    public static func requestPermission() async -> PermissionState {
        do {
            let granted = try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound])
            return granted ? .authorized : .denied
        } catch {
            return .denied
        }
    }

    public static func permissionState() async -> PermissionState {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        switch settings.authorizationStatus {
        case .notDetermined: return .notDetermined
        case .authorized, .provisional, .ephemeral: return .authorized
        case .denied: return .denied
        @unknown default: return .denied
        }
    }

    public static func deliver(_ event: IncidentEvent) {
        let content = UNMutableNotificationContent()
        switch event {
        case .outage:
            content.title = "Scratchpad server is offline"
            content.body = "Capture service is still available. Pending captures are waiting for the server."
        case .blocked(let count):
            content.title = "Scratchpad delivery needs attention"
            content.body = "\(count) capture\(count == 1 ? "" : "s") need review. Open Scratchpad to inspect them."
        case .localSavingUnavailable:
            content.title = "Scratchpad cannot confirm local saves"
            content.body = "The capture service is unavailable. New captures may not be saved locally."
        case .recovered:
            content.title = "Scratchpad recovered"
            content.body = "The previously reported issue has cleared."
        }
        let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request) { _ in }
    }
}
