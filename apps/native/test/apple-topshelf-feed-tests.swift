import Foundation

@main enum ShelfFeedTests {
  static func main() throws {
    var assertions = 0
    func check(_ value: @autoclosure () -> Bool) {
      assertions += 1
      precondition(value())
    }
    func entry(_ id: String, show: String? = nil) -> [String: Any] {
      var value: [String: Any] = ["id": id, "title": "Title \(id)", "displayUrl": "portico://item/\(id)", "kind": "episode"]
      if let show { value["showTitle"] = show }
      return value
    }
    func section(_ id: String, _ entries: [[String: Any]], code: String = "next.home.continueWatching", params: [String: String]? = nil) -> [String: Any] {
      var text: [String: Any] = ["code": code, "fallback": "Server \(id)"]
      if let params { text["params"] = params }
      return ["id": id, "title": "Server \(id)", "titleText": text, "shape": "poster", "entries": entries]
    }
    func decode(_ sections: [[String: Any]]) throws -> PorticoShelfFeed {
      try JSONDecoder().decode(PorticoShelfFeed.self, from: JSONSerialization.data(withJSONObject: ["sections": sections]))
    }
    let feed = try decode([
      section("continue", [entry("a", show: "Actual show"), entry("b")]),
      section("duplicate", [entry("a")]),
      section("recent", [entry("b"), entry("c")], code: "home.row.recentlyAddedIn", params: ["library": "Films"]),
      section("later", [entry("d")]),
    ])
    let rows = feed.selectedRows()
    check(rows.map { $0.section.id } == ["continue", "recent"])
    check(rows[0].entries.map(\.id) == ["a", "b"])
    check(rows[1].entries.map(\.id) == ["c"])
    check(rows[0].entries[0].tileTitle == "Actual show")
    check(rows[0].entries[1].tileTitle == "Title b")
    var lookups: [(String, [String: String])] = []
    let title = rows[0].section.localizedTitle { code, params in
      lookups.append((code, params)); return "Catalogue row"
    }
    check(title == "Catalogue row" && lookups[0].0 == "next.home.continueWatching" && lookups[0].1.isEmpty)
    let nextTitle = rows[1].section.localizedTitle { code, params in
      check(code == "home.row.recentlyAddedIn" && params == ["library": "Films"])
      return "Resolved library row"
    }
    check(nextTitle == "Resolved library row")
    check(rows[1].section.localizedTitle { _, _ in nil } == "Server recent")
    let unknown = try decode([section("unknown", [entry("e")], code: "unknown.server.code")]).selectedRows()
    check(unknown[0].section.localizedTitle { _, _ in nil } == "Server unknown")
    let empty = try decode([])
    check(empty.selectedRows().isEmpty)
    let limited = try decode([
      section("continue", (0..<15).map { entry("c\($0)") }),
      section("next", (0..<15).map { entry("n\($0)") }),
    ]).selectedRows()
    check(limited.map { $0.entries.count } == [10, 10])
    let noContinue = try decode([section("empty", []), section("first", [entry("f")]), section("second", [entry("g")])]).selectedRows()
    check(noContinue.map { $0.section.id } == ["first"])
    let suppliedEmpty = try decode([section("first", [entry("h", show: "")])]).selectedRows()
    check(suppliedEmpty[0].entries[0].tileTitle.isEmpty)
    print("{\"assertions\":\(assertions),\"failures\":0}")
  }
}
