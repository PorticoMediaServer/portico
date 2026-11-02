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
	var saved, playlists, predicates, personal, recommendations int64
	if err = s.read().QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&predicates); err != nil {
		return "", err
	}
	if predicates <= 0 {
		return "", ErrSelectionBatchStale
	}
	// Personal input changes invalidate this profile/library only. Taste and its
	// pending jobs are profile-wide because For you ranking crosses libraries.
	if err = s.read().QueryRow(`SELECT COALESCE((SELECT selection_revision FROM viewer_revisions WHERE profile_id=? AND library_id=?),0),COALESCE((SELECT revision FROM catalog_selection_profile_epochs WHERE profile_id=?),0)`, profile, library, profile).Scan(&personal, &recommendations); err != nil {
		return "", err
	}
	if err = s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&saved); err != nil {
		return "", err
	}
	if parts, ok := profileParts(profile); ok {
		if err = s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM playlist_directory_revisions WHERE authority=? AND account_id=? AND profile_id=?),0)`, parts[0], parts[1], parts[2]).Scan(&playlists); err != nil {
			return "", err
		}
	}
	return operationHash([]any{"exact-selection-v2", base, predicates, personal, recommendations, saved, playlists, s.recommendationNow(time.Time{}).UTC().Format("2006-01-02")}), nil
}

// BrowsePagePolicyFence protects an ordinary snapshot page independently of
// its optional exact-selection work. Personal progress and catalogue additions
// may stale that work; they do not turn the page into a continuation. Current
// grants are checked separately by the HTTP caller, in this same snapshot.
func (s *Service) BrowsePagePolicyFence(library, profile string) (string, error) {
	var policy, classification, saved, playlists int64
	var kind string
	err := s.read().QueryRow(`SELECT l.kind,p.policy_epoch,c.revision,g.revision
 FROM catalog_libraries l CROSS JOIN catalog_facet_source_state p
 CROSS JOIN catalog_summary_classification_revision c CROSS JOIN saved_resource_generation g
 WHERE l.library_id=? AND l.retired=0 AND p.singleton=1 AND c.singleton=1 AND g.id=1`, library).Scan(&kind, &policy, &classification, &saved)
	if err != nil {
		return "", err
	}
	if policy <= 0 || classification <= 0 {
		return "", ErrSelectionBatchStale
	}
	if parts, ok := profileParts(profile); ok {
		if err = s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM playlist_directory_revisions WHERE authority=? AND account_id=? AND profile_id=?),0)`, parts[0], parts[1], parts[2]).Scan(&playlists); err != nil {
			return "", err
		}
	}
	return operationHash([]any{"browse-page-policy-v1", library, profile, kind, policy, classification, saved, playlists}), nil
}
