import UIKit
import React
import React_RCTAppDelegate
import ReactAppDependencyProvider
import GoogleCast

@main
class AppDelegate: UIResponder, UIApplicationDelegate {
  /// SEC-22: one allowlist (`PorticoURLRoutes`, shared with tvOS) for every URL handed to
  /// React Native, whether the app was running or not.
  func application(_ app: UIApplication, open url: URL,
                   options: [UIApplication.OpenURLOptionsKey: Any] = [:]) -> Bool {
    guard let route = PorticoURLRoutes.forwarded(url) else { return false }
    return RCTLinkingManager.application(app, open: route, options: options)
  }

  var window: UIWindow?

  var reactNativeDelegate: ReactNativeDelegate?
  var reactNativeFactory: RCTReactNativeFactory?

  /// The player's one orientation request, or the app's own set when none is active. The module
  /// and this answer must agree, because `requestGeometryUpdate` is only admitted when they do.
  func application(_ application: UIApplication,
                   supportedInterfaceOrientationsFor window: UIWindow?) -> UIInterfaceOrientationMask {
    PorticoFullWindowSupportedOrientations()
  }

  func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    // PERF-03: artwork responses revalidate (ETag) instead of re-downloading on every remount;
    // React Native's image loader uses the shared URL cache. tvOS may purge Caches; that's fine.
    URLCache.shared = URLCache(memoryCapacity: 32 << 20, diskCapacity: 256 << 20)

    // IOS-PLAYER-03: Google Cast, only when this build names Portico's receiver application
    // (PORTICO_CAST_APPLICATION_ID). Without one the app offers no Cast row at all.
    if let castApp = Bundle.main.object(forInfoDictionaryKey: "PorticoCastApplicationID") as? String,
       !castApp.isEmpty, !castApp.hasPrefix("$(") {
      let options = GCKCastOptions(discoveryCriteria: GCKDiscoveryCriteria(applicationID: castApp))
      options.physicalVolumeButtonsWillControlDeviceVolume = true
      options.stopReceiverApplicationWhenEndingSession = false
      GCKCastContext.setSharedInstanceWith(options)
    }

    // BETA06: the downloads owner is deliberately not started here. Starting it creates the
    // background session, and on a relaunch for download events Apple's contract is to hold the
    // completion handler first and create the session after (see
    // handleEventsForBackgroundURLSession below). A foreground launch starts it from the scene.

    let delegate = ReactNativeDelegate()
    let factory = RCTReactNativeFactory(delegate: delegate)
    delegate.dependencyProvider = RCTAppDependencyProvider()

    reactNativeDelegate = delegate
    reactNativeFactory = factory

