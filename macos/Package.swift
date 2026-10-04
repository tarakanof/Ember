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
        // testdata/knob: the knob protocol vectors, read by path; keep in
        // sync with cinder's firmware/test/vectors.
        .testTarget(name: "EmberKitTests", dependencies: ["EmberKit"], exclude: ["testdata"]),
    ]
)
