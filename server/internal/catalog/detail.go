package catalog

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/metadatalanguage"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

func (s *Service) detailOnce(viewer Viewer, server, item string, owner, includeRelated bool) (Detail, error) {
	out := Detail{Markers: []segmentmarkers.Marker{}, Actions: []DetailAction{}, Metadata: DetailMetadata{Status: "unavailable", Sources: []MetadataSource{}, Ratings: []ProviderRating{}, Genres: []Genre{}, Credits: []Credit{}}}
	if e := s.VisibleItem(s.Context(), viewer, item); e != nil {
		return out, e
	}
	profile, fence := viewer.Profile, viewer.Fence
	library, e := s.LibraryForItem(item)
	if e != nil {
		return out, e
	}
	before, e := s.ContentRevision(library, profile)
	if e != nil {
		return out, e
	}
	out.Scope = DetailScope{server, library, item, fence}
	out.Revision = before
	out.Item, e = s.getItem(profile, item, false)
	if e != nil {
		return out, e
	}
	// The detail advertises only ready selected objects, just like Search.
	// Stored provider/folder hints do not identify an immutable representation.
	target := artworkTarget{Kind: "item", ID: item}
	artwork, e := s.resolveArtwork([]artworkTarget{target})
	if e != nil {
		return out, e
	}
	art := artwork[target]
	out.Item.PosterURL, out.Item.BackdropURL, out.Item.StillURL = art.PosterURL, art.BackdropURL, art.StillURL
	out.Personal, e = readPersonal(s.read(), profile, item)
	if e != nil {
		return out, e
	}
	out.Item.ProgressSeconds = out.Personal.ProgressSeconds
	min, max, step := 0., 10., 1.
	out.Actions = []DetailAction{{ID: "watchlist", LabelKey: "action.watchlist", Enabled: true}, {ID: "favorite", LabelKey: "action.favorite", Enabled: true}, {ID: "rating", LabelKey: "action.rating", Enabled: true, Min: &min, Max: &max, Step: &step}}
	out.Actions = append(out.Actions, DetailAction{ID: "watched", LabelKey: "action.watched", Enabled: true})
	if out.Item.Available {
		zero := 0.
		resume := out.Item.ProgressSeconds
		if resume > out.Item.Duration {
			resume = out.Item.Duration
		}
		if out.Item.Kind != "audiobook_file" && resume >= out.Item.Duration-3 {
			resume = 0
		}
		if resume > 0 {
			out.Actions = append(out.Actions, DetailAction{ID: "resume", LabelKey: "action.resume", Enabled: true, Playback: &ContentPlayback{ItemID: item, StartSeconds: &resume}}, DetailAction{ID: "start_over", LabelKey: "action.start_over", Enabled: true, Playback: &ContentPlayback{ItemID: item, StartSeconds: &zero}})
		} else {
			out.Actions = append(out.Actions, DetailAction{ID: "play", LabelKey: "action.play", Enabled: true, Playback: &ContentPlayback{ItemID: item, StartSeconds: &zero}})
		}
	}
	if owner {
		if out.Item.Kind == "audiobook_file" {
			out.Actions = append(out.Actions, DetailAction{ID: "local_metadata_policy", LabelKey: "action.local_metadata_policy", Enabled: true})
		}
		if out.Item.Kind == "song" {
			out.Actions = append(out.Actions, DetailAction{ID: "music_metadata_review", LabelKey: "action.music_metadata_review", Enabled: true})
		}
		if editableMetadataKind(out.Item.Kind) {
			out.Actions = append(out.Actions, DetailAction{ID: "metadata_edit", LabelKey: "action.metadata_edit", Enabled: true})
		}
		out.Actions = append(out.Actions, DetailAction{ID: "delete", LabelKey: "action.unlink_item", Enabled: true})
	}
	if out.Facts, e = s.titleFacts(item); e != nil {
		return out, e
	}
	if out.Languages, e = s.detailLanguages(item); e != nil {
		return out, e
	}
	if out.Collections, e = s.detailCollections(viewer, item); e != nil {
		return out, e
	}
	if out.MissingSince, e = s.MissingSince(s.Context(), item); e != nil {
		return out, e
	}
	var known int
	if e = s.read().QueryRow(`SELECT count(*) FROM metadata_details WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&known); e != nil {
		return out, e
	}
	if known > 0 {
		out.Metadata.Status = "available"
	}
	rows, e := s.read().Query(`SELECT provider,value,scale,votes,source_url,observed_at FROM metadata_ratings WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY provider LIMIT 16`, item)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v ProviderRating
		if e = rows.Scan(&v.Provider, &v.Value, &v.Max, &v.Votes, &v.SourceURL, &v.ObservedAt); e != nil {
			rows.Close()
			return out, e
		}
		out.Metadata.Ratings = append(out.Metadata.Ratings, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = s.itemGenres(item, out.Item.Kind)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v Genre
		if e = rows.Scan(&v.ID, &v.Name, &v.Provider); e != nil {
			rows.Close()
			return out, e
		}
		out.Metadata.Genres = append(out.Metadata.Genres, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	// The cast's first page and the key crew; the rest pages from
	// GET /v1/items/{id}/credits (credits.go).
	credits, totals, e := s.detailCredits(item)
	if e != nil {
		return out, e
	}
	out.Metadata.Credits, out.Metadata.CreditTotals = credits, totals
	if out.Item.Episode != nil {
		out.EpisodeDetails, e = s.episodeDetails(viewer, out.Item)
		if e != nil {
			return out, e
		}
	}
	rows, e = s.read().Query(`SELECT provider,source_url,observed_at FROM metadata_details WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY provider LIMIT 16`, item)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var source MetadataSource
		if e = rows.Scan(&source.Provider, &source.SourceURL, &source.ObservedAt); e != nil {
			rows.Close()
			return out, e
		}
		out.Metadata.Sources = append(out.Metadata.Sources, source)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = s.read().Query(`SELECT DISTINCT c.attribution FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id WHERE c.attribution<>'' AND ((a.kind='item' AND a.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))) OR (a.kind='album' AND a.entity_id IN(SELECT album.id FROM catalog_entities source JOIN catalog_songs song ON song.entity_id=source.id JOIN catalog_entities album ON album.id=song.album_id WHERE source.public_id=pid_blob(?))) OR (a.kind='book' AND a.entity_id IN(SELECT book.id FROM catalog_entities source JOIN catalog_book_files part ON part.entity_id=source.id JOIN catalog_entities book ON book.id=part.book_id WHERE source.public_id=pid_blob(?)))) ORDER BY c.attribution LIMIT 12`, item, item, item)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var text string
		if e = rows.Scan(&text); e != nil {
			rows.Close()
			return out, e
		}
		out.Metadata.Attributions = append(out.Metadata.Attributions, text)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	var tvdb int
	if e = s.read().QueryRow(`SELECT count(*) FROM metadata_details WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND provider='tvdb'`, item).Scan(&tvdb); e != nil {
		return out, e
	}
	if tvdb > 0 {
		seen := false
		for _, text := range out.Metadata.Attributions {
			if strings.Contains(text, "TheTVDB") {
				seen = true
			}
		}
		if !seen && len(out.Metadata.Attributions) < 12 {
			out.Metadata.Attributions = append(out.Metadata.Attributions, "Metadata provided by TheTVDB. https://thetvdb.com")
		}
	}
	// One engine serves detail.related, per-item recommendation rows and
	// suggestions. detail.related keeps its published shape and budgets.
	if includeRelated {
		if out.Related, e = s.relatedProjection(viewer, out.Item, out.Metadata.Genres); e != nil {
			return out, e
		}
	}
	if out.Extras, e = s.itemExtras(profile, out.Item); e != nil {
		return out, e
	}
	after, e := s.ContentRevision(library, profile)
	if e != nil {
		return out, e
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// Detail has no continuation, so a read that raced a publication simply retries.
func (s *Service) Detail(viewer Viewer, server, item string, owner bool) (Detail, error) {
	return s.DetailWithRelated(viewer, server, item, owner, true)
}

// DetailWithRelated lets callers omit the recommendation composition entirely.
// Playable items keep the original detailOnce path unchanged; the non-playable
// containers P2-61 opens for FileInfo compose from their own stored projection
// in detail_container.go.
func (s *Service) DetailWithRelated(viewer Viewer, server, item string, owner, includeRelated bool) (Detail, error) {
	library, namespace, e := s.LibraryForDetail(item)
	if e != nil {
		return Detail{}, e
	}
	if namespace == "item" {
		return consistentRead(true, func() (Detail, error) { return s.detailOnce(viewer, server, item, owner, includeRelated) })
	}
	return consistentRead(true, func() (Detail, error) { return s.containerDetailOnce(viewer, server, library, namespace, item) })
}

// titleFacts reads the item's own facts (the editor's General fields); nil when the item has
// no details row or every field is empty.
func (s *Service) titleFacts(item string) (*TitleFacts, error) {
	var f TitleFacts
	err := s.read().QueryRow(`SELECT COALESCE(original_title,''),COALESCE(edition,''),COALESCE(tagline,''),COALESCE(release_date,''),COALESCE(content_rating,''),COALESCE(studio,''),COALESCE(network,''),COALESCE(country,'')
	 FROM catalog_item_details WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&f.OriginalTitle, &f.Edition, &f.Tagline, &f.ReleaseDate, &f.ContentRating, &f.Studio, &f.Network, &f.Country)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if f.OriginalLanguage, err = metadatalanguage.Published(s.Context(), s.read(), item); err != nil {
		return nil, err
	}
	if f == (TitleFacts{}) {
		return nil, nil
	}
	return &f, nil
}

