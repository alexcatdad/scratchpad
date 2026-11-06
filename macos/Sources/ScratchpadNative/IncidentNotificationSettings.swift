import Foundation
import MenubarKit

/// The `UserDefaults` keys behind the incident notification toggles. The
/// Settings page binds its toggles to these keys, and the notifier reads its
/// preferences from them, so both always agree.
public enum IncidentNotificationSettings {
    /// "Notify me about problems". Kept from before MenubarKit so users keep their setting.
    public static let enabledKey = "incidentNotificationsEnabled"
    /// "Play a sound". Incident notifications are silent unless the user turns it on.
    public static let soundEnabledKey = "incidentNotificationSoundEnabled"

    /// The notifier's preferences, read from `defaults`; missing keys read as off.
    public static func preferences(defaults: UserDefaults = .standard) -> NotificationPreferences {
        NotificationPreferences(
            enabled: defaults.bool(forKey: enabledKey),
            soundEnabled: defaults.bool(forKey: soundEnabledKey)
        )
    }
}
