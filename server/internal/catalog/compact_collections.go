package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Facts are written synchronously, so a collection or playlist head is
// readable as soon as it is live.
const collectionHeadFence = `e.retired=0`

const playlistHeadFence = `p.deleted=0`

// Restricted counts serve the last published policy generation while its
// contributions catch up. Each probe is indexed; request paths do not count
// whole collection memberships.
func (s *Service) collectionClassReady(library string, r identity.ContentRestrictions) (int64, int64, error) {
	key, generation, ready, err := s.publishedVisibilityClass(library, r)
	if err != nil {
		return 0, 0, err
	}
	if !ready {
		return 0, 0, ErrVisibilityBuilding
	}
	var class int64
	err = s.read().QueryRow(`SELECT c.id FROM compact_visibility_classes c JOIN catalog_libraries l ON l.id=c.library_id WHERE c.class_key=? AND l.library_id=? AND c.active_generation=? AND c.retired=0`, key, library, generation).Scan(&class)
	if err != nil {
		return 0, 0, err
	}

	return class, generation, nil
}
