import AppKit
import Foundation
import MenubarKit
import SwiftUI
import Testing
import UserNotifications

@testable import ScratchpadApp
@testable import ScratchpadNative

// Website screenshots from fixture state. Runs only when
// SCRATCHPAD_SCREENSHOTS names an output directory (`task screenshots`);
// nothing on the desktop is captured and no service is contacted.

private let screenshotDirectory = ProcessInfo.processInfo.environment["SCRATCHPAD_SCREENSHOTS"].flatMap {
    $0.isEmpty ? nil : URL(fileURLWithPath: $0, isDirectory: true)
}

private struct SilentNotificationCenter: NotificationCenterClient {
    func requestAuthorization() async throws -> Bool { false }
    func authorizationStatus() async -> UNAuthorizationStatus { .notDetermined }
    func add(_ notification: MenubarNotification, playSound: Bool) async throws {}
}

private let fixtureCaptures: [(type: String, title: String, state: String, minutesAgo: Double)] = [
    ("decision", "Use SQLite for the local queue", "delivered", 4),
    ("finding", "Login item resets on upgrade", "delivered", 26),
    ("constraint", "No tokens in captures", "delivered", 95),
    ("failure", "Retry storm when the server restarts mid-sync", "delivered", 180),
    ("question", "Should project matching prefer the git remote?", "delivered", 320),
    ("project_state", "Agents now save through the scratchpad CLI", "delivered", 1440),
]

private let fixtureVersion = "2026.11.07.1"

/// A bundle whose Info.plist carries only the fixture release version.
private func fixtureBundle() throws -> Bundle {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(
        "scratchpad-fixture-\(UUID().uuidString).bundle")
    let contents = root.appendingPathComponent("Contents")
    try FileManager.default.createDirectory(at: contents, withIntermediateDirectories: true)
    let info: [String: Any] = [
        "CFBundleIdentifier": "com.example.scratchpad-fixture", "ReleaseVersion": fixtureVersion,
    ]
    try PropertyListSerialization.data(fromPropertyList: info, format: .xml, options: 0)
        .write(to: contents.appendingPathComponent("Info.plist"))
    return try #require(Bundle(url: root))
}

@MainActor
private func fixtureModel() throws -> AppModel {
    let now = Date()
    let iso = ISO8601DateFormatter()
    let status = ServiceStatus(
        lifecycle: "running", pending: 0, blocked: 0, rejected: 0, needsSelection: 0, keyAvailable: true,
        storageAvailable: true, lastDelivery: nil,
        connection: ConnectionObservation(state: "connected", checkedAt: iso.string(from: now.addingTimeInterval(-3))),
        queueSchemaVersion: 1, serviceVersion: fixtureVersion, serviceBuild: nil, serverVersion: fixtureVersion,
        serverCommit: nil)
    let receipts: [[String: Any]] = fixtureCaptures.enumerated().map { index, capture in
        let when = iso.string(from: now.addingTimeInterval(-capture.minutesAgo * 60))
        return [
            "receiptId": "fixture-\(index)", "state": capture.state, "acceptedAt": when,
            "content": [
                "requestId": "fixture-request-\(index)", "clientSavedAt": when, "scope": [:] as [String: Any],
                "record": [
                    "type": capture.type, "title": capture.title, "authority": "explicit", "confidence": "high",
                    "payload": [:] as [String: Any],
                ] as [String: Any],
            ] as [String: Any],
        ]
    }
    let data = try JSONSerialization.data(withJSONObject: receipts)
    return AppModel(
        fixtureStatus: status, receipts: try JSONDecoder().decode([Receipt].self, from: data),
        configuration: ServiceConfiguration(serverURL: "https://scratchpad.example.ts.net"),
        notificationCenter: SilentNotificationCenter())
}

@MainActor
private func write(_ data: Data?, _ name: String, to directory: URL) throws {
    let png = try #require(data, "\(name) did not render")
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    try png.write(to: directory.appendingPathComponent("\(name).png"))
}

@Test(.enabled(if: screenshotDirectory != nil)) @MainActor
func documentationScreenshots() throws {
    let directory = try #require(screenshotDirectory)
    _ = NSApplication.shared
    let bundle = try fixtureBundle()
    for (suffix, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
        let model = try fixtureModel()
        try write(
            PanelSnapshot.png(of: MenuContent(bundle: bundle).environmentObject(model), appearance: appearance),
            "panel-\(suffix)", to: directory)

        model.workspacePage = .general
        try write(
            PanelSnapshot.png(
                of: GeneralPage(bundle: bundle).environmentObject(model).frame(height: 690), width: 620,
                chrome: .window(title: "General"), appearance: appearance),
            "settings-\(suffix)", to: directory)

        model.workspacePage = .overview
        try write(
            PanelSnapshot.png(
                of: OverviewPage().environmentObject(model).frame(height: 600), width: 720,
                chrome: .window(title: "Overview"), appearance: appearance),
            "workspace-overview-\(suffix)", to: directory)
    }
}
