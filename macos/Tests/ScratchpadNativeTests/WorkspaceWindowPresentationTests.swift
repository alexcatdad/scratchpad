import AppKit
import Testing
import ScratchpadNative

@Test @MainActor func workspacePresentationShowsTheWorkspaceInsteadOfTheTrayWindow() {
    _ = NSApplication.shared
    let tray = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 100, height: 100), styleMask: [.titled], backing: .buffered, defer: false)
    let workspace = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 100, height: 100), styleMask: [.titled], backing: .buffered, defer: false)
    tray.isReleasedWhenClosed = false
    workspace.isReleasedWhenClosed = false
    tray.identifier = NSUserInterfaceItemIdentifier("tray-popover")
    workspace.identifier = WorkspaceWindowPresentation.identifier
    defer { workspace.close(); tray.close() }

    #expect(WorkspaceWindowPresentation.present(in: [tray, workspace]))
    #expect(workspace.isVisible)
    #expect(!tray.isVisible)
}

@Test @MainActor func workspacePresentationDoesNotRaiseAnUnrelatedWindow() {
    _ = NSApplication.shared
    let unrelated = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 100, height: 100), styleMask: [.titled], backing: .buffered, defer: false)
    unrelated.isReleasedWhenClosed = false
    unrelated.identifier = NSUserInterfaceItemIdentifier("other")
    defer { unrelated.close() }

    #expect(!WorkspaceWindowPresentation.present(in: [unrelated]))
    #expect(!unrelated.isVisible)
}
