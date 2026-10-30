import Foundation

/// Which `portico://` URLs the iPhone, iPad and Apple TV apps will hand to React Native.
/// (Compiled into both targets; the iOS project references this file.)
///
/// Kept apart from the app delegate, and free of UIKit, so the grammar can be
/// compiled and exercised on its own — an allowlist is only worth as much as the
/// evidence that it accepts what it must and refuses everything else.
///
/// Forwarding is not authorisation. A route that passes here is still resolved
/// against the signed-in viewer by the JavaScript router before anything opens
/// or plays; this decides only what is well-formed enough to be worth routing.
public enum PorticoURLRoutes {
#if DEBUG
  private static let publicMeasurement = "portico://native-layout?specimen=signed-out-welcome"
  private static let refusedMeasurement = URL(string: "portico://native-layout")!
  private static func isMeasurementAttempt(_ url: URL) -> Bool {
    let host = URLComponents(url: url, resolvingAgainstBaseURL: false)?.host
    return host?.lowercased() == "native-layout" || url.absoluteString.range(
      of: #"^portico:(?:[/\\]|%2f|%5c)*(?:[^/?#]*@)?native-layout"#,
      options: [.regularExpression, .caseInsensitive]) != nil
  }
#endif

  /// Identifiers use the same shape the JavaScript router accepts, so a URL that
  /// gets through is one it will recognise rather than silently drop.
  static func safeIdentifier(_ value: String?, limit: Int) -> Bool {
    guard let value, !value.isEmpty, value.count <= limit else { return false }
    return value.range(of: "^[A-Za-z0-9_-]+$", options: .regularExpression) != nil
  }

  /// `portico://account-return?transactionId=…`, optionally `&code=…` (SEC-01: a current Portico
  /// Account's completion code, 43 characters) — the sign-in continuation.
  static func isAccountReturn(_ parts: URLComponents) -> Bool {
    guard parts.host == "account-return", parts.path.isEmpty,
          let items = parts.queryItems, items.count == 1 || items.count == 2,
          items[0].name == "transactionId" else { return false }
    if items.count == 2 {
      guard items[1].name == "code", let code = items[1].value, code.count == 43, safeIdentifier(code, limit: 43) else { return false }
    }
    return safeIdentifier(items[0].value, limit: 160)
  }

  /// `portico://item/{id}` and `portico://play/{id}` — the display and play
  /// actions the server publishes in the Top Shelf feed. A warm launch arrives
  /// through the app delegate; a cold launch goes through `launchOptions`, which
  /// is why only the warm path was failing.
  static func isContentRoute(_ parts: URLComponents) -> Bool {
    guard parts.host == "item" || parts.host == "play",
          parts.query == nil, parts.queryItems == nil,
          parts.path.hasPrefix("/") else { return false }
    let identifier = String(parts.path.dropFirst())
    guard !identifier.contains("/") else { return false }
    return safeIdentifier(identifier, limit: 128)
  }

  /// `portico://downloads` — where a "download finished" notification leads (iOS).
  static func isDownloads(_ parts: URLComponents) -> Bool {
    parts.host == "downloads" && (parts.path.isEmpty || parts.path == "/") && parts.query == nil
  }

  // Account, device, invitation and Watch Together routes follow the link contract
  // (`Project Architecture/Remediation/Spec — Link Contract.md`). Secrets travel only in the
  // fragment, never the query.

  static let accountTabs: Set<String> = ["profiles", "sessions", "security"]

  /// `portico://account` or `portico://account?tab=profiles|sessions|security` (the JS Account page has no tabs and ignores it).
  static func isAccount(_ parts: URLComponents) -> Bool {
    guard parts.host == "account", parts.path.isEmpty || parts.path == "/", parts.fragment == nil else { return false }
    guard let items = parts.queryItems else { return parts.query == nil }
    return items.count == 1 && items[0].name == "tab" && accountTabs.contains(items[0].value ?? "")
  }

  /// `portico://device` — approve a TV's sign-in (the code is typed on the approval screen).
  static func isDevice(_ parts: URLComponents) -> Bool {
    parts.host == "device" && (parts.path.isEmpty || parts.path == "/") && parts.query == nil && parts.fragment == nil
  }

  /// `portico://together/join/{code}` — a Watch Together invite.
  static func isTogetherJoin(_ parts: URLComponents) -> Bool {
    guard parts.host == "together", parts.query == nil, parts.fragment == nil, parts.path.hasPrefix("/join/") else { return false }
    return safeCode(String(parts.path.dropFirst("/join/".count)))
  }

  /// `portico://invite#invite={token}` — a server or account invitation; the token only in the fragment.
  static func isInvite(_ parts: URLComponents) -> Bool {
    guard parts.host == "invite", parts.path.isEmpty || parts.path == "/", parts.query == nil,
          let token = fragmentValue(parts.fragment, "invite"), fragmentPairs(parts.fragment).count == 1 else { return false }
    return safeToken(token)
  }

