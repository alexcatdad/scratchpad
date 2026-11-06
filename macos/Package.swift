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
        // menubar-kit v2026.11.05.2 (SwiftPM cannot resolve four-part CalVer tags, so the tag's commit is pinned).
        .package(
            url: "https://github.com/alexcatdad/menubar-kit.git", revision: "ec879dcd4c33638c90f5f5be0a8cd21c5eb75665")
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
            dependencies: ["ScratchpadNative", .product(name: "MenubarKit", package: "menubar-kit")]
        ),
    ]
)
