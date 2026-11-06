import AppKit
import MenubarKit
import ScratchpadNative
import SwiftUI

// Tokens, the status icon, button styles and grouped tiles come from
// MenubarKit, whose default theme is Scratchpad amber.

/// Human labels for receipt states, shared by the popover and the Captures page.
enum ReceiptLabels {
    static func state(_ state: String) -> String {
        switch state {
        case "delivered": "Synced"
        case "pending": "Waiting"
        case "blocked": "Blocked"
        case "rejected": "Rejected"
        case "needs_selection": "Needs a project"
        default: state.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }
    static func tone(_ state: String) -> StatusPresentation.Tone {
        switch state {
        case "delivered": .ok
        case "pending": .off
        case "needs_selection": .warning
        default: .error
        }
    }
    static func type(_ receipt: Receipt) -> String {
        receipt.content.map { $0.record.type.replacingOccurrences(of: "_", with: " ").capitalized } ?? "Capture"
    }
    static func title(_ receipt: Receipt) -> String {
        receipt.content?.record.title ?? (receipt.state == "delivered" ? "Synced capture" : "Capture waiting to sync")
    }
    static func when(_ timestamp: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date =
            formatter.date(from: timestamp)
            ?? {
                formatter.formatOptions = [.withInternetDateTime]
                return formatter.date(from: timestamp)
            }()
        guard let date else { return timestamp }
        return date.formatted(.relative(presentation: .named, unitsStyle: .abbreviated))
    }
}

/// Empty-state placeholder (ContentUnavailableView needs macOS 14).
struct EmptyStateView: View {
    let title: String
    let symbol: String
    let detail: String
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: symbol).font(.system(size: 34)).foregroundStyle(.secondary).accessibilityHidden(true)
            Text(title).font(.headline)
            Text(detail).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding()
    }
}
