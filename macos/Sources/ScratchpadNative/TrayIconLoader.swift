import AppKit

public enum TrayIconLoader {
    public static let logicalSize = NSSize(width: 18, height: 18)

    private static let representations: [(name: String, pixels: Int)] = [
        ("ScratchpadTrayIcon.png", 18),
        ("ScratchpadTrayIcon@2x.png", 36),
    ]

    public static func load(resourcesDirectory: URL?) -> NSImage? {
        guard let resourcesDirectory else { return nil }

        var imageRepresentations: [NSBitmapImageRep] = []
        for resource in representations {
            let url = resourcesDirectory.appendingPathComponent(resource.name)
            guard let data = try? Data(contentsOf: url),
                  let representation = NSBitmapImageRep(data: data),
                  representation.pixelsWide == resource.pixels,
                  representation.pixelsHigh == resource.pixels,
                  representation.hasAlpha,
                  hasVisiblePixel(representation)
            else {
                return nil
            }

            representation.size = logicalSize
            imageRepresentations.append(representation)
        }

        let image = NSImage(size: logicalSize)
        imageRepresentations.forEach(image.addRepresentation)
        image.isTemplate = true
        return image
    }

    private static func hasVisiblePixel(_ representation: NSBitmapImageRep) -> Bool {
        for y in 0..<representation.pixelsHigh {
            for x in 0..<representation.pixelsWide {
                guard let color = representation.colorAt(x: x, y: y)?.usingColorSpace(.deviceRGB) else {
                    continue
                }
                if color.alphaComponent > 0 { return true }
            }
        }
        return false
    }
}
