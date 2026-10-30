import Foundation

// APP-08: the native URL allowlist.
//
// Compiled together with the real tvos/Portico/PorticoURLRoutes.swift —
// no copy of the grammar lives here. Run with:
//
//   swiftc -O -o "$OUT/url-routes" \
//     apps/native/tvos/Portico/PorticoURLRoutes.swift \
//     apps/native/test/porticourlroutes-tests.swift
//   "$OUT/url-routes"

@main
struct URLRouteTests {
  static var failures = 0

  static func expect(_ text: String, _ allowed: Bool, _ why: String) {
    guard let url = URL(string: text) else {
      if allowed { FileHandle.standardError.write("FAIL unparsable: \(text)\n".data(using: .utf8)!); failures += 1 }
      return
    }
    let actual = PorticoURLRoutes.allows(url)
    if actual != allowed {
      FileHandle.standardError.write("FAIL \(why): \(text) — expected \(allowed), got \(actual)\n".data(using: .utf8)!)
      failures += 1
    }
  }

  static func expectForwarded(_ text: String, _ wanted: String?, _ why: String) {
    guard let url = URL(string: text) else {
      FileHandle.standardError.write("FAIL unrepresentable forwarding vector: \(why)\n".data(using: .utf8)!)
      failures += 1
      return
    }
    let actual = PorticoURLRoutes.forwarded(url)?.absoluteString
    if actual != wanted {
      FileHandle.standardError.write("FAIL forwarding: \(why) — expected \(wanted ?? "nil"), got \(actual ?? "nil")\n".data(using: .utf8)!)
      failures += 1
    }
  }

