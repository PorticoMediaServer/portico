// swift-tools-version:5.9
import PackageDescription
let libraries = ["Libporticosubtitle", "Libass", "Libfreetype", "Libharfbuzz", "Libfribidi", "Libunibreak", "Libavcodec", "Libavformat", "Libavutil", "Libswresample"]
let package = Package(name: "PorticoSubtitleEngine", platforms: [.iOS("15.1"), .tvOS("15.1")], products: [.library(name: "PorticoSubtitleEngine", targets: libraries)], targets: libraries.map { .binaryTarget(name: $0, path: "Frameworks/\($0).xcframework") })
