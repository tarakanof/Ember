// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "AwtrixMenuKit",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "AwtrixMenuKit", targets: ["AwtrixMenuKit"]),
    ],
    targets: [
        .target(name: "AwtrixMenuKit"),
        .testTarget(name: "AwtrixMenuKitTests", dependencies: ["AwtrixMenuKit"]),
    ]
)