    return true
  }

  // MARK: Background downloads

  /// A download finished while Portico was suspended or not running: iOS relaunches the app in the
  /// background (no scene connects, so React Native does not start) and waits for this handler to
  /// be called once the downloads session has delivered its events. The handler is held first and
  /// only then is the downloads owner (PorticoDownloads.m) started, which recreates the session
  /// under the same identifier, files what arrived and then calls `PorticoBackgroundEvents.finish`.
  func application(_ application: UIApplication, handleEventsForBackgroundURLSession identifier: String,
                   completionHandler: @escaping () -> Void) {
    PorticoBackgroundEvents.wake(identifier, completionHandler)
  }

  /// When the app launched (PorticoLink uses it to tell a cold-launch link from a later one).
  private var launchedAt = Date()

  // MARK: Universal links, Handoff and Spotlight (OS-08, D-MAIL-2)

  /// A universal link (web.getportico.tv/account…, /device, /join/…), a title handed off from
  /// another device, or a Spotlight result. Each becomes one allowlisted `portico://` route;
  /// anything else is refused (a universal link then opens in Safari).
  func application(_ application: UIApplication, continue userActivity: NSUserActivity,
                   restorationHandler: @escaping ([UIUserActivityRestoring]?) -> Void) -> Bool {
    guard let route = PorticoURLRoutes.route(activityType: userActivity.activityType,
                                             userInfo: userActivity.userInfo,
                                             webpageURL: userActivity.webpageURL) else {
      if userActivity.activityType == NSUserActivityTypeBrowsingWeb, let web = userActivity.webpageURL, web.scheme == "https" {
        UIApplication.shared.open(web, options: [:], completionHandler: nil)
        return true
      }
      return false
    }
    PorticoLink.open(route)
    return true
  }
  func connect(_ scene: UIWindowScene, options: UIScene.ConnectionOptions) -> UIWindow {
    let window = UIWindow(windowScene: scene)
    self.window = window
    // The one host the player may turn; a request for any other window is refused.
    PorticoFullWindowAdoptHostWindow(window)
    var launch: [UIApplication.LaunchOptionsKey: Any] = [:]
    if let url = options.urlContexts.first?.url, let route = PorticoURLRoutes.forwarded(url) {
      launch[.url] = route
    }
    launchedAt = Date()
    PorticoLink.launchedAt = launchedAt
    // Deliver cold universal links through getInitialURL as the same allowlisted route.
    if launch[.url] == nil, let activity = options.userActivities.first,
       let route = PorticoURLRoutes.route(activityType: activity.activityType,
                                          userInfo: activity.userInfo,
                                          webpageURL: activity.webpageURL) {
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

/// Holds the system's completion handler for the downloads session until that session has
/// delivered the events of the wake it belongs to, and calls each exactly once. Main queue only.
///
/// There is no matching by time. On a relaunch the session does not exist until `wake` has held
/// the handler, so no event, and no end of events, can come before it. For a session that already
/// exists (the app was suspended, or is in front), Apple's contract is that the end of events
/// follows the handler; an end of events with no handler held belongs to no wake and is not kept
/// for a later one.
@objc(PorticoBackgroundEvents) final class PorticoBackgroundEvents: NSObject {
  private static var completions: [String: () -> Void] = [:]

  /// A wake that needs nothing from the system's handler (a scene coming to the foreground):
  /// creates the downloads owner and its session if needed, and retries an unread manifest.
  static func startDownloads() {
    PorticoDownloadOwner.start()
  }

  /// The system's background-session callback. Holds the handler, then starts the owner.
  static func wake(_ identifier: String, _ completion: @escaping () -> Void) {
    dispatchPrecondition(condition: .onQueue(.main))
    // Portico creates no other background session, so nothing else would ever call this one.
    guard identifier == PorticoDownloadOwner.sessionIdentifier else { completion(); return }
    // A new wake for the same session: the system has moved on from the earlier one.
    completions.removeValue(forKey: identifier)?()
    completions[identifier] = completion
    PorticoDownloadOwner.start()
  }

  /// Called by the owner from its serial queue once every write queued before the session's
  /// end of events has finished.
  @objc static func finish(_ identifier: String) {
    DispatchQueue.main.async { completions.removeValue(forKey: identifier)?() }
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

  /// BETA06: an ordinary launch, or a return to the app, starts the downloads owner (a no-op
  /// after the first time, apart from retrying a manifest that could not be read yet).
  func sceneWillEnterForeground(_ scene: UIScene) {
    PorticoBackgroundEvents.startDownloads()
  }

  func scene(_ scene: UIScene, openURLContexts contexts: Set<UIOpenURLContext>) {
    guard let app = UIApplication.shared.delegate as? AppDelegate else { return }
    for context in contexts {
      _ = app.application(UIApplication.shared, open: context.url, options: [:])
    }
  }

  func scene(_ scene: UIScene, continue userActivity: NSUserActivity) {
    guard let app = UIApplication.shared.delegate as? AppDelegate else { return }
    _ = app.application(UIApplication.shared, continue: userActivity, restorationHandler: { _ in })
  }
}
