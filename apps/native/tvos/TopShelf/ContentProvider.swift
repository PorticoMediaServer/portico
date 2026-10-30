import TVServices
import Foundation
import CryptoKit
import Security
import ImageIO
import UniformTypeIdentifiers
import os

/// A bounded, cookie-free shelf read. The session owns its delegate until completion.
private final class PorticoShelfRead: NSObject, URLSessionDataDelegate {
  private let limit: Int
  private var body = Data()
  private var response: URLResponse?
  private var session: URLSession?
  private var task: URLSessionDataTask?
  private var completion: ((Data?, URLResponse?, Error?) -> Void)?

  private init(limit: Int, completion: @escaping (Data?, URLResponse?, Error?) -> Void) {
    self.limit = limit; self.completion = completion; super.init()
  }
  static func fetch(_ request: URLRequest, limit: Int, completion: @escaping (Data?, URLResponse?, Error?) -> Void) {
    let reader = PorticoShelfRead(limit: limit, completion: completion)
    let config = URLSessionConfiguration.ephemeral
    config.urlCache = nil; config.httpCookieStorage = nil; config.httpShouldSetCookies = false
    config.urlCredentialStorage = nil; config.timeoutIntervalForResource = 20
    let queue = OperationQueue(); queue.maxConcurrentOperationCount = 1
    let session = URLSession(configuration: config, delegate: reader, delegateQueue: queue)
    reader.session = session; reader.task = session.dataTask(with: request); reader.task?.resume()
  }
  /// The owner's own certificate: the app pins it for an address once the server has proved its
  /// identity over it (PorticoServerTrust, in the app group's defaults); the shelf honours the
  /// same pins and nothing else beyond the system's trust.
  func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                  completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
    let space = challenge.protectionSpace
    guard space.authenticationMethod == NSURLAuthenticationMethodServerTrust, let trust = space.serverTrust else {
      completionHandler(.performDefaultHandling, nil); return
    }
    if SecTrustEvaluateWithError(trust, nil) { completionHandler(.performDefaultHandling, nil); return }
    var host = space.host.lowercased()
    if host.hasPrefix("[") && host.hasSuffix("]") { host = String(host.dropFirst().dropLast()) }
    let pins = UserDefaults(suiteName: ContentProvider.group)?.dictionary(forKey: "portico.certificates.v1")
    guard let line = pins?["\(host):\(space.port)"] as? String, let pinned = line.split(separator: " ").first,
          let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else {
      completionHandler(.performDefaultHandling, nil); return
    }
    let digest = Data(SHA256.hash(data: SecCertificateCopyData(leaf) as Data)).base64EncodedString()
      .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    if digest == pinned { completionHandler(.useCredential, URLCredential(trust: trust)) } else { completionHandler(.performDefaultHandling, nil) }
  }
  func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                  newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
    completionHandler(nil)
  }
  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
                  completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
    self.response = response
    completionHandler(response.expectedContentLength > Int64(limit) ? .cancel : .allow)
  }
  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    guard data.count <= limit - body.count else { dataTask.cancel(); return }
    body.append(data)
  }
  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    let done = completion; completion = nil
    self.task = nil; self.session = nil; session.finishTasksAndInvalidate()
    done?(error == nil ? body : nil, response, error)
  }
}

/// The Portico shelf on the Apple TV home screen.
///
/// Two sections, posters only (the card-shape rule): **Continue Watching** (up to 10, with
/// progress, Resume plays) and the viewer's next Home row under its own name ("Recently added",
/// "Watchlist", whatever their Home puts next; up to 10, never repeating Continue Watching). A
/// section with nothing in it is left out.
///
/// The system wakes this when nobody is using the app, so it holds no sign-in. The app leaves it
/// the server's address (shared defaults) and a token that reads one small feed and its posters
/// (the shared keychain, SEC-19). Posters are fetched here with that token in a header, shrunk to
/// 400 px wide and kept in the shared cache, so the home screen loads small local files and no
/// token ever appears in an image URL. If the server doesn't answer, the last good feed and
/// posters are shown; with no cached feed the shelf is simply empty.
final class ContentProvider: TVTopShelfContentProvider {
  static let group = "group.tv.getportico.portico"
  private static let log = Logger(subsystem: "tv.getportico.portico.topshelf", category: "feed")
  private static let posterWidth = 400
  private typealias Feed = PorticoShelfFeed

