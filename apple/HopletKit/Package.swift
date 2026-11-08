// swift-tools-version: 6.0
import PackageDescription

let package = Package(
  name: "HopletKit",
  // macOS is here only so that `swift test` runs on a Mac.
  platforms: [.iOS("26.0"), .macOS("15.0")],
  products: [
    .library(name: "HopletProto", targets: ["HopletProto"]),
    .library(name: "HopletClient", targets: ["HopletClient"]),
  ],
  targets: [
    .target(name: "HopletProto"),
    .target(name: "HopletClient", dependencies: ["HopletProto"]),
    .testTarget(name: "HopletProtoTests", dependencies: ["HopletProto"]),
    .testTarget(name: "HopletClientTests", dependencies: ["HopletClient"]),
  ]
)
