package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

// SummarizeLibraryList batches the shell's entire allowed library set. The
// caller's snapshot carries every key, candidate and decoration query.
func (s *Service) SummarizeLibraryList(ctx context.Context, viewer Viewer, libraries []Library) (map[string]LibrarySummary, error) {
	out := map[string]LibrarySummary{}
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		reader := s.WithContext(ctx)
		keys, reasons, err := reader.librarySummaryBatchKeys(ctx, viewer, libraries)
		if err != nil {
			return err
		}
		cores := map[string]librarySummaryCore{}
		var pending []summaryBatchInput
		for _, lib := range libraries {
			if !viewer.AllowsLibrary(lib.ID) {
				continue
			}
			key, ok := keys[lib.ID]
			if !ok || key.Handle == 0 {
				return sql.ErrNoRows
			}
			if reasons[lib.ID] == "" {
				if value, hit := s.state.summaries.peek(key); hit {
					cores[lib.ID] = value
					continue
				}
			} else {
				telemetry.CountLibrarySummaryCache(reasons[lib.ID])
			}
			partial := isReadingLibrary(lib.Kind) && (reasons[lib.ID] == telemetry.SummaryBypassReading || key.Reading.Ready == 0 || key.Reading.Input != key.Reading.Ready || key.Reading.Rule != compactcatalog.ReadingProjectionRuleVersion)
			pending = append(pending, summaryBatchInput{lib.ID, lib.Kind, key.Handle, key.Reading.Ready, partial})
		}
		if len(pending) > 0 {
			computed, e := reader.computeLibrarySummaryBatch(ctx, viewer, pending)
			if e != nil {
				return e
			}
			for _, lib := range pending {
				value := computed[lib.Library]
				if reasons[lib.Library] == "" {
					value, e = s.state.summaries.get(ctx, keys[lib.Library], func() (librarySummaryCore, error) { return value, nil })
					if e != nil {
						return e
					}
				}
				cores[lib.Library] = value
			}
		}
		out, err = reader.decorateLibrarySummaryBatch(ctx, libraries, cores)
		return err
	})
	return out, err
}

func (c *librarySummaryCache) peek(key summaryKey) (librarySummaryCore, bool) {
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		c.mu.Unlock()
		return librarySummaryCore{}, false
	}
	c.lru.MoveToFront(e)
	value := e.Value.(summaryEntry).core
	c.mu.Unlock()
	telemetry.CountLibrarySummaryCache(telemetry.SummaryHitEntry)
	return value.clone(), true
}

type summaryBatchInput struct {
	Library string `json:"library"`
	Kind    string `json:"kind"`
	Handle  int64  `json:"handle"`
	Ready   int64  `json:"ready"`
	Partial bool   `json:"partial"`
}

// The correlated list subqueries seek the existing ordered partial index before
// privacy or publication eligibility. Each branch has a literal raw limit; no
// scan expands to find an eligible row. The request JSON is a parameter set,
// never stored catalogue state or a continuation result.
const summaryBatchCandidates = `WITH requested AS MATERIALIZED (
 SELECT json_extract(value,'$.library') library_name,json_extract(value,'$.kind') library_kind,
 json_extract(value,'$.handle') handle,json_extract(value,'$.ready') ready,json_extract(value,'$.partial') partial FROM json_each(?)
 ), kind_requests AS MATERIALIZED (
 SELECT r.*,k.id entity_kind FROM requested r CROSS JOIN catalog_kinds k
 WHERE (r.library_kind NOT IN('tv','anime','music','audiobook','book','comic') AND k.name='movie')
 OR (r.library_kind IN('tv','anime') AND k.name IN('show','episode'))
 OR (r.library_kind='music' AND k.name IN('artist','album','song'))
 OR (r.library_kind='audiobook' AND k.name IN('book','author'))
 OR (r.library_kind='book' AND k.name='ebook') OR (r.library_kind='comic' AND k.name='comic')
 ), candidates AS MATERIALIZED (
 SELECT r.library_name,r.library_kind,r.handle,r.ready,r.partial,e.id,e.public_id,e.kind,e.added_ms
 FROM kind_requests r CROSS JOIN catalog_entities e
 WHERE r.partial=0 AND e.id IN(SELECT picked.id FROM catalog_entities picked INDEXED BY catalog_entities_summary_added
 WHERE picked.library_id=r.handle AND picked.kind=r.entity_kind AND picked.retired=0 ORDER BY picked.added_ms DESC,picked.id DESC LIMIT 20001)
 UNION ALL
 SELECT r.library_name,r.library_kind,r.handle,r.ready,r.partial,e.id,e.public_id,e.kind,e.added_ms
 FROM kind_requests r CROSS JOIN catalog_entities e
 WHERE r.partial=1 AND e.id IN(SELECT picked.id FROM catalog_entities picked INDEXED BY catalog_entities_summary_added
 WHERE picked.library_id=r.handle AND picked.kind=r.entity_kind AND picked.retired=0 ORDER BY picked.added_ms DESC,picked.id DESC LIMIT 129)
 ) `