  /// Reads the feed token the app stored in the keychain access group it shares with this
  /// extension (the app group identifier doubles as a keychain access group).
  static func token() -> String? {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: "tv.getportico.topshelf",
      kSecAttrAccount as String: "feed",
      kSecAttrAccessGroup as String: group,
      kSecReturnData as String: true,
      kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var result: AnyObject?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    if status == errSecSuccess, let data = result as? Data, let value = String(data: data, encoding: .utf8), !value.isEmpty { return value }
    log.error("keychain read status \(status, privacy: .public)")
    // Builds before SEC-19 kept the token in shared defaults; the app moves it on next launch.
    return UserDefaults(suiteName: group)?.string(forKey: "topshelf.token")
  }

  private static func cacheDirectory(origin: URL, owner: String) -> URL? {
    // A response from a retired profile may finish after the app cleared its cache. It must
    // never write into the new profile's folder, even if that happens between a check and write.
    let identity = Data((origin.absoluteString + "\u{0}" + owner).utf8)
    let key = SHA256.hash(data: identity).map { String(format: "%02x", $0) }.joined()
    return FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)?
      .appendingPathComponent("Library/Caches/topshelf", isDirectory: true).appendingPathComponent(key, isDirectory: true)
  }
  private static func posterURL(_ id: String, cache: URL?) -> URL? {
    guard id.range(of: "^[A-Za-z0-9_-]{1,128}$", options: .regularExpression) != nil else { return nil }
    return cache?.appendingPathComponent("art", isDirectory: true).appendingPathComponent(id + ".jpg")
  }

  override func loadTopShelfContent(completionHandler: @escaping (TVTopShelfContent?) -> Void) {
    let log = Self.log
    let defaults = UserDefaults(suiteName: Self.group)
    guard let base = defaults?.string(forKey: "topshelf.baseUrl"), let origin = URL(string: base),
          origin.scheme == "https" || origin.scheme == "http",
          !(origin.host ?? "").isEmpty, origin.user == nil, origin.password == nil, origin.query == nil, origin.fragment == nil,
          origin.path.isEmpty || origin.path == "/" else {
      log.error("no server configured; shelf empty")
      completionHandler(nil); return
    }
    let owner = defaults?.string(forKey: "topshelf.owner") ?? ""
    let cache = Self.cacheDirectory(origin: origin, owner: owner)
    let feedURL = cache?.appendingPathComponent("feed.json")
    guard let token = Self.token() else {
      log.error("no feed token in the shared keychain or defaults; shelf empty")
      completionHandler(nil); return
    }
    guard let url = URL(string: "/v1/topshelf", relativeTo: origin) else { completionHandler(nil); return }
    var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 10)
    request.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    PorticoShelfRead.fetch(request, limit: 1024 * 1024) { data, response, error in
      guard Self.isCurrent(origin: origin, owner: owner) else { completionHandler(nil); return }
      let status = (response as? HTTPURLResponse)?.statusCode ?? 0
      var body = data
      if let error { log.error("feed request failed: \(error.localizedDescription, privacy: .public)") }
      if status != 200 {
        log.error("feed answered \(status, privacy: .public); using the last feed if there is one")
        body = feedURL.flatMap { try? Data(contentsOf: $0) }
        // An expired or revoked token must not keep showing someone's history.
        if status == 401 || status == 403 { Self.clearCache(cache); body = nil }
      }
      guard let body else { completionHandler(nil); return }
      let feed: Feed
      do { feed = try JSONDecoder().decode(Feed.self, from: body) } catch {
        log.error("feed could not be read: \(String(describing: error), privacy: .public)")
        completionHandler(nil); return
      }
      if status == 200, let file = feedURL {
        try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? body.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
      }
      let rows = feed.selectedRows()
      // Posters: whatever is cached is used now; missing ones are fetched for up to ~6 s, and any
      // that arrive later refresh the shelf on its next load.
      let missing = rows.flatMap(\.entries).filter { entry in
        guard entry.imageUrl != nil, let file = Self.posterURL(entry.id, cache: cache) else { return false }
        return !FileManager.default.fileExists(atPath: file.path)
      }
      Self.fetchPosters(missing, origin: origin, owner: owner, token: token, cache: cache, budget: 6) {
        guard Self.isCurrent(origin: origin, owner: owner) else { completionHandler(nil); return }
        let content = Self.content(rows: rows, cache: cache)
        log.notice("feed drawn: \(rows.count, privacy: .public) rows")
        completionHandler(content)
      }
    }
  }

  private static func isCurrent(origin: URL, owner: String) -> Bool {
    let defaults = UserDefaults(suiteName: group)
    return defaults?.string(forKey: "topshelf.baseUrl") == origin.absoluteString && (defaults?.string(forKey: "topshelf.owner") ?? "") == owner
  }

  private static func fetchPosters(_ entries: [Feed.Entry], origin: URL, owner: String, token: String, cache: URL?, budget: TimeInterval, done: @escaping () -> Void) {
    guard !entries.isEmpty else { done(); return }
    let group = DispatchGroup(), gate = DispatchSemaphore(value: 4)
    let queue = DispatchQueue(label: "tv.getportico.topshelf.art", attributes: .concurrent)
    var any = false; let lock = NSLock()
    for entry in entries {
      // The server's poster URL is used as given: its query carries the artwork grant the art
      // route resolves (and the width), so nothing in it may be replaced.
      guard let path = entry.imageUrl, let parts = URLComponents(string: path) else { continue }
      guard let url = parts.url(relativeTo: origin)?.absoluteURL, url.scheme == origin.scheme, url.host?.lowercased() == origin.host?.lowercased(), url.port == origin.port,
            url.user == nil, url.password == nil, url.fragment == nil, let file = posterURL(entry.id, cache: cache) else { continue }
      group.enter()
      queue.async {
        gate.wait()
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 15)
        request.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        request.setValue("image/*", forHTTPHeaderField: "Accept")
        PorticoShelfRead.fetch(request, limit: 8 << 20) { data, response, error in
          defer { gate.signal(); group.leave() }
          guard Self.isCurrent(origin: origin, owner: owner), (response as? HTTPURLResponse)?.statusCode == 200, let data, data.count < 8 << 20, let small = shrink(data) else {
            log.error("poster \(entry.id, privacy: .public) answered \((response as? HTTPURLResponse)?.statusCode ?? 0, privacy: .public) \(error?.localizedDescription ?? "", privacy: .public)")
            return
          }
          try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
          if (try? small.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])) != nil { lock.lock(); any = true; lock.unlock() }
        }
      }
    }
    // Draw with what's there after the budget; posters that land later refresh the shelf.
    var finished = false
    let finish = { lock.lock(); let first = !finished; finished = true; lock.unlock(); if first { done() } }
    DispatchQueue.global().asyncAfter(deadline: .now() + budget, execute: finish)
    group.notify(queue: .global()) {
      lock.lock(); let late = finished && any; lock.unlock()
      finish()
      if late { TVTopShelfContentProvider.topShelfContentDidChange() }
    }
  }

  /// A 400 px wide JPEG: small enough to load instantly, sharp enough for a poster tile.
  private static func shrink(_ data: Data) -> Data? {
    guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
          let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [kCGImageSourceCreateThumbnailFromImageAlways: true, kCGImageSourceThumbnailMaxPixelSize: posterWidth * 3 / 2, kCGImageSourceCreateThumbnailWithTransform: true] as CFDictionary)
    else { return nil }
    let out = NSMutableData()
    guard let destination = CGImageDestinationCreateWithData(out, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
    CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: 0.85] as CFDictionary)
    return CGImageDestinationFinalize(destination) ? out as Data : nil
  }

  private static func clearCache(_ cache: URL?) {
    if let cache = cache { try? FileManager.default.removeItem(at: cache) }
  }

  private static func item(_ entry: Feed.Entry, section: String, cache: URL?) -> TVTopShelfSectionedItem? {
    guard let display = URL(string: entry.displayUrl), display.scheme == "portico" else { return nil }
    let item = TVTopShelfSectionedItem(identifier: section + "/" + entry.id)
    item.title = entry.tileTitle
    item.imageShape = .poster
    // A tile without a poster would show the system's loading dots; leave it out until its
    // poster has arrived (the shelf refreshes when it does).
    guard let file = posterURL(entry.id, cache: cache), FileManager.default.fileExists(atPath: file.path) else { return nil }
    item.setImageURL(file, for: [.screenScale1x, .screenScale2x])
    if let percent = entry.progressPercent, percent > 0 { item.playbackProgress = min(1, Double(percent) / 100) }
    item.displayAction = TVTopShelfAction(url: display)
    if let play = entry.playUrl.flatMap({ URL(string: $0) }), play.scheme == "portico" { item.playAction = TVTopShelfAction(url: play) }
    return item
  }

  private static func content(rows: [Feed.Row], cache: URL?) -> TVTopShelfContent? {
    var sections: [TVTopShelfItemCollection<TVTopShelfSectionedItem>] = []
    for row in rows {
      let items = row.entries.compactMap { item($0, section: row.section.id, cache: cache) }
      guard !items.isEmpty else { continue }   // an empty section is left out, never shown as dots
      let collection = TVTopShelfItemCollection(items: items)
      collection.title = row.section.localizedTitle { PorticoSystemText.text(code: $0, params: $1) }
      sections.append(collection)
    }
    return sections.isEmpty ? nil : TVTopShelfSectionedContent(sections: sections)
  }
}
