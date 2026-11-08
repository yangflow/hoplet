// swift-tools-version: 6.0
import PackageDescription

let package = Package(
  name: "SimulatorHelper",
  // The helper drives the macOS iOS Simulator; it has no other platform.
  platforms: [.macOS("15.0")],
  products: [.executable(name: "hoplet-simulator-helper", targets: ["hoplet-simulator-helper"])],
  targets: [
    // Pure protocol and command logic. No private frameworks.
    .target(name: "SimulatorHelperCore"),
    .executableTarget(
      name: "hoplet-simulator-helper",
      dependencies: ["SimulatorHelperCore"]
    ),
    .testTarget(name: "SimulatorHelperCoreTests", dependencies: ["SimulatorHelperCore"]),
  ]
)
