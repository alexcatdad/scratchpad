import AppKit

/// Only the workspace may receive focus; the tray popover is another app window.
@MainActor
public enum WorkspaceWindowPresentation {
    public static let identifier = NSUserInterfaceItemIdentifier("workspace")

    @discardableResult
    public static func present(in windows: [NSWindow] = NSApplication.shared.windows) -> Bool {
        guard let window = windows.first(where: { $0.identifier == identifier }) else { return false }
        if #available(macOS 14, *) {
            NSApp.activate()
        } else {
            NSApp.activate(ignoringOtherApps: true)
        }
        if window.isMiniaturized { window.deminiaturize(nil) }
        window.makeKeyAndOrderFront(nil)
        window.orderFrontRegardless()
        return true
    }
}
