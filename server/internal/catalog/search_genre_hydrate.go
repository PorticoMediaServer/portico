package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func (r *GenreRuntime) hydrate(ctx context.Context, id string, h readprep.Handle, delivery readprep.Result, d genreDescriptor, summary genreSummary) (SearchGenrePage, error) {
	page := SearchGenrePage{QueryID: id, Genres: []SearchGenre{}}
	// Revision names the complete coherent base vector, not mutable live heads.
	raw, _ := json.Marshal(summary.Bases)
	digest := sha256.Sum256(raw)
	page.Revision = hex.EncodeToString(digest[:])
	if d.Spec.Preview != "" {
		v := summary.HasGenres
		page.HasGenres = &v
	}
	bytes, rawBytes := int64(32768+8*len(delivery.Key.Canonical)), int64(0)
	for _, row := range summary.Rows {
		if row.Count < 1 || row.Count > 9007199254740991 || int64(int(row.Count)) != row.Count {
			return page, ErrFacetBaseInput
		}
		var width int64
		if err := dbwork.QueryRow(ctx, r.db, `SELECT length(CAST(v.value AS BLOB))+length(CAST(t.label AS BLOB)) FROM catalog_facet_base_rows b JOIN catalog_facet_base_values v ON v.id=b.value_id JOIN catalog_facet_base_texts t ON t.id=b.text_id WHERE b.base_id=? AND b.value_id=?`, row.Base, row.Value).Scan(&width); err != nil {
			return page, readprep.ErrStale
		}
		rawBytes += width
		bytes += 20*width + 8192
	}
	if err := delivery.ResizeDelivery(bytes); err != nil {
		if !(len(summary.Rows) == 1 && rawBytes > 256<<10 && errors.Is(err, readprep.ErrCapacity)) {
			return page, err
		}
	}
	page.Genres = make([]SearchGenre, 0, len(summary.Rows))
	for _, row := range summary.Rows {
		genre := SearchGenre{Count: int(row.Count), Covers: []SearchGenreArt{}}
		if err := dbwork.QueryRow(ctx, r.db, `SELECT v.value,t.label FROM catalog_facet_base_rows b JOIN catalog_facet_base_values v ON v.id=b.value_id JOIN catalog_facet_base_texts t ON t.id=b.text_id WHERE b.base_id=? AND b.value_id=?`, row.Base, row.Value).Scan(&genre.Value, &genre.Label); err != nil {
			return page, readprep.ErrStale
		}
		if row.Backdrop != nil {
			art, err := r.art(ctx, *row.Backdrop)
			if err != nil {
				return page, err
			}
			genre.Backdrop = &art
		}
		for _, ref := range row.Covers {
			art, err := r.art(ctx, ref)
			if err != nil {
				return page, err
			}
			genre.Covers = append(genre.Covers, art)
		}
		if d.Spec.Preview == "backdrop" && genre.Backdrop == nil || d.Spec.Preview == "covers" && len(genre.Covers) < 2 {
			return page, readprep.ErrStale
		}
		page.Genres = append(page.Genres, genre)
	}
	if d.Spec.Preview == "" && len(summary.Rows) > 0 {
		token := genreToken{handle: h, expires: delivery.OriginalExpiry.Unix()}
		first, last := summary.Rows[0], summary.Rows[len(summary.Rows)-1]
		token.base, token.value, token.count, token.direction = first.Base, first.Value, first.Count, 3
		page.ReloadCursor = r.encode(token)
		if summary.Earlier {
			token.direction = 2
			page.PreviousCursor = r.encode(token)
		}
		if summary.Later {
			token.base, token.value, token.count, token.direction = last.Base, last.Value, last.Count, 1
			page.NextCursor = r.encode(token)
		}
	}
	return page, nil
}
func (r *GenreRuntime) art(ctx context.Context, ref genreArtRef) (SearchGenreArt, error) {
	var art SearchGenreArt
	var public, source []byte
	var kind int
	var d artworkref.Descriptor
	// Recheck exact source/blob availability and target lifetime. A dead retained
	// reference is stale, never a different title or an invented drawable slot.
	role := "backdrop"
	if ref.Role == 2 {
		role = "poster"
	}
	query := `SELECT t.public_id,t.kind,CASE ? WHEN 1 THEN t.backdrop ELSE t.poster END,` + artworkref.Columns + `
 FROM catalog_search_genre_candidates c JOIN catalog_search_genre_targets t ON t.base_id=c.base_id AND t.entity_id=c.entity_id
 JOIN catalog_entities e ON e.id=t.entity_id AND e.public_id=t.public_id
 JOIN artwork_sources src ON src.digest=lower(hex(CASE ? WHEN 1 THEN t.backdrop ELSE t.poster END))
 JOIN artwork_selections selected ON selected.kind=CASE t.kind WHEN 1 THEN 'item' ELSE 'show' END AND selected.entity_id=t.entity_id AND selected.role=? AND selected.subject='' AND selected.digest=src.digest
 JOIN artwork_blobs sb ON sb.digest=src.small_blob AND sb.status='ready'
 JOIN artwork_blobs lb ON lb.digest=src.large_blob AND lb.status='ready'
 WHERE c.base_id=? AND c.value_id=? AND c.role=? AND c.position=? AND c.entity_id=?`
	err := dbwork.QueryRow(ctx, r.db, query, ref.Role, ref.Role, role, ref.Base, ref.Value, ref.Role, ref.Position, ref.Entity).Scan(append([]any{&public, &kind, &source}, d.ScanTargets()...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return art, readprep.ErrStale
	}
	if err != nil {
		return art, err
	}
	art.Kind = "item"
	if kind == 2 {
		art.Kind = "show"
	}
	art.ID = entityid.Encode(public)
	art.Path = selectedArtworkURL(art.Kind, art.ID, role, "", hex.EncodeToString(source), d)
	return art, nil
}