  static func main() {
    // Malformed fragments are refused, never a crash (an empty key/value split).
    expect("portico://invite#=", false, "empty fragment pair")
    expect("portico://invite#&=&", false, "empty fragment pairs")
    expect("https://web.getportico.tv/join#=", false, "empty universal-link fragment pair")

    // The two actions the server publishes in the Top Shelf feed. These are the
    // warm-launch URLs that used to be refused before reaching React Native.
    expect("portico://item/abc123", true, "Top Shelf display action")
    expect("portico://play/abc123", true, "Top Shelf play action")
    expect("portico://item/ITEM_with-mixed_Case-09", true, "the identifier charset the router accepts")
    expect("portico://item/" + String(repeating: "a", count: 128), true, "an identifier at the length limit")

    // The sign-in continuation must keep working exactly as before.
    expect("portico://account-return?transactionId=tx_123", true, "account return")
    expect("portico://account-return?transactionId=tx_123&code=" + String(repeating: "C", count: 43), true, "account return with a completion code (SEC-01)")
    expect("portico://account-return?transactionId=tx_123&code=short", false, "account return with a malformed code")
    expect("portico://account-return?code=" + String(repeating: "C", count: 43) + "&transactionId=tx_123", false, "account return with the code first")

    // Everything else stays refused.
    expect("portico://item/", false, "an empty identifier")
    expect("portico://item", false, "no identifier at all")
    expect("portico://item/" + String(repeating: "a", count: 129), false, "an over-long identifier")
    expect("portico://item/abc/def", false, "an extra path component")
    expect("portico://item/abc?x=1", false, "an unexpected query")
    expect("portico://item/abc#frag", false, "a fragment")
    expect("portico://user:pass@item/abc", false, "embedded credentials")
    expect("portico://item:8080/abc", false, "a port")
    expect("portico://item/abc%2Fdef", false, "a percent-encoded separator")
    expect("portico://item/abc def", false, "a space in the identifier")
    expect("portico://item/../secret", false, "path traversal")
    expect("https://item/abc", false, "another scheme")
    expect("portico://open/abc", false, "an unknown host")
    expect("portico://account-return", false, "account return with no transaction")
    expect("portico://account-return?transactionId=tx&other=1", false, "account return with extra parameters")
    expect("portico://account-return?transactionId=", false, "account return with an empty transaction")
    expect("portico://account-return/extra?transactionId=tx", false, "account return with a path")

    // SEC-01: a provider sign-in's https return becomes the app's own return, only when well-formed.
    let code = String(repeating: "C", count: 43)
    func expectRoute(_ web: String, _ want: String?, _ why: String) {
      let got = PorticoURLRoutes.route(activityType: "NSUserActivityTypeBrowsingWeb", userInfo: nil, webpageURL: URL(string: web))?.absoluteString
      if got != want { FileHandle.standardError.write("FAIL \(why): \(web) -> \(got ?? "nil")\n".data(using: .utf8)!); failures += 1 }
    }
    expectRoute("https://web.getportico.tv/app/account-return#tx=tx_A&code=" + code, "portico://account-return?transactionId=tx_A&code=" + code, "https account return")
    expectRoute("https://web.getportico.tv/app/account-return#code=" + code + "&tx=tx_A", "portico://account-return?transactionId=tx_A&code=" + code, "https account return, either order")
    expectRoute("https://web.getportico.tv/app/account-return#tx=tx_A", nil, "https account return without a code")
    expectRoute("https://web.getportico.tv/app/account-return#tx=tx_A&code=" + code + "&x=1", nil, "https account return with extra values")
    expectRoute("https://web.getportico.tv/app/account-return?x=1#tx=tx_A&code=" + code, nil, "https account return with a query")
    expectRoute("https://evil.example.com/app/account-return#tx=tx_A&code=" + code, nil, "account return on another host")

    // O-162: shared links (client-core server-links.ts, frozen) open the app only for web.getportico.tv/s/….
    expectRoute("https://web.getportico.tv/s/srv_1/item/abc-123", "portico://shared/srv_1/item/abc-123", "a shared item link")
    expectRoute("https://web.getportico.tv/s/srv_1", "portico://shared/srv_1", "the server alone is its Home")
    expectRoute("https://web.getportico.tv/s/srv.1~x/library/lib_9/", "portico://shared/srv.1~x/library/lib_9", "the server id charset, trailing slash")
    expectRoute("https://WEB.getportico.tv/s/srv_1/person/p1", "portico://shared/srv_1/person/p1", "host case is ignored")
    expectRoute("https://web.getportico.tv/s/srv_1/search?q=slow%20horses", "portico://shared/srv_1/search?q=slow%20horses", "a destination's query is kept for the parser")
    expectRoute("https://web.getportico.tv/s/srv_1/item/abc#t=120", "portico://shared/srv_1/item/abc", "a fragment is dropped")
    expectRoute("https://web.getportico.tv/s/srv_1/item/caf%C3%A9", "portico://shared/srv_1/item/caf%C3%A9", "a percent-encoded id")
    expectRoute("https://getportico.tv/s/srv_1/item/abc", nil, "the marketing host is not claimed")
    expectRoute("https://evil.example.com/s/srv_1/item/abc", nil, "another host")
    expectRoute("https://web.getportico.tv/s", nil, "no server")
    expectRoute("https://web.getportico.tv/s/srv_1/item/a%2Fb", nil, "an encoded slash")
    expectRoute("https://web.getportico.tv/s/srv_1/item/a%3Fb", nil, "an encoded question mark")
    expectRoute("https://web.getportico.tv/s/srv_1/item/a%0Ab", nil, "an encoded control character")
    expectRoute("https://web.getportico.tv/s/srv_1//abc", nil, "an empty segment")
    expectRoute("https://web.getportico.tv/s/srv_1/../item", nil, "a dot segment")
    expectRoute("https://web.getportico.tv/s/srv_1/a/b/c/d/e/f", nil, "too many segments")
    expectRoute("https://web.getportico.tv/s/srv_1/item/abc?x=%0A", nil, "a control character in the query")
    expectRoute("https://user@web.getportico.tv/s/srv_1/item/abc", nil, "credentials")
    expectRoute("https://web.getportico.tv:8443/s/srv_1/item/abc", nil, "a port")
    expect("portico://shared/srv_1/item/abc", true, "the forwarded shared route")
    expect("portico://shared/srv_1", true, "the forwarded Home route")
    expect("portico://shared/srv_1/search?q=x", true, "a forwarded query")
    expect("portico://shared/srv_1/item/abc#x", false, "a shared route with a fragment")
    expect("portico://shared/../item/abc", false, "traversal in a shared route")

    // Spec — Hosted at Scale: a server's own invitation link opens the app's join screen.
    let invite = String(repeating: "I", count: 43)
    expectRoute("https://web.getportico.tv/join#code=" + invite + "&server=https%3A%2F%2Fdemo.getportico.tv", "portico://join#code=" + invite + "&server=https%3A%2F%2Fdemo%2Egetportico%2Etv", "https join link")
    expectRoute("https://web.getportico.tv/join#server=http%3A%2F%2F192.168.1.20%3A32500&code=" + invite, "portico://join#code=" + invite + "&server=http%3A%2F%2F192%2E168%2E1%2E20%3A32500", "https join link, either order, a LAN address")
    expectRoute("https://web.getportico.tv/join#code=short&server=https%3A%2F%2Fdemo.getportico.tv", nil, "a short invitation code")
    expectRoute("https://web.getportico.tv/join#code=" + invite + "&server=https%3A%2F%2Fuser%3Apw%40demo.getportico.tv", nil, "a server address with credentials")
    expectRoute("https://web.getportico.tv/join#code=" + invite + "&server=javascript%3Aalert(1)", nil, "a non-http server address")
    expectRoute("https://web.getportico.tv/join#code=" + invite + "&server=https%3A%2F%2Fdemo.getportico.tv%2Fpath", nil, "a server address with a path")
    expect("portico://join#code=" + invite + "&server=https%3A%2F%2Fdemo.getportico.tv", true, "the app's join route")
    expect("portico://join#code=" + invite, false, "a join route without the server")
    expect("portico://join?code=" + invite, false, "a join code in the query")

    let publicMeasurement = "portico://native-layout?specimen=signed-out-welcome"
    let refusedMeasurement = "portico://native-layout"
#if DEBUG
    let allowedPublic = true
    let publicForward: String? = publicMeasurement
    let refusedForward: String? = refusedMeasurement
#else
    let allowedPublic = false
    let publicForward: String? = nil
    let refusedForward: String? = nil
#endif
    expect(publicMeasurement, allowedPublic, "exact DEBUG public welcome only")
    expectForwarded(publicMeasurement, publicForward, "exact public welcome forwarding")
    let malformedMeasurement = [
      refusedMeasurement,
      "portico://native-layout?specimen=unknown",
      "portico://native-layout?specimen=",
      "portico://native-layout?specimen",
      publicMeasurement + "&specimen=signed-out-welcome",
      publicMeasurement + "&server=https://fixture.invalid",
      publicMeasurement + "&password=synthetic-value",
      publicMeasurement + "&runtime=injected",
      publicMeasurement + "&",
      publicMeasurement + "#",
      publicMeasurement + "#synthetic-value",
      "portico://native-layout/?specimen=signed-out-welcome",
      "portico://native-layout/path?specimen=signed-out-welcome",
      "portico://user:synthetic-value@native-layout?specimen=signed-out-welcome",
      "portico://native-layout:1?specimen=signed-out-welcome",
      "portico://native-layout?%73pecimen=signed-out-welcome",
      "portico://native-layout?specimen=signed-out-%77elcome",
      "portico://%6eative-layout?specimen=signed-out-welcome",
      "portico://NATIVE-LAYOUT?specimen=signed-out-welcome",
      "PORTICO://native-layout?specimen=signed-out-welcome",
      "portico://native-layout-bad?specimen=signed-out-welcome",
      "portico://native-layout%25zz?specimen=signed-out-welcome",
      "portico:native-layout?specimen=signed-out-welcome",
      "portico:/native-layout?specimen=signed-out-welcome",
      "portico:////native-layout?specimen=signed-out-welcome",
      "portico:%5Cnative-layout?specimen=signed-out-welcome",
      "https://native-layout?specimen=signed-out-welcome",
    ]
    for text in malformedMeasurement {
      expect(text, false, "malformed measurement is never admitted")
      expectForwarded(text, refusedForward, "DEBUG measurement refusal strips all caller data")
    }
    expectForwarded(refusedMeasurement, refusedForward, "refusal marker is idempotent")
    for text in ["portico://item/item-id", "portico://play/item-id", "portico://downloads", "portico://account?tab=security", "portico://device", "portico://together/join/ABCD"] {
      expectForwarded(text, text, "existing ordinary routes are unchanged")
    }
    for text in ["portico://unknown", "portico://item/native-layout/path", "https://fixture.invalid/native-layout"] {
      expectForwarded(text, nil, "ordinary refused URLs are not measurement requests")
    }

    if failures == 0 {
      print("PASS native URL allowlist: Top Shelf item/play forwarded, account-return preserved, traversal/credentials/port/query/fragment/overlong refused")
      exit(0)
    }
    exit(1)
  }
}
