import Foundation
import MenubarKit
import Testing

@testable import ScratchpadNative

@Suite struct IncidentNotificationSettingsTests {
    private func makeDefaults() -> UserDefaults {
        let name = "IncidentNotificationSettingsTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defaults.removePersistentDomain(forName: name)
        return defaults
    }

    @Test func notificationsAndSoundDefaultToOff() {
        #expect(IncidentNotificationSettings.preferences(defaults: makeDefaults()) == NotificationPreferences())
    }

    @Test func keepsTheExistingEnabledKey() {
        #expect(IncidentNotificationSettings.enabledKey == "incidentNotificationsEnabled")
        #expect(IncidentNotificationSettings.soundEnabledKey != IncidentNotificationSettings.enabledKey)
    }

    @Test func enablingNotificationsStaysSilent() {
        let defaults = makeDefaults()
        defaults.set(true, forKey: IncidentNotificationSettings.enabledKey)
        let preferences = IncidentNotificationSettings.preferences(defaults: defaults)
        #expect(preferences.enabled)
        #expect(!preferences.soundEnabled)
    }

    @Test func soundFollowsItsOwnKey() {
        let defaults = makeDefaults()
        defaults.set(true, forKey: IncidentNotificationSettings.enabledKey)
        defaults.set(true, forKey: IncidentNotificationSettings.soundEnabledKey)
        #expect(
            IncidentNotificationSettings.preferences(defaults: defaults)
                == NotificationPreferences(enabled: true, soundEnabled: true))
    }

    @Test func ignoresMenubarKitDefaultSoundKey() {
        let defaults = makeDefaults()
        defaults.set(true, forKey: IncidentNotificationSettings.enabledKey)
        defaults.set(true, forKey: NotificationPreferences.soundEnabledKey)
        #expect(!IncidentNotificationSettings.preferences(defaults: defaults).soundEnabled)
    }
}