// Neighbours use the server's episode order and the caller's visibility. The
// SQL reads two indexed neighbours, not every episode for a focused TV tile.
func (s *Service) episodeDetails(viewer Viewer, item Item) (*EpisodeDetails, error) {
	info := item.Episode
	if info == nil {
		return nil, nil
	}
	out := &EpisodeDetails{}
	if err := s.read().QueryRow(`SELECT COALESCE(NULLIF(d.content_rating,''),sh.content_rating,'') FROM catalog_entities item JOIN catalog_item_details d ON d.entity_id=item.id JOIN catalog_episodes ep ON ep.entity_id=item.id LEFT JOIN catalog_shows sh ON sh.entity_id=ep.show_id WHERE item.public_id=pid_blob(?)`, item.ID).Scan(&out.ContentRating); err != nil {
		return nil, err
	}
	settings, err := readShowSettings(s.read(), info.ShowID)
	if err != nil {
		return nil, err
	}
	keySeason := 1
	if info.SeasonNumber != nil {
		keySeason = *info.SeasonNumber
	}
	if keySeason == 0 {
		keySeason = 10000
	}
	for _, previous := range []bool{true, false} {
		before := previous
		if settings.NewestFirst {
			before = !before
		}
		op, direction := ">", "ASC"
		if before {
			op, direction = "<", "DESC"
		}
		visible, args := viewer.itemVisibilitySQL("item.id")
		values := append([]any{info.ShowID}, args...)
		values = append(values, keySeason, info.Number, item.ID)
		order := `CASE WHEN COALESCE(sd.number,1)=0 THEN 10000 ELSE COALESCE(sd.number,1) END`
		var neighbour EpisodeNeighbour
		err = s.read().QueryRow(`SELECT pid(item.public_id),ep.number,item.title FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND `+visible+` AND (`+order+`,ep.number,item.id)`+op+`(?,?,(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))) ORDER BY `+order+` `+direction+`,ep.number `+direction+`,item.id `+direction+` LIMIT 1`, values...).Scan(&neighbour.ID, &neighbour.Number, &neighbour.Title)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if previous {
			out.Previous = &neighbour
		} else {
			out.Next = &neighbour
		}
	}
	return out, nil
}
