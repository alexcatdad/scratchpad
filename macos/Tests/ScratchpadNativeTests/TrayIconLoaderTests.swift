import AppKit
import Foundation
import Testing
import ScratchpadNative

@Test func trayIconLoaderAddsRealBundledPNGsAtOneLogicalSize() throws {
    let sourceResources = packageResourcesDirectory()
    let temporaryBundle = FileManager.default.temporaryDirectory
        .appendingPathComponent("ScratchpadTrayIconTests-\(UUID().uuidString).app", isDirectory: true)
        .appendingPathComponent("Contents/Resources", isDirectory: true)
    try FileManager.default.createDirectory(at: temporaryBundle, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: temporaryBundle.deletingLastPathComponent().deletingLastPathComponent()) }

    for name in ["ScratchpadTrayIcon.png", "ScratchpadTrayIcon@2x.png"] {
        try FileManager.default.copyItem(
            at: sourceResources.appendingPathComponent(name),
            to: temporaryBundle.appendingPathComponent(name)
        )
    }

    let image = try #require(TrayIconLoader.load(resourcesDirectory: temporaryBundle))
    #expect(image.isTemplate)
    #expect(image.size == NSSize(width: 18, height: 18))

    let representations = image.representations.compactMap { $0 as? NSBitmapImageRep }
    #expect(representations.count == 2)
    #expect(Set(representations.map { "\($0.pixelsWide)x\($0.pixelsHigh)" }) == ["18x18", "36x36"])
    #expect(representations.allSatisfy { $0.size == NSSize(width: 18, height: 18) })
    #expect(representations.allSatisfy(hasVisibleAlpha))
}

@Test func trayIconLoaderFailsClosedWhenOneResourceIsMissing() throws {
    let sourceResources = packageResourcesDirectory()
    let temporaryBundle = FileManager.default.temporaryDirectory
        .appendingPathComponent("ScratchpadTrayIconMissingTests-\(UUID().uuidString).app", isDirectory: true)
        .appendingPathComponent("Contents/Resources", isDirectory: true)
    try FileManager.default.createDirectory(at: temporaryBundle, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: temporaryBundle.deletingLastPathComponent().deletingLastPathComponent()) }
    try FileManager.default.copyItem(
        at: sourceResources.appendingPathComponent("ScratchpadTrayIcon.png"),
        to: temporaryBundle.appendingPathComponent("ScratchpadTrayIcon.png")
    )

    #expect(TrayIconLoader.load(resourcesDirectory: temporaryBundle) == nil)
    #expect(TrayIconLoader.load(resourcesDirectory: nil) == nil)
}

private func packageResourcesDirectory() -> URL {
    URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("Resources", isDirectory: true)
}

private func hasVisibleAlpha(_ representation: NSBitmapImageRep) -> Bool {
    for y in 0..<representation.pixelsHigh {
        for x in 0..<representation.pixelsWide {
            if let color = representation.colorAt(x: x, y: y)?.usingColorSpace(.deviceRGB), color.alphaComponent > 0 {
                return true
            }
        }
    }
    return false
}