  /// `portico://join#code={code}&server={origin}` — a server's own invitation (Spec — Hosted at
  /// Scale): the one-time code and the inviting server's address, only in the fragment.
  static func isJoin(_ parts: URLComponents) -> Bool {
    guard parts.host == "join", parts.path.isEmpty || parts.path == "/", parts.query == nil,
          fragmentPairs(parts.fragment).count == 2,
          let code = fragmentValue(parts.fragment, "code"), let server = fragmentValue(parts.fragment, "server") else { return false }
    return safeInvitationCode(code) && safeServerOrigin(server)
  }
  /// The server's invitation codes: 32–128 URL-safe characters.
  static func safeInvitationCode(_ value: String?) -> Bool {
    guard let value, (32...128).contains(value.count) else { return false }
    return value.range(of: "^[A-Za-z0-9_-]+$", options: .regularExpression) != nil
  }
  /// An http(s) origin: a host (and port), no credentials, path, query or fragment.
  static func safeServerOrigin(_ value: String?) -> Bool {
    guard let value, (8...512).contains(value.count), let parts = URLComponents(string: value),
          parts.scheme == "https" || parts.scheme == "http", let host = parts.host, !host.isEmpty,
          parts.user == nil, parts.password == nil, parts.path.isEmpty || parts.path == "/",
          parts.query == nil, parts.fragment == nil else { return false }
    return true
  }

