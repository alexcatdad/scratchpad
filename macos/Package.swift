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
    targets: [
        .target(name: "ScratchpadNative"),
        .executableTarget(
            name: "ScratchpadApp",
            dependencies: [
                "ScratchpadNative",
            ]
        ),
        .executableTarget(name: "ScratchpadKeychain", dependencies: ["ScratchpadNative"]),
        .testTarget(name: "ScratchpadNativeTests", dependencies: ["ScratchpadNative"]),
    ]
)
