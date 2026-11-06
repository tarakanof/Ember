// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "EmberKit",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "EmberKit", targets: ["EmberKit"]),
    ],
    targets: [
        .target(name: "EmberKit", resources: [.process("Resources")]),
        .testTarget(name: "EmberKitTests", dependencies: ["EmberKit"], exclude: ["testdata"]),
    ]
)
