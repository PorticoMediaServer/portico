package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

const SearchVideoGenreSource = "search-video-genre-v1"
const searchGenreTopCandidates = 100

type genreGrant struct {
	ID      int64  `json:"id"`
	Library string `json:"library"`
}
type genreWitness struct {
	Version  int                               `json:"version"`
	Fence    compactcatalog.FacetProducerFence `json:"fence"`
	ArtEpoch int64                             `json:"artEpoch"`
}

func genreGrants(source string) ([]genreGrant, error) {
	raw, ok := strings.CutPrefix(source, SearchVideoGenreSource+":")
	if !ok {
		return nil, ErrFacetBaseInput
	}
	var grants []genreGrant
	if json.Unmarshal([]byte(raw), &grants) != nil {
		return nil, ErrFacetBaseInput
	}
	canonical, _ := json.Marshal(grants)
	if string(canonical) != raw {
		return nil, ErrFacetBaseInput
	}
	for i, g := range grants {
		if g.ID <= 0 || g.Library == "" || i > 0 && grants[i-1].Library >= g.Library {
			return nil, ErrFacetBaseInput
		}
	}
	return grants, nil
}
func captureGenreScopes(ctx context.Context, q facetWindowReader, v Viewer) ([]FacetBaseScope, error) {
	libraries := append([]string{}, v.Libraries...)
	sort.Strings(libraries)
	grants := make([]genreGrant, 0, len(libraries))
	scopes := []FacetBaseScope{}
	for i, library := range libraries {
		if i > 0 && library == libraries[i-1] {
			continue
		}
		var id int64
		var kind string
		err := q.QueryRowContext(ctx, `SELECT c.id,l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.library_id=? AND c.retired=0`, library).Scan(&id, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		grants = append(grants, genreGrant{ID: id, Library: library})
		if kind == "movie" || kind == "tv" || kind == "anime" {
			scopes = append(scopes, FacetBaseScope{LibraryID: id, Library: library, Field: "genre", Policy: FacetBasePolicy(v.EffectiveRestrictions())})
		}
	}
	raw, _ := json.Marshal(grants)
	source := SearchVideoGenreSource + ":" + string(raw)
	for i := range scopes {
		scopes[i].Source = source
	}
	return scopes, nil
}
func CaptureSearchGenreBasePlan(ctx context.Context, q compactcatalog.ReadQuery, scope FacetBaseScope) (FacetBasePlan, error) {
	plan := FacetBasePlan{Scope: scope, Producer: searchGenreProducer{}}
	if !scope.valid() || scope.Field != "genre" {
		return plan, ErrFacetBaseInput
	}
	if _, err := genreGrants(scope.Source); err != nil {
		return plan, err
	}
	if _, err := parseFacetBasePolicy(scope.Policy); err != nil {
		return plan, err
	}
	var kind string
	if err := q.QueryRowContext(ctx, `SELECT l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.id=? AND c.library_id=? AND c.retired=0`, scope.LibraryID, scope.Library).Scan(&kind); err != nil {
		return plan, err
	}
	if kind != "movie" && kind != "tv" && kind != "anime" {
		return plan, ErrFacetBaseUnavailable
	}
	f, err := compactcatalog.CaptureFacetProducerFence(ctx, q)
	if err != nil {
		return plan, err
	}
	var art int64
	if err = q.QueryRowContext(ctx, `SELECT art_epoch FROM catalog_search_genre_state WHERE singleton=1`).Scan(&art); err != nil {
		return plan, err
	}
	raw, err := json.Marshal(genreWitness{Version: 1, Fence: f, ArtEpoch: art})
	plan.Witness = string(raw)
	return plan, err
}
func validateGenreWitness(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot) error {
	var w genreWitness
	if json.Unmarshal([]byte(s.Witness), &w) != nil || w.Version != 1 {
		return ErrFacetBaseInput
	}
	if err := compactcatalog.ValidateFacetProducerFence(ctx, tx, w.Fence); err != nil {
		return err
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT art_epoch FROM catalog_search_genre_state WHERE singleton=1`).Scan(&epoch); err != nil {
		return err
	}
	if epoch != w.ArtEpoch {
		return compactcatalog.ErrFacetProducerChanged
	}
	return nil
}

// Called by the generic bounded base GC before deleting value/base rows.
func retireSearchGenreBaseStep(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	r, err := tx.ExecContext(ctx, `DELETE FROM catalog_search_genre_candidates WHERE (base_id,value_id,role,position) IN(SELECT base_id,value_id,role,position FROM catalog_search_genre_candidates WHERE base_id=? LIMIT 8)`, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n > 0 {
		return false, err
	}
	r, err = tx.ExecContext(ctx, `DELETE FROM catalog_search_genre_targets WHERE base_id=? AND entity_id IN(SELECT entity_id FROM catalog_search_genre_targets WHERE base_id=? LIMIT 8)`, id, id)
	if err != nil {
		return false, err
	}
	n, err = r.RowsAffected()
	if err != nil || n > 0 {
		return false, err
	}
	r, err = tx.ExecContext(ctx, `DELETE FROM catalog_search_genre_ready WHERE base_id=? AND value_id IN(SELECT value_id FROM catalog_search_genre_ready WHERE base_id=? LIMIT 8)`, id, id)
	if err != nil {
		return false, err
	}
	n, err = r.RowsAffected()
	return n == 0, err
}
