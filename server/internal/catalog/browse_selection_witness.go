package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

// Selection reads saved/playlist membership in addition to the common exact
// browse predicate. These maintained metadata generations are synchronous;
// unlike browseGroupFence, checking them never COUNTs saved entries. They are
// deliberately conservative (unrelated saved changes cause a preparation miss).
// Ordinary page/cursor revisions retain their existing publication semantics.
func (s *Service) browseSelectionWitness(library, profile string, restrictions identity.ContentRestrictions) (string, error) {
	base, err := s.browseExactWitness(library, profile, restrictions)
	if err != nil {
		return "", err
	}
	var saved, playlists, predicates int64
	if err = s.read().QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&predicates); err != nil {
		return "", err
	}
	if predicates <= 0 {
		return "", ErrSelectionBatchStale
	}
	if err = s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&saved); err != nil {
		return "", err
	}
	if parts, ok := profileParts(profile); ok {
		if err = s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM playlist_directory_revisions WHERE authority=? AND account_id=? AND profile_id=?),0)`, parts[0], parts[1], parts[2]).Scan(&playlists); err != nil {
			return "", err
		}
	}
	return operationHash([]any{"exact-selection-v1", base, predicates, saved, playlists, s.recommendationNow(time.Time{}).UTC().Format("2006-01-02")}), nil
}
