import SwiftUI
import AppKit
import ScratchpadNative

/// Shared design tokens: macOS materials and system colours, with amber as the
/// one accent. Amber is only ever a fill and always carries ink text.
extension Color {
    static let scratchpadAmber = Color(nsColor: NSColor(name: "scratchpadAmber") { appearance in
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(srgbRed: 1.0, green: 0.788, blue: 0.235, alpha: 1)
            : NSColor(srgbRed: 0.949, green: 0.718, blue: 0.02, alpha: 1)
    })
    /// Amber used as text or icon on light backgrounds, where the fill colour is unreadable.
    static let scratchpadAmberInk = Color(nsColor: NSColor(name: "scratchpadAmberInk") { appearance in
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(srgbRed: 1.0, green: 0.788, blue: 0.235, alpha: 1)
            : NSColor(srgbRed: 0.541, green: 0.353, blue: 0, alpha: 1)
    })
    static let scratchpadInk = Color(red: 0.102, green: 0.102, blue: 0.102)
}

extension StatusPresentation.Tone {
    var color: Color {
        switch self {
        case .ok: .green
        case .warning: .orange
        case .error: .red
        case .off: .secondary
        case .update: .scratchpadAmberInk
        }
    }
}

/// The status symbol: its shape carries the meaning, colour only reinforces it.
struct StatusIcon: View {
    let tone: StatusPresentation.Tone
    var size: CGFloat = 13
    var body: some View {
        Image(systemName: tone.symbol)
            .font(.system(size: size, weight: .semibold))
            .foregroundStyle(tone.color)
            .accessibilityHidden(true)
    }
}

/// Primary actions: an amber capsule with ink text.
struct ProminentButtonStyle: ButtonStyle {
    var fullWidth = false
    @Environment(\.isEnabled) private var isEnabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(Color.scratchpadInk)
            .padding(.horizontal, 14)
            .frame(minHeight: 28)
            .frame(maxWidth: fullWidth ? .infinity : nil)
            .background(Capsule().fill(Color.scratchpadAmber.opacity(configuration.isPressed ? 0.8 : 1)))
            .opacity(isEnabled ? 1 : 0.5)
            .contentShape(Capsule())
    }
}

/// A full-width menu row in the menu bar popover.
struct MenuRowButtonStyle: ButtonStyle {
    @State private var hovering = false
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 10)
            .frame(minHeight: 28)
            .background(RoundedRectangle(cornerRadius: 8).fill(.primary.opacity(configuration.isPressed ? 0.12 : hovering ? 0.07 : 0)))
            .contentShape(Rectangle())
            .onHover { hovering = $0 }
    }
}

extension View {
    /// The rounded tile used to group rows in the popover and Overview.
    func groupedTile() -> some View {
        padding(.vertical, 4)
            .background(.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 12))
    }
}

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
        let date = formatter.date(from: timestamp) ?? {
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