  static func safeCode(_ value: String?) -> Bool {
    guard let value, (4...16).contains(value.count) else { return false }
    return value.range(of: "^[A-Za-z0-9-]+$", options: .regularExpression) != nil
  }
  /// Any length the app can hold: a short or damaged invitation still opens the app, which then
  /// says the link isn't complete (QA) rather than the tap silently doing nothing.
  static func safeToken(_ value: String?) -> Bool {
    guard let value, (1...512).contains(value.count) else { return false }
    return value.range(of: "^[A-Za-z0-9_.~-]+$", options: .regularExpression) != nil
  }
  /// `a=1&b=2` fragments, as the web links write them.
  static func fragmentPairs(_ fragment: String?) -> [(String, String)] {
    guard let fragment, !fragment.isEmpty else { return [] }
    return fragment.split(separator: "&").map { pair in
      // Keep empty pieces: "=" is ("", ""), and a malformed link must never crash the app.
      let kv = pair.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false).map(String.init)
      return (kv.first ?? "", kv.count > 1 ? (kv[1].removingPercentEncoding ?? "") : "")
    }
  }
  static func fragmentValue(_ fragment: String?, _ name: String) -> String? {
    fragmentPairs(fragment).first(where: { $0.0 == name })?.1
  }

  /// O-162 shared links (client-core `server-links.ts`, frozen): `portico://shared/{serverId}/{segment}…`
  /// with an optional short query. 1–6 encoded segments (the server alone is its Home); an encoded
  /// slash, `?`, `#` or control character, a dot segment, or a fragment never passes.
  static let sharedForbidden = "%(2[fF]|3[fF]|23|[01][0-9a-fA-F]|7[fF])"
  static func sharedSegments(_ path: String) -> [String]? {
    guard path.hasPrefix("/") else { return nil }
    let segments = path.dropFirst().split(separator: "/", omittingEmptySubsequences: false).map(String.init)
    guard (1...6).contains(segments.count), segments.allSatisfy({ segment in
      segment != "." && segment != ".." && segment.range(of: "^[A-Za-z0-9._~%-]{1,512}$", options: .regularExpression) != nil
        && segment.range(of: sharedForbidden, options: .regularExpression) == nil
    }) else { return nil }
    return segments
  }
  static func sharedQuery(_ query: String?) -> Bool {
    guard let query else { return true }
    return query.range(of: "^[A-Za-z0-9._~%&=+-]{1,1024}$", options: .regularExpression) != nil && query.range(of: sharedForbidden, options: .regularExpression) == nil
  }
  static func isShared(_ parts: URLComponents) -> Bool {
    parts.host == "shared" && parts.fragment == nil && sharedQuery(parts.percentEncodedQuery) && sharedSegments(parts.percentEncodedPath) != nil
  }

  /// Hosts whose https links open the app (the `applinks:` associated domains).
  static let universalHosts: Set<String> = ["web.getportico.tv", "getportico.tv"]

  /// Translates something the system hands the app to continue — a universal link, a Handoff or
  /// Spotlight activity — into the one `portico://` route it means, or nil. Only routes the
  /// allowlist accepts come out.
  public static func route(activityType: String, userInfo: [AnyHashable: Any]?, webpageURL: URL?) -> URL? {
    var candidate: URL?
    switch activityType {
    case "NSUserActivityTypeBrowsingWeb":
      guard let web = webpageURL, web.scheme == "https", let host = web.host?.lowercased(), universalHosts.contains(host),
            let parts = URLComponents(url: web, resolvingAgainstBaseURL: false), parts.user == nil, parts.password == nil, parts.port == nil
      else { return nil }
      let path = parts.path.hasSuffix("/") && parts.path.count > 1 ? String(parts.path.dropLast()) : parts.path
      // O-162: only web.getportico.tv shares; the fragment is dropped, a short query is kept for the parser.
      // The encoded path: an encoded slash or anything outside the identifier set stays refused.
      let encoded = parts.percentEncodedPath.hasSuffix("/") && parts.percentEncodedPath.count > 1 ? String(parts.percentEncodedPath.dropLast()) : parts.percentEncodedPath
      if encoded == "/s" || encoded.hasPrefix("/s/") {
        let query = parts.percentEncodedQuery.flatMap { $0.isEmpty ? nil : $0 }
        guard host == "web.getportico.tv", let segments = sharedSegments(String(encoded.dropFirst(2))), sharedQuery(query),
              let shared = URL(string: "portico://shared/" + segments.joined(separator: "/") + (query.map { "?" + $0 } ?? "")), allows(shared) else { return nil }
        return shared
      }
      switch path {
      case "/account":
        // `event` and anything else in the query is for the web page only.
        let tab = parts.queryItems?.first(where: { $0.name == "tab" })?.value
        candidate = URL(string: tab.map { accountTabs.contains($0) ? "portico://account?tab=" + $0 : "portico://account" } ?? "portico://account")
      case "/device":
        candidate = URL(string: "portico://device")
      case "/join":
        if let code = fragmentValue(parts.fragment, "code"), let server = fragmentValue(parts.fragment, "server"),
           parts.query == nil, fragmentPairs(parts.fragment).count == 2, safeInvitationCode(code), safeServerOrigin(server),
           let encoded = server.addingPercentEncoding(withAllowedCharacters: .alphanumerics) {
          candidate = URL(string: "portico://join#code=" + code + "&server=" + encoded)
        } else if let token = fragmentValue(parts.fragment, "invite"), safeToken(token) {
          // An old Portico Account invitation: the app says to ask for a new one.
          candidate = URL(string: "portico://invite#invite=" + token)
        }
      case "/together/join":
        if let code = fragmentValue(parts.fragment, "code"), safeCode(code) { candidate = URL(string: "portico://together/join/" + code) }
      case "/app/account-return":
        // SEC-01: a provider sign-in's return (`#tx=…&code=…`); the sign-in screen checks it is
        // the one it started. Only the two values pass, and only into the query of the app route.
        if parts.query == nil, fragmentPairs(parts.fragment).count == 2,
           let tx = fragmentValue(parts.fragment, "tx"), safeIdentifier(tx, limit: 160),
           let code = fragmentValue(parts.fragment, "code"), code.count == 43, safeIdentifier(code, limit: 43) {
          candidate = URL(string: "portico://account-return?transactionId=" + tx + "&code=" + code)
        }
      default: break
      }
    case "tv.getportico.portico.title":
      if let id = userInfo?["itemId"] as? String { candidate = URL(string: "portico://item/" + id) }
    case "com.apple.corespotlightitem":
      // Spotlight results are indexed as "continueWatching:{id}" or "saved:{id}".
      if let unique = userInfo?["kCSSearchableItemActivityIdentifier"] as? String, let id = unique.split(separator: ":").last {
        candidate = URL(string: "portico://item/" + id)
      }
    default: break
    }
    guard let candidate, allows(candidate) else { return nil }
    return candidate
  }

  /// One forwarding decision for the delegates. A DEBUG measurement refusal must
  /// reach the strict JS classifier as a fixed public marker, never as a nil URL
  /// that would select ordinary auth restoration or carry arbitrary caller data.
  public static func forwarded(_ url: URL) -> URL? {
#if DEBUG
    if isMeasurementAttempt(url) { return allows(url) ? url : refusedMeasurement }
#endif
    return allows(url) ? url : nil
  }

  /// The single decision the app delegate makes, for warm opens and cold launches alike
  /// (SEC-22: a cold launch's URL is filtered before React Native can read it).
  public static func allows(_ url: URL) -> Bool {
    guard let parts = URLComponents(url: url, resolvingAgainstBaseURL: false),
          parts.scheme == "portico",
          parts.user == nil, parts.password == nil, parts.port == nil
    else { return false }
#if DEBUG
    if url.absoluteString == publicMeasurement { return true }
#endif
    // Only an invitation carries a fragment (its token or code); every other route refuses one.
    if parts.fragment != nil { return isInvite(parts) || isJoin(parts) }
    return isAccountReturn(parts) || isContentRoute(parts) || isDownloads(parts) || isAccount(parts) || isDevice(parts) || isTogetherJoin(parts) || isShared(parts)
  }
}
