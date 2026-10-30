package metadata

import (
	"context"
	"database/sql"
	"errors"
)

// Automatic acquisition always yields to explicit owner choices. Source and
// language preferences only compare ordinary automatic candidates.
func automaticArtworkOccupied(ctx context.Context, tx *sql.Tx, t RepairTarget, entity int64, role, subject, provider, candidate, fence string, pending bool) (bool, error) {
	var preferred, language, locale string
	if err := tx.QueryRowContext(ctx, `SELECT artwork_preference,artwork_language,COALESCE((SELECT p.language FROM screen_metadata_policies p JOIN catalog_libraries cl ON cl.library_id=p.library_id JOIN catalog_entities e ON e.library_id=cl.id WHERE e.id=?),default_language) FROM metadata_runtime_settings WHERE singleton=1`, entity).Scan(&preferred, &language, &locale); err != nil {
		return false, err
	}
	switch language {
	case "en":
		locale = "en"
	case "textless":
		locale = ""
	}
	incomingLocale := "und"
	if err := tx.QueryRowContext(ctx, `SELECT locale FROM artwork_candidates WHERE id=? AND kind=? AND entity_id=? AND provider=? AND source_fence=?`, candidate, t.Kind, entity, provider, fence).Scan(&incomingLocale); err != nil {
		return false, err
	}
	rank := func(value string) int {
		switch role {
		case "poster", "backdrop", "logo", "banner":
			return artworkLanguageRank(value, locale)
		}
		return 0
	}
	holds := func(currentProvider, currentFence, currentLocale string) bool {
		if currentProvider == "local" {
			if provider == "local" {
				return currentFence == fence
			}
			return preferred == "folders"
		}
		if provider == "local" {
			return preferred == "online" && currentFence == fence
		}
		return currentFence == fence && rank(currentLocale) <= rank(incomingLocale)
	}
	var currentProvider, currentFence, currentLocale string
	var owner bool
	err := tx.QueryRowContext(ctx, `SELECT a.locked=1 OR a.actor<>'' OR c.provider IN('upload','generated'),c.provider,c.source_fence,c.locale FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id WHERE a.kind=? AND a.entity_id=? AND a.role=? AND a.subject=?`, t.Kind, entity, role, subject).Scan(&owner, &currentProvider, &currentFence, &currentLocale)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && (owner || holds(currentProvider, currentFence, currentLocale)) {
		return true, nil
	}
	if !pending {
		return false, nil
	}
	err = tx.QueryRowContext(ctx, `SELECT j.actor<>'' OR j.provider IN('upload','generated'),j.provider,j.source_fence,COALESCE(c.locale,'und') FROM artwork_jobs j LEFT JOIN artwork_candidates c ON c.id=j.candidate_id WHERE j.kind=? AND j.entity_id=? AND j.role=? AND j.subject=? AND j.status IN('pending','running','retry') ORDER BY (j.actor<>'' OR j.provider IN('upload','generated')) DESC,j.preview LIMIT 1`, t.Kind, entity, role, subject).Scan(&owner, &currentProvider, &currentFence, &currentLocale)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner || holds(currentProvider, currentFence, currentLocale), nil
}
