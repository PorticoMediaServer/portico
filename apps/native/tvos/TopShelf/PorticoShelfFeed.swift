import Foundation

/// Only supplied Home facts become system-surface text. No title or episode-label inference.
struct PorticoShelfFeed: Decodable {
  struct Entry: Decodable {
    let id: String
    let title: String
    let showTitle: String?
    let imageUrl: String?
    let displayUrl: String
    let playUrl: String?
    let progressPercent: Int?

    var tileTitle: String { showTitle ?? title }
  }
  struct TitleText: Decodable {
    let code: String
    let params: [String: String]?
  }
  struct Section: Decodable {
    let id: String
    let title: String
    let titleText: TitleText
    let shape: String?
    let entries: [Entry]

    func localizedTitle(_ lookup: (String, [String: String]) -> String?) -> String {
      lookup(titleText.code, titleText.params ?? [:]) ?? title
    }
  }
  struct Row {
    let section: Section
    let entries: [Entry]
  }
  let sections: [Section]

  /// Preserve Home order and the existing ten-poster budgets; the next row excludes Continue.
  func selectedRows() -> [Row] {
    let continuing = sections.first { $0.id == "continue" }
    let entries = Array((continuing?.entries ?? []).prefix(10))
    let seen = Set(entries.map(\.id))
    var rows = continuing.map { [Row(section: $0, entries: entries)] } ?? []
    for section in sections where section.id != "continue" {
      let next = Array(section.entries.filter { !seen.contains($0.id) }.prefix(10))
      if !next.isEmpty { rows.append(Row(section: section, entries: next)); break }
    }
    return rows
  }
}
