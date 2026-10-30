package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strings"
)

// Original bounded composer, retained verbatim as the cache/decoration parity oracle.
func (s *Service) originalLibrarySummary(ctx context.Context, viewer Viewer, library Library) (LibrarySummary, error) {
	out := LibrarySummary{Counts: []LibraryKindCount{}, CoverURLs: []string{}, Empty: true}
	kinds := summaryKinds(library.Kind)
	clause, args := EntityRestrictionSQL("e.id", viewer.Restrictions)
	publicationTypeSQL := "NULL"
	if isReadingLibrary(library.Kind) {
		reading, err := s.readingBrowseScope(library.ID)
		if err != nil {
			return out, err
		}
		// The bounded candidate window stays unchanged. Its visible counts and
		// artwork must additionally belong to the same ready Reading membership
		// and canonical publication/link/format choice as Browse. Availability
		// does not exclude an otherwise eligible Reading title.
		clause = "(" + clause + ") AND " + reading.eligibleSQL("e.id") +
			" AND EXISTS(SELECT 1 FROM reading_attribution_members m WHERE " + reading.memberScope() + " AND m.item_id=e.id)"
		if library.Kind == "book" {
			publicationTypeSQL = `(SELECT m.publication_type FROM reading_attribution_members m WHERE ` + reading.memberScope() + ` AND m.item_id=e.id)`
		}
	}
	selections := []string{}
	values := []any{}
	for _, kind := range kinds {
		code, err := compactcatalog.ParseKind(kind)
		if err != nil {
			return out, err
		}
		selections = append(selections, `SELECT * FROM (SELECT e.id,e.public_id,e.kind,e.added_ms FROM catalog_entities e INDEXED BY catalog_entities_summary_added WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=? AND e.retired=0 ORDER BY e.added_ms DESC,e.id DESC LIMIT 20001)`)
		values = append(values, library.ID, code)
	}
	values = append(values, args...)
	// Each kind contributes at most 20,001 candidates through its ordered partial
	// index. Sorting the union and applying live visibility is bounded even for
	// a cold restriction class and a catalogue with millions of titles.
	rows, err := s.read().QueryContext(ctx, `WITH candidates AS (`+strings.Join(selections, " UNION ALL ")+`)
 SELECT pid(e.public_id),k.name,COALESCE(d.poster_url,''),COALESCE(d.backdrop_url,''),(`+clause+`),`+publicationTypeSQL+` FROM candidates e JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_item_details d ON d.entity_id=e.id ORDER BY e.added_ms DESC,e.id DESC`, values...)
	if err != nil {
		return out, err
	}
	counts := map[string]int{}
	publicationCounts := map[string]int{}
	visited := map[string]int{}
	coverKind := kinds[0]
	if library.Kind == "music" {
		coverKind = "album"
	}
	targets := []artworkTarget{}
	fallback := map[artworkTarget]resolvedArtwork{}
	seen := map[string]bool{}
	for rows.Next() {
		var id, kind, poster, backdrop string
		var visible bool
		var publicationType sql.NullString
		if err = rows.Scan(&id, &kind, &poster, &backdrop, &visible, &publicationType); err != nil {
			rows.Close()
			return out, err
		}
		visited[kind]++
		if visited[kind] > librarySummaryCap {
			continue
		}
		if !visible {
			continue
		}
		counts[kind]++
		if publicationType.Valid {
			switch publicationType.String {
			case "book", "magazine", "newspaper":
				publicationCounts[publicationType.String]++
			default:
				rows.Close()
				return out, ErrReadingProjectionChanged
			}
		}
		for _, countKind := range kinds {
			if kind == countKind {
				out.Empty = false
			}
		}
		if kind == coverKind {
			if len(targets) < 64 {
				targetKind := kind
				if kind == "movie" || kind == "ebook" || kind == "comic" {
					targetKind = "item"
				}
				target := artworkTarget{targetKind, id}
				targets = append(targets, target)
				if kind == "movie" {
					art := resolvedArtwork{}
					if poster != "" {
						art.PosterURL = "/v1/items/" + id + "/art/poster"
					}
					if backdrop != "" {
						art.BackdropURL = "/v1/items/" + id + "/art/backdrop"
					}
					fallback[target] = art
				}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	art, err := s.resolveArtworkContext(ctx, targets)
	if err != nil {
		return out, err
	}
	for _, target := range targets {
		a := art[target]
		if a.PosterURL == "" {
			a.PosterURL = fallback[target].PosterURL
		}
		if a.BackdropURL == "" {
			a.BackdropURL = fallback[target].BackdropURL
		}
		if len(out.CoverURLs) < 3 && a.PosterURL != "" && !seen[a.PosterURL] {
			out.CoverURLs = append(out.CoverURLs, a.PosterURL)
			seen[a.PosterURL] = true
		}
		if out.LatestArtwork == "" {
			out.LatestArtwork = a.BackdropURL
		}
	}
	out.EmptyKnown = true
	for _, n := range visited {
		if n > librarySummaryCap {
			out.EmptyKnown = false
		}
	}
	for _, kind := range kinds {
		out.Counts = append(out.Counts, LibraryKindCount{Kind: kind, Count: counts[kind], AtLeast: visited[kind] > librarySummaryCap})
	}
	if library.Kind == "book" {
		for _, kind := range []string{"book", "magazine", "newspaper"} {
			out.PublicationCounts = append(out.PublicationCounts, LibraryKindCount{Kind: kind, Count: publicationCounts[kind], AtLeast: visited["ebook"] > librarySummaryCap})
		}
	}
	err = s.read().QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE j.library_id=? AND j.status IN('complete','complete_with_warnings')) AND EXISTS(SELECT 1 FROM jobs WHERE library_id=? AND status IN('queued','running','paused'))`, library.ID, library.ID).Scan(&out.FirstScan)
	return out, err
}
