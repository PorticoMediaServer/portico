import AppIntents
import Foundation
import React
import UIKit

/// Opens a `portico://` route inside the running app, through the allowlist. Shared by the app
/// delegate (notifications, universal links, Handoff, Spotlight) and the App Intents.
enum PorticoLink {
  /// Set by the app delegate at launch; a route arriving in the first seconds waits for
  /// JavaScript to start listening.
  static var launchedAt = Date()

  @MainActor static func open(_ url: URL) {
    guard PorticoURLRoutes.allows(url) else { return }
    let delay: TimeInterval = Date().timeIntervalSince(launchedAt) < 5 ? 2 : 0
    DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
      _ = RCTLinkingManager.application(UIApplication.shared, open: url, options: [:])
    }
  }
}

/// The titles Siri can offer: Continue Watching and Saved, written by `PorticoActivity.indexTitles`
/// to the app's own Application Support (never shared outside the app).
enum PorticoSiriTitles {
  struct Title: Codable { let itemId: String; let title: String; let subtitle: String? }
  struct File: Codable { var continueWatching: [Title] = []; var saved: [Title] = [] }

  static func load() -> File {
    guard let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first,
          let data = try? Data(contentsOf: support.appendingPathComponent("PorticoSiri/titles.json")),
          let file = try? JSONDecoder().decode(File.self, from: data) else { return File() }
    return file
  }
  static func all() -> [Title] {
    let file = load(); var seen = Set<String>()
    return (file.continueWatching + file.saved).filter { seen.insert($0.itemId).inserted }
  }
}

/// Lets the Objective-C Spotlight module tell Siri the title list changed.
@objc(PorticoIntentsBridge) final class PorticoIntentsBridge: NSObject {
  @objc static func titlesChanged() {
    if #available(iOS 16.0, *) { PorticoShortcuts.updateAppShortcutParameters() }
  }
}

@available(iOS 16.0, *)
struct PorticoTitleEntity: AppEntity {
  static var typeDisplayRepresentation: TypeDisplayRepresentation = "Title"
  static var defaultQuery = PorticoTitleQuery()
  var id: String
  var title: String
  var subtitle: String?
  var displayRepresentation: DisplayRepresentation {
    if let subtitle, !subtitle.isEmpty { return DisplayRepresentation(title: "\(title)", subtitle: "\(subtitle)") }
    return DisplayRepresentation(title: "\(title)")
  }
  init(_ t: PorticoSiriTitles.Title) { id = t.itemId; title = t.title; subtitle = t.subtitle }
}

@available(iOS 16.0, *)
struct PorticoTitleQuery: EntityStringQuery {
  func entities(for identifiers: [String]) async throws -> [PorticoTitleEntity] {
    PorticoSiriTitles.all().filter { identifiers.contains($0.itemId) }.map(PorticoTitleEntity.init)
  }
  func entities(matching string: String) async throws -> [PorticoTitleEntity] {
    let needle = string.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current)
    return PorticoSiriTitles.all().filter {
      $0.title.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current).contains(needle)
    }.map(PorticoTitleEntity.init)
  }
  func suggestedEntities() async throws -> [PorticoTitleEntity] { PorticoSiriTitles.all().map(PorticoTitleEntity.init) }
}

/// "Play The Bear in Portico".
@available(iOS 16.0, *)
struct PlayTitleIntent: AppIntent {
  static var title: LocalizedStringResource = "Play a title"
  static var description = IntentDescription("Plays something from Continue Watching or My List.")
  static var openAppWhenRun = true
  @Parameter(title: "Title", requestValueDialog: IntentDialog("Which title?")) var title: PorticoTitleEntity

  @MainActor func perform() async throws -> some IntentResult {
    if let url = URL(string: "portico://play/" + title.id) { PorticoLink.open(url) }
    return .result()
  }
}

/// "Resume Portico": plays the first title in Continue Watching.
@available(iOS 16.0, *)
struct ResumeWatchingIntent: AppIntent {
  static var title: LocalizedStringResource = "Resume watching"
  static var description = IntentDescription("Picks up where you left off.")
  static var openAppWhenRun = true

  @MainActor func perform() async throws -> some IntentResult {
    if let next = PorticoSiriTitles.load().continueWatching.first, let url = URL(string: "portico://play/" + next.itemId) { PorticoLink.open(url) }
    return .result()
  }
}

@available(iOS 16.0, *)
struct PorticoShortcuts: AppShortcutsProvider {
  static var appShortcuts: [AppShortcut] {
    AppShortcut(intent: ResumeWatchingIntent(), phrases: [
      "Resume \(.applicationName)",
      "Continue watching in \(.applicationName)",
    ])
    AppShortcut(intent: PlayTitleIntent(), phrases: [
      "Play \(\.$title) in \(.applicationName)",
      "Watch \(\.$title) in \(.applicationName)",
    ])
  }
}
