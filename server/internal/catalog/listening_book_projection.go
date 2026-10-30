package catalog

import "sort"

// Author and series pages already receive the canonical per-book facts from
// EnrichListeningEntries (listeningBookFacts): stored/local narrator and series,
// complete book duration, and the requesting profile's own resume progress.
// This adds only the entity-level aggregate after that enrichment, over the
// final rows and the same visible section.
//
// The group aggregate is published only for a genuinely complete first window:
// start 0, exact non-lower-bound total, every entry returned, no continuation,
// and every row carrying the contributing fact. Missing duration or narrator is
// never turned into zero or a partial sum.
func applyAuthorSeriesEntityFacts(out *ContentEnvelope, request ContentRequest) {
	if out.Entity == nil || request.View != "author" && request.View != "book_series" {
		return
	}
	var books *ContentSection
	for i := range out.Sections {
		if out.Sections[i].ID == "books" {
			books = &out.Sections[i]
			break
		}
	}
	if books == nil {
		return
	}
	count := books.TotalCount
	out.Entity.Count = &count
	if books.Start != 0 || books.NextCursor != "" || count == 0 || len(books.Entries) != count {
		return
	}
	total, completeDuration, completeNarrators := 0.0, true, true
	narrators := []string{}
	seen := map[string]bool{}
	for _, entry := range books.Entries {
		if entry.Duration == nil {
			completeDuration = false
		} else {
			total += *entry.Duration
		}
		if len(entry.Narrators) == 0 {
			completeNarrators = false
		} else {
			for _, narrator := range entry.Narrators {
				if !seen[narrator] {
					seen[narrator] = true
					narrators = append(narrators, narrator)
				}
			}
		}
	}
	if completeDuration {
		out.Entity.Duration = &total
	}
	if completeNarrators {
		sort.Strings(narrators)
		out.Entity.Narrators = narrators
	}
}
