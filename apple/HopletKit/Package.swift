// swift-tools-version: 6.0
import PackageDescription

let package = Package(
  name: "HopletKit",
  // macOS is here only so that `swift test` runs on a Mac.
  platforms: [.iOS("26.0"), .macOS("15.0")],
  products: [.library(name: "HopletProto", targets: ["HopletProto"])],
  targets: [
    .target(name: "HopletProto"),
    .testTarget(name: "HopletProtoTests", dependencies: ["HopletProto"]),
  ]
)
