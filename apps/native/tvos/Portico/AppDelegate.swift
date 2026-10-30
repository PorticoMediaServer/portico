import UIKit
import React
import React_RCTAppDelegate
import ReactAppDependencyProvider

@main
class AppDelegate: UIResponder, UIApplicationDelegate {
  /// The allowlist lives in `PorticoURLRoutes` so its grammar can be compiled
  /// and tested without UIKit. DEBUG public measurement refusals use a fixed marker;
  /// ordinary refused URLs are never forwarded.
  func application(_ app: UIApplication, open url: URL,
                   options: [UIApplication.OpenURLOptionsKey: Any] = [:]) -> Bool {
    guard let route = PorticoURLRoutes.forwarded(url) else { return false }
    return RCTLinkingManager.application(app, open: route, options: options)
  }

  var window: UIWindow?

  var reactNativeDelegate: ReactNativeDelegate?
  var reactNativeFactory: RCTReactNativeFactory?

  func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    // PERF-03: artwork responses revalidate (ETag) instead of re-downloading on every remount;
    // React Native's image loader uses the shared URL cache. tvOS may purge Caches; that's fine.
    URLCache.shared = URLCache(memoryCapacity: 32 << 20, diskCapacity: 128 << 20)

    let delegate = ReactNativeDelegate()
    let factory = RCTReactNativeFactory(delegate: delegate)
    delegate.dependencyProvider = RCTAppDependencyProvider()

    reactNativeDelegate = delegate
    reactNativeFactory = factory

    return true
  }
  func connect(_ scene: UIWindowScene, options: UIScene.ConnectionOptions) -> UIWindow {
    let window = UIWindow(windowScene: scene)
    self.window = window
    var launch: [UIApplication.LaunchOptionsKey: Any] = [:]
    if let url = options.urlContexts.first?.url, let route = PorticoURLRoutes.forwarded(url) {
      launch[.url] = route
    }
    reactNativeFactory?.startReactNative(withModuleName: "Portico", in: window, launchOptions: launch)
    // The launch screen stays over React until Boot draws its first frame (PorticoBootCover.h).
    PorticoBootCoverInstall(window)
    return window
  }

}

class ReactNativeDelegate: RCTDefaultReactNativeFactoryDelegate {
  override func sourceURL(for bridge: RCTBridge) -> URL? {
    self.bundleURL()
  }

  override func bundleURL() -> URL? {
#if DEBUG
    if let path = ProcessInfo.processInfo.environment["PORTICO_TEST_BUNDLE_PATH"], !path.isEmpty { return URL(fileURLWithPath: path) }
    if let packaged = Bundle.main.url(forResource: "main", withExtension: "jsbundle") { return packaged }
    RCTBundleURLProvider.sharedSettings().jsLocation = "127.0.0.1:19424"
    return RCTBundleURLProvider.sharedSettings().jsBundleURL(forBundleRoot: "index")
#else
    return Bundle.main.url(forResource: "main", withExtension: "jsbundle")
#endif
  }
}

/// A single scene owns the app's window; app-wide playback/download owners stay in the app delegate.
class SceneDelegate: UIResponder, UIWindowSceneDelegate {
  var window: UIWindow?

  func scene(_ scene: UIScene, willConnectTo session: UISceneSession,
             options connectionOptions: UIScene.ConnectionOptions) {
    guard let scene = scene as? UIWindowScene,
          let app = UIApplication.shared.delegate as? AppDelegate else { return }
    window = app.connect(scene, options: connectionOptions)
  }

  func scene(_ scene: UIScene, openURLContexts contexts: Set<UIOpenURLContext>) {
    guard let app = UIApplication.shared.delegate as? AppDelegate else { return }
    for context in contexts {
      _ = app.application(UIApplication.shared, open: context.url, options: [:])
    }
  }
}
