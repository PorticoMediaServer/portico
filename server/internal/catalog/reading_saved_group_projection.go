package catalog

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type ReadingSavedGroupProjection struct {
	Target          ReadingSavedGroupTarget
	Proof           ReadingSavedGroupProof
	Title           string
	Count           int64
	PosterURL       string
	GenerationFence string
}

// The caller supplies a freshly admitted normal personal Viewer. All accepted
// identity, label/count/member and artwork facts below share its transaction.
func ResolveReadingSavedGroupTx(ctx context.Context, tx *sql.Tx, viewer Viewer, target ReadingSavedGroupTarget, mobiAvailable bool) (ReadingSavedGroupProjection, error) {
	out := ReadingSavedGroupProjection{Target: target}
	if tx == nil || viewer.Profile == "" || viewer.Fence == "" {
		return out, identity.ErrForbidden
	}
	if !validReadingSavedGroupTarget(target) || !viewer.AllowsLibrary(target.LibraryID) {
		return out, sql.ErrNoRows
	}
	var handle int64
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT cl.id,lib.kind FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind IN('book','comic')`, target.LibraryID).Scan(&handle, &kind); err != nil {
		return out, err
	}
	if target.GroupKind != ReadingSavedSeries && kind != "book" {
		return out, sql.ErrNoRows
	}
	generation, err := compactcatalog.ReadingProjectionReadyTx(ctx, tx, handle)
	if errors.Is(err, compactcatalog.ErrBuilding) {
		return out, ErrReadingProjectionChanged
	}
	if err != nil {
		return out, err
	}
	scope := readingBrowseScope{library: handle, kind: kind, generation: generation, mobi: kind == "book" && mobiAvailable}
	visible, args := viewer.itemVisibilitySQL("e.id")
	base := ` FROM reading_attribution_members m JOIN catalog_entities e ON e.id=m.item_id AND e.library_id=m.library_id
 WHERE ` + scope.memberScope() + ` AND m.series_id=? AND m.series_key IS NOT NULL AND m.series_key<>'' AND m.series_value IS NOT NULL AND m.series_value<>''
 AND e.retired=0 AND ` + scope.eligibleSQL("e.id") + ` AND (` + visible + `)`
	parameters := append([]any{target.SeriesID}, args...)
	order := `numbered_order,number_value,sort_key COLLATE BINARY,entity`
	if target.GroupKind != ReadingSavedSeries {
		base += ` AND m.publication_type=? AND m.issue_date IS NOT NULL`
		parameters = append(parameters, string(target.GroupKind))
		if target.GroupKind == ReadingSavedNewspaper {
			base += ` AND length(m.issue_date)=10`
		} else {
			base += ` AND length(m.issue_date) IN(7,10)`
		}
		order = `issue_date DESC,entity DESC`
	}
	query := `WITH eligible AS MATERIALIZED(SELECT m.series_key,m.series_value,m.series_source,m.numbered_order,m.number_value,m.sort_key,m.issue_date,e.id AS entity,pid(e.public_id) AS public_id` + base + `),
 spellings AS(SELECT series_value,count(*) AS frequency,sum(series_source='owner') AS owners FROM eligible GROUP BY series_value),
 label AS(SELECT series_value FROM spellings ORDER BY owners>0 DESC,CASE WHEN owners>0 THEN owners ELSE frequency END DESC,series_value COLLATE BINARY LIMIT 1),
 counts AS(SELECT count(*) AS total,min(series_key COLLATE BINARY) AS first_key,max(series_key COLLATE BINARY) AS last_key FROM eligible),
 cover AS(SELECT entity,public_id FROM eligible ORDER BY ` + order + ` LIMIT 1)
 SELECT counts.first_key,counts.last_key,label.series_value,counts.total,cover.entity,cover.public_id FROM counts CROSS JOIN label CROSS JOIN cover`
	var first, last, public string
	var entity int64
	if err = tx.QueryRowContext(ctx, query, parameters...).Scan(&first, &last, &out.Title, &out.Count, &entity, &public); err != nil {
		return out, err
	}
	if first == "" || first != last || !utf8.ValidString(first) || len(first) > 1<<20 || !utf8.ValidString(out.Title) || out.Title == "" || utf8.RuneCountInString(out.Title) > 4096 || out.Count < 1 || out.Count > 9007199254740991 {
		return ReadingSavedGroupProjection{}, ErrReadingProjectionChanged
	}
	covers, err := compactcatalog.ReadingCoverQueriesTx(ctx, tx, []int64{entity})
	if err != nil {
		return out, err
	}
	if digest := covers[entity]; digest != "" {
		out.PosterURL = "/v1/items/" + url.PathEscape(public) + "/art/cover?" + digest
	}
	out.Proof = ReadingSavedGroupProof{Key: first, ViewerFence: viewer.Fence}
	out.GenerationFence = scope.fence()
	return out, nil
}
