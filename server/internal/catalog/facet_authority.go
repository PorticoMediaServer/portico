package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

var ErrFacetAuthorityUnavailable = errors.New("facet read authority is not installed")

type facetAuthorityContextKey struct{}

// WithFacetWindowAuthority installs a request's live authority checker. Runtime
// instances never capture a request principal or mutable viewer globally.
func WithFacetWindowAuthority(ctx context.Context, check FacetWindowCheck) context.Context {
	return context.WithValue(ctx, facetAuthorityContextKey{}, check)
}

// CheckFacetWindowAuthority is the process runtime's dispatcher. A missing
// request guard cannot authorize either preparing state or cached metadata.
func CheckFacetWindowAuthority(ctx context.Context, caller readprep.Caller, snapshot FacetBaseSnapshot) error {
	check, ok := ctx.Value(facetAuthorityContextKey{}).(FacetWindowCheck)
	if !ok || check == nil {
		return ErrFacetAuthorityUnavailable
	}
	return check(ctx, caller, snapshot)
}

// CaptureFacetAuthority is separate from the content source epoch. Ordinary
// metadata churn may retain a COMPLETE content basis; changes to facts used by
// an active restriction predicate cannot continue disclosing its old counts.
// Library grants and caller/session/profile authority are rechecked by HTTP.
func CaptureFacetAuthority(ctx context.Context, q compactcatalog.ReadQuery, scope FacetBaseScope) (string, error) {
	r, err := parseFacetBasePolicy(scope.Policy)
	if err != nil {
		return "", err
	}
	var library, kind string
	var policyEpoch, genreEpoch int64
	err = q.QueryRowContext(ctx, `SELECT c.library_id,l.kind,s.policy_epoch,s.genre_epoch
 FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id
 CROSS JOIN catalog_facet_source_state s
 WHERE c.id=? AND c.library_id=? AND c.retired=0 AND s.singleton=1`, scope.LibraryID, scope.Library).Scan(&library, &kind, &policyEpoch, &genreEpoch)
	if err != nil {
		return "", err
	}
	document := struct {
		LibraryID   int64  `json:"libraryId"`
		Library     string `json:"library"`
		Kind        string `json:"kind"`
		Algorithm   int    `json:"algorithm"`
		PolicyEpoch *int64 `json:"policyEpoch,omitempty"`
		GenreEpoch  *int64 `json:"genreEpoch,omitempty"`
	}{LibraryID: scope.LibraryID, Library: library, Kind: kind, Algorithm: 1}
	genre := scope.Source == SearchVideoGenreSource || strings.HasPrefix(scope.Source, SearchVideoGenreSource+":")
	if r.Active() {
		document.PolicyEpoch = &policyEpoch
	}
	if genre {
		bindGenreEpoch := r.Active()
		if !bindGenreEpoch {
			complete, err := genreGrantsCoverLiveLibraries(ctx, q, scope.Source)
			if err != nil {
				return "", err
			}
			bindGenreEpoch = !complete
		}
		// Empty fallback can also be revoked by a first member outside an
		// inactive viewer's library grants. All-library inactive viewers retain
		// the content-only basis during ordinary imports and policy churn.
		if bindGenreEpoch {
			document.GenreEpoch = &genreEpoch
		}
	}
	raw, err := json.Marshal(document)
	return string(raw), err
}

// Compare the exact canonical grant lifetimes/public IDs captured in the source
// with live libraries. A bare legacy source has no grant witness: fence it
// conservatively without admitting it as a runtime producer source.
func genreGrantsCoverLiveLibraries(ctx context.Context, q compactcatalog.ReadQuery, source string) (bool, error) {
	if source == SearchVideoGenreSource {
		return false, nil
	}
	if _, err := genreGrants(source); err != nil {
		return false, err
	}
	// The canonical array has unique public IDs. Joining each grant by the
	// library PK and confirming its public ID therefore counts each live
	// lifetime at most once. One JSON parameter also admits large grant sets
	// without a SQL-variable limit or one round trip per library.
	var complete bool
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM catalog_libraries WHERE retired=0)=
 (SELECT count(*) FROM json_each(?) g JOIN catalog_libraries l ON l.id=json_extract(g.value,'$.id')
 WHERE l.retired=0 AND l.library_id=json_extract(g.value,'$.library'))`, strings.TrimPrefix(source, SearchVideoGenreSource+":")).Scan(&complete)
	return complete, err
}

// IsSimpleFacetRequest uses the same dispatch order as Facets. Parsed nil
// queries stay nil, and Reading always wins over pivot/field dispatch.
func (s *Service) IsSimpleFacetRequest(request FacetRequest) (bool, error) {
	library, err := s.library(request.Library)
	if err != nil {
		return false, err
	}
	if isReadingLibrary(library.Kind) {
		return false, nil
	}
	return request.Query == nil && request.Pivot == "" && request.Field != "library" && request.Field != "actor" && request.Field != "director" && request.Field != "writer", nil
}