func (s *Service) computeLibrarySummaryBatch(ctx context.Context, viewer Viewer, inputs []summaryBatchInput) (map[string]librarySummaryCore, error) {
	out := map[string]librarySummaryCore{}
	raw, _ := json.Marshal(inputs)
	clause, args := EntityRestrictionSQL("e.id", viewer.Restrictions)
	book := readingSummaryEligibleSQL(&readingBrowseScope{kind: "book", mobi: s.readingMobiConfigured()}, "e.id")
	comic := readingSummaryEligibleSQL(&readingBrowseScope{kind: "comic"}, "e.id")
	clause = "(" + clause + ") AND (e.library_kind NOT IN('book','comic') OR ((e.partial=1 OR summary_member.item_id IS NOT NULL) AND CASE e.library_kind WHEN 'book' THEN " + book + " ELSE " + comic + " END))"
	query := summaryBatchCandidates + `SELECT e.library_name,pid(e.public_id),k.name,(` + clause + `),CASE WHEN e.library_kind='book' AND e.partial=0 THEN summary_member.publication_type ELSE NULL END
 FROM candidates e CROSS JOIN catalog_kinds k ON k.id=e.kind
 LEFT JOIN reading_attribution_members summary_member ON summary_member.library_id=e.handle AND summary_member.generation=e.ready AND summary_member.item_id=e.id AND e.partial=0
 ORDER BY e.library_name,e.added_ms DESC,e.id DESC`
	values := append([]any{string(raw)}, args...)
	rows, err := s.read().QueryContext(ctx, query, values...)
	if err != nil {
		return nil, err
	}
	type accumulation struct {
		input                         summaryBatchInput
		counts, visited, publications map[string]int
		core                          librarySummaryCore
	}
	acc := map[string]*accumulation{}
	for _, input := range inputs {
		acc[input.Library] = &accumulation{input: input, counts: map[string]int{}, visited: map[string]int{}, publications: map[string]int{}, core: librarySummaryCore{Empty: true, EmptyKnown: !input.Partial, Counts: []LibraryKindCount{}}}
	}
	for rows.Next() {
		var library, id, kind string
		var visible bool
		var publication sql.NullString
		if err = rows.Scan(&library, &id, &kind, &visible, &publication); err != nil {
			break
		}
		a := acc[library]
		if a == nil {
			err = errors.New("catalog: unexpected summary library")
			break
		}
		a.visited[kind]++
		cap := librarySummaryCap
		if a.input.Partial {
			cap = 128
		}
		if a.visited[kind] > cap {
			a.core.EmptyKnown = false
			if a.input.Partial && visible {
				a.core.Empty = false
			}
			continue
		}
		if !visible {
			continue
		}
		a.counts[kind]++
		a.core.Empty = false
		if publication.Valid {
			switch publication.String {
			case "book", "magazine", "newspaper":
				a.publications[publication.String]++
			default:
				err = ErrReadingProjectionChanged
			}
			if err != nil {
				break
			}
		}
		coverKind := summaryKinds(a.input.Kind)[0]
		if a.input.Kind == "music" {
			coverKind = "album"
		}
		if kind == coverKind && len(a.core.Targets) < 64 {
			targetKind := kind
			if kind == "movie" || kind == "ebook" || kind == "comic" {
				targetKind = "item"
			}
			a.core.Targets = append(a.core.Targets, artworkTarget{targetKind, id})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for id, a := range acc {
		cap := librarySummaryCap
		if a.input.Partial {
			cap = 128
		}
		for _, kind := range summaryKinds(a.input.Kind) {
			a.core.Counts = append(a.core.Counts, LibraryKindCount{Kind: kind, Count: a.counts[kind], AtLeast: a.input.Partial || a.visited[kind] > cap})
		}
		if a.input.Kind == "book" && !a.input.Partial {
			for _, kind := range []string{"book", "magazine", "newspaper"} {
				a.core.PublicationCounts = append(a.core.PublicationCounts, LibraryKindCount{Kind: kind, Count: a.publications[kind], AtLeast: a.visited["ebook"] > cap})
			}
		}
		out[id] = a.core
	}
	return out, nil
}

func (s *Service) decorateLibrarySummaryBatch(ctx context.Context, libraries []Library, cores map[string]librarySummaryCore) (map[string]LibrarySummary, error) {
	out := map[string]LibrarySummary{}
	var targets []artworkTarget
	var movieIDs []string
	var libraryIDs []string
	for _, lib := range libraries {
		core, ok := cores[lib.ID]
		if !ok {
			continue
		}
		libraryIDs = append(libraryIDs, lib.ID)
		targets = append(targets, core.Targets...)
		if lib.Kind == "movie" {
			for _, target := range core.Targets {
				movieIDs = append(movieIDs, target.ID)
			}
		}
		out[lib.ID] = LibrarySummary{Counts: cloneSummaryCounts(core.Counts), PublicationCounts: cloneSummaryCounts(core.PublicationCounts), CoverURLs: []string{}, Empty: core.Empty, EmptyKnown: core.EmptyKnown}
	}
	fallback := map[artworkTarget]resolvedArtwork{}
	if len(movieIDs) > 0 {
		raw, _ := json.Marshal(movieIDs)
		rows, err := s.read().QueryContext(ctx, `SELECT pid(e.public_id),d.poster_url<>'',d.backdrop_url<>'' FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) CROSS JOIN catalog_item_details d ON d.entity_id=e.id`, string(raw))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var poster, backdrop bool
			if err = rows.Scan(&id, &poster, &backdrop); err != nil {
				break
			}
			a := resolvedArtwork{}
			if poster {
				a.PosterURL = "/v1/items/" + id + "/art/poster"
			}
			if backdrop {
				a.BackdropURL = "/v1/items/" + id + "/art/backdrop"
			}
			fallback[artworkTarget{"item", id}] = a
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	art, err := s.resolveArtworkContext(ctx, targets)
	if err != nil {
		return nil, err
	}
	for _, lib := range libraries {
		value, ok := out[lib.ID]
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, target := range cores[lib.ID].Targets {
			a := art[target]
			if a.PosterURL == "" {
				a.PosterURL = fallback[target].PosterURL
			}
			if a.BackdropURL == "" {
				a.BackdropURL = fallback[target].BackdropURL
			}
			if len(value.CoverURLs) < 3 && a.PosterURL != "" && !seen[a.PosterURL] {
				value.CoverURLs = append(value.CoverURLs, a.PosterURL)
				seen[a.PosterURL] = true
			}
			if value.LatestArtwork == "" {
				value.LatestArtwork = a.BackdropURL
			}
		}
		out[lib.ID] = value
	}
	if len(libraryIDs) > 0 {
		raw, _ := json.Marshal(libraryIDs)
		rows, e := s.read().QueryContext(ctx, `SELECT requested.value,NOT EXISTS(SELECT 1 FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE j.library_id=requested.value AND j.status IN('complete','complete_with_warnings')) AND EXISTS(SELECT 1 FROM jobs WHERE library_id=requested.value AND status IN('queued','running','paused')) FROM json_each(?) requested`, string(raw))
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			var first bool
			if e = rows.Scan(&id, &first); e != nil {
				break
			}
			value := out[id]
			value.FirstScan = first
			out[id] = value
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
