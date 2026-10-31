import Foundation

/// One description of the capture service's state, shared by the menu bar,
/// the window sidebar and Overview so every surface uses the same words.
/// Every tone has its own symbol shape, so state never depends on colour alone.
public struct StatusPresentation: Equatable, Sendable {
    public enum Tone: Equatable, Sendable {
        case ok, warning, error, off, update

        public var symbol: String {
            switch self {
            case .ok: "checkmark.circle.fill"
            case .warning: "exclamationmark.triangle.fill"
            case .error: "xmark.octagon.fill"
            case .off: "stop.circle.fill"
            case .update: "arrow.up.circle.fill"
            }
        }
    }

    public enum Action: Equatable, Sendable {
        case startService, restartService, resumeSyncing, checkAgain, reconnect, reviewCaptures
    }

    public struct Row: Equatable, Sendable {
        public let title: String
        public let value: String
        public let tone: Tone
    }

    public let tone: Tone
    /// The short status word, as used in the window sidebar.
    public let word: String
    public let headline: String
    public let detail: String
    public let action: Action?
    public let rows: [Row]

    public var actionTitle: String? {
        switch action {
        case .startService: "Start Capture Service"
        case .restartService: "Restart Capture Service"
        case .resumeSyncing: "Resume Syncing"
        case .checkAgain: "Try Again Now"
        case .reconnect: "Reconnect…"
        case .reviewCaptures: "Review Captures"
        case nil: nil
        }
    }

    public init(status: ServiceStatus?, update: ServiceUpdateState, now: Date = .now) {
        guard let status else {
            self.init(tone: .off, word: "Not running", headline: "Capture service not running",
                      detail: "AI tools cannot save memory until it starts.", action: .startService,
                      rows: [Row(title: "Capture service", value: "Not running", tone: .off),
                             Row(title: "Server", value: "Not checked", tone: .off)])
            return
        }
        let waiting = status.pending + status.blocked + status.rejected + status.needsSelection
        let waitingRow = Row(title: "Waiting to sync",
                             value: waiting == 0 ? "None" : Self.captures(waiting),
                             tone: waiting == 0 ? .ok : .warning)
        let serviceRow: Row
        switch status.lifecycle {
        case "running": serviceRow = Row(title: "Capture service", value: "Running", tone: .ok)
        case "paused": serviceRow = Row(title: "Capture service", value: "Syncing paused", tone: .warning)
        case "quiescing": serviceRow = Row(title: "Capture service", value: "Restarting", tone: .off)
        default: serviceRow = Row(title: "Capture service", value: "Stopped", tone: .off)
        }
        let connection = status.connection?.effectiveState(at: now) ?? "unknown"
        let serverRow: Row
        switch connection {
        case "connected": serverRow = Row(title: "Server", value: "Connected", tone: .ok)
        case "connectivity": serverRow = Row(title: "Server", value: "Unreachable", tone: .warning)
        case "authentication", "credential_changed": serverRow = Row(title: "Server", value: "Sign-in needed", tone: .error)
        case "server_incompatible": serverRow = Row(title: "Server", value: "Needs an update", tone: .error)
        default: serverRow = Row(title: "Server", value: "Checking", tone: .off)
        }
        let rows = [serviceRow, serverRow, waitingRow]

        if status.lifecycle == "stopped" {
            self.init(tone: .off, word: "Stopped", headline: "Capture service stopped",
                      detail: "AI tools cannot save memory until you start it.", action: .startService, rows: rows)
        } else if status.lifecycle == "quiescing" {
            self.init(tone: .off, word: "Restarting", headline: "Capture service restarting",
                      detail: "Captures already saved on this Mac are kept.", action: nil, rows: rows)
        } else if !status.keyAvailable || status.storageAvailable == false {
            self.init(tone: .error, word: "Cannot save", headline: "Cannot save on this Mac",
                      detail: status.keyAvailable
                          ? "The local capture storage is unavailable."
                          : "The capture queue key is locked. Unlock your login keychain.",
                      action: nil, rows: rows)
        } else if case .restartNeeded(let running) = update {
            self.init(tone: .update, word: "Finish update", headline: "Update installed",
                      detail: "The capture service is still running \(running). Restarting keeps every queued capture.",
                      action: .restartService, rows: rows)
        } else if status.lifecycle == "paused" {
            self.init(tone: .warning, word: "Paused", headline: "Syncing paused",
                      detail: "Captures are saved on this Mac and sync when you resume.", action: .resumeSyncing, rows: rows)
        } else {
            switch connection {
            case "connectivity":
                self.init(tone: .warning, word: "Unreachable", headline: "Saving on this Mac",
                          detail: waiting == 0
                              ? "The server is unreachable. New captures sync when it is back."
                              : "The server is unreachable. \(Self.captures(waiting)) will sync when it is back.",
                          action: .checkAgain, rows: rows)
            case "authentication", "credential_changed":
                self.init(tone: .error, word: "Sign-in needed", headline: "Reconnect to keep syncing",
                          detail: "The server no longer accepts this Mac’s key. Captures are still saved here.",
                          action: .reconnect, rows: rows)
            case "server_incompatible":
                self.init(tone: .error, word: "Server outdated", headline: "Server needs an update",
                          detail: "Captures are saved on this Mac until the server is updated.", action: nil, rows: rows)
            case "connected":
                if status.needsSelection > 0 || status.blocked > 0 || status.rejected > 0 {
                    let count = status.needsSelection + status.blocked + status.rejected
                    self.init(tone: .warning, word: "Needs you", headline: "\(Self.captures(count)) need\(count == 1 ? "s" : "") you",
                              detail: "Choose a project or review why the server refused them.",
                              action: .reviewCaptures, rows: rows)
                } else {
                    self.init(tone: .ok, word: "Connected", headline: "Saving and syncing",
                              detail: status.pending == 0
                                  ? "Every capture has reached the server."
                                  : "\(Self.captures(status.pending)) syncing now.",
                              action: nil, rows: rows)
                }
            default:
                self.init(tone: .off, word: "Checking", headline: "Checking the server",
                          detail: "Captures are saved on this Mac while the connection is checked.", action: nil, rows: rows)
            }
        }
    }

    init(tone: Tone, word: String, headline: String, detail: String, action: Action?, rows: [Row]) {
        self.tone = tone
        self.word = word
        self.headline = headline
        self.detail = detail
        self.action = action
        self.rows = rows
    }

    static func captures(_ count: Int) -> String { count == 1 ? "1 capture" : "\(count) captures" }
}
