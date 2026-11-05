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
        // First-party tray, panel and notification building blocks. menubar-kit has
        // no release tag yet (and SwiftPM cannot resolve its four-part CalVer tags),
        // so it is pinned to an exact commit of main. Name the tag here once one exists.
        .package(url: "https://github.com/alexcatdad/menubar-kit.git", revision: "7403cb02d14946d9d92f4d7b1dd441316745a622"),
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
