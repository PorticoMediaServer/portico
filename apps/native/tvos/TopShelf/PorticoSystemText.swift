import Foundation

/// Home row titles from the app's chosen catalogue, bundled in the generated PorticoSystem table.
/// Unknown codes and missing arguments return nil so the caller can use the server's wording.
enum PorticoSystemText {
  private static let argument = try! NSRegularExpression(pattern: "\\{([A-Za-z0-9_]+)\\}")

  static func text(code: String, params: [String: String]) -> String? {
    let missing = "\u{0}"
    for bundle in bundles() {
      let raw = bundle.localizedString(forKey: code, value: missing, table: "PorticoSystem")
      if raw == missing { continue }
      let matches = argument.matches(in: raw, range: NSRange(raw.startIndex..., in: raw))
      let source = raw as NSString
      guard matches.allSatisfy({ params[source.substring(with: $0.range(at: 1))] != nil }) else { return nil }
      let result = NSMutableString(string: raw)
      // Replace original ranges once: catalogue-like braces in an argument remain plain text.
      for match in matches.reversed() {
        let name = source.substring(with: match.range(at: 1))
        result.replaceCharacters(in: match.range, with: params[name]!)
      }
      return result as String
    }
    return nil
  }

  private static func bundles() -> [Bundle] {
    let tag = UserDefaults(suiteName: ContentProvider.group)?.string(forKey: "topshelf.locale") ?? ""
    let names = [tag == "en-US" ? "en" : tag, "en"].filter { !$0.isEmpty }
    var seen = Set<String>()
    return names.filter { seen.insert($0).inserted }
      .compactMap { Bundle.main.path(forResource: $0, ofType: "lproj").flatMap(Bundle.init(path:)) } + [Bundle.main]
  }
}
