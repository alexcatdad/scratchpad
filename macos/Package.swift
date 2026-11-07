// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "ScratchpadMac",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "Scratchpad", targets: ["ScratchpadApp"]),
        .executable(name: "scratchpad-keychain", targets: ["ScratchpadKeychain"]),
        .library(name: "ScratchpadNative", targets: ["ScratchpadNative"]),
    ],
    dependencies: [
        // First-party tray, panel and notification building blocks.
        // menubar-kit v2026.11.07.2 (SwiftPM cannot resolve four-part CalVer tags, so the tag's commit is pinned).
        .package(
            url: "https://github.com/alexcatdad/menubar-kit.git", revision: "cf3666e8e1a822930c7f4c3f64309de5951b1c54")
    ],
    targets: [
        .target(
            name: "ScratchpadNative",
            dependencies: [.product(name: "MenubarKit", package: "menubar-kit")]
        ),
        .executableTarget(
            name: "ScratchpadApp",
            dependencies: [
                "ScratchpadNative",
                .product(name: "MenubarKit", package: "menubar-kit"),
            ]
        ),
        .executableTarget(name: "ScratchpadKeychain", dependencies: ["ScratchpadNative"]),
        .testTarget(
            name: "ScratchpadNativeTests",
            dependencies: [
                "ScratchpadNative", "ScratchpadApp", .product(name: "MenubarKit", package: "menubar-kit"),
            ]
        ),
    ]
)
