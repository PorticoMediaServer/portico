package catalog

import "github.com/PorticoMediaServer/portico/server/internal/identity"

// Group counts serve the published restriction generation while contribution
// queues catch up. No whole group or book membership is counted on request.
func (s *Service) bookGroupClassReady(library string, r identity.ContentRestrictions) (int64, int64, error) {
	if err := s.compactProjectionReady(30); err != nil {
		return 0, 0, err
	}
	key, generation, ready, err := s.publishedVisibilityClass(library, r)
	if err != nil {
		return 0, 0, err
	}
	if !ready {
		return 0, 0, ErrVisibilityBuilding
	}
	var class, built, current int64
	err = s.read().QueryRow(`SELECT c.id,c.catalog_revision,lr.revision FROM compact_visibility_classes c JOIN catalog_libraries l ON l.id=c.library_id JOIN library_revisions lr ON lr.library_id=l.library_id WHERE c.class_key=? AND l.library_id=? AND c.active_generation=? AND c.retired=0`, key, library, generation).Scan(&class, &built, &current)
	if err != nil {
		return 0, 0, err
	}
	_, _ = built, current // a class one revision behind still serves; rows are rechecked

	return class, generation, nil
}
