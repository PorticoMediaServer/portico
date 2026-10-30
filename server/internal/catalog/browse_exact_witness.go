package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// browseExactWitness captures the shared exact-browse basis in the caller's
// read snapshot or carried writer. It is not query identity or authority: callers must separately
// bind their canonical query, evaluation instant, and live authorization.
// Missing inputs are errors, never a reusable empty/legacy witness. The global
// predicate epoch deliberately over-invalidates unrelated libraries and policies.
func (s *Service) browseExactWitness(library, profile string, restrictions identity.ContentRestrictions) (string, error) {
	if dbwork.Snapshot(s.Context()) == nil && !dbwork.InWriteTx(s.Context()) && s.readTx == nil {
		return "", fmt.Errorf("exact browse witness requires a carried transaction")
	}
	var basis struct {
		Version                                                                         string
		Library, Profile                                                                string
		LibraryID, Raw, Order, Viewer, Generation, Retaining, Predicate, Classification int64
		Policy                                                                          identity.ContentRestrictions
		Domains                                                                         [][6]int64
		Reading, Formats                                                                string
		Schema                                                                          int64
	}
	basis.Version, basis.Library, basis.Profile, basis.Policy = "exact-browse-v1", library, profile, restrictions
	err := s.read().QueryRow(`SELECT l.id,r.revision,r.order_revision,COALESCE(v.revision,0),g.generation,g.retaining,p.revision,c.revision
 FROM catalog_libraries l JOIN library_revisions r ON r.library_id=l.library_id
 JOIN catalog_generations g ON g.singleton=1
 JOIN catalog_browse_predicate_epoch p ON p.singleton=1
 JOIN catalog_summary_classification_revision c ON c.singleton=1
 LEFT JOIN viewer_revisions v ON v.library_id=l.library_id AND v.profile_id=?
 WHERE l.library_id=? AND l.retired=0`, profile, library).Scan(&basis.LibraryID, &basis.Raw, &basis.Order, &basis.Viewer, &basis.Generation, &basis.Retaining, &basis.Predicate, &basis.Classification)
	if err != nil {
		return "", err
	}
	// A cold reducer uses a physical integer inventory cursor to avoid retaining
	// arbitrarily large object keys. VACUUM can renumber hidden rowids; SQLite
	// advances schema_version when it rebuilds those tables. Ordinary inventory
	// insert/delete/key changes already advance the synchronous predicate epoch.
	if err = s.read().QueryRow(`PRAGMA schema_version`).Scan(&basis.Schema); err != nil {
		return "", err
	}
	if basis.Raw <= 0 || basis.Order <= 0 || basis.Generation <= 0 || basis.Predicate <= 0 || basis.Classification <= 0 {
		return "", fmt.Errorf("exact browse witness has unproved revisions")
	}
	// These are fixed-size domain metadata, never a walk of dirty entities.
	// Rebuilds remain readable through the existing serving views; capture the
	// actual readiness/slot state rather than assuming CheckReadiness proved it.
	rows, err := s.read().Query(`SELECT domain,version,rebuilding,active_slot,cleaning,first_build FROM catalog_derivations ORDER BY domain`)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var d [6]int64
		if err = rows.Scan(&d[0], &d[1], &d[2], &d[3], &d[4], &d[5]); err != nil {
			rows.Close()
			return "", err
		}
		basis.Domains = append(basis.Domains, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, required := range []int64{18, 19, 20} {
		found := false
		for _, d := range basis.Domains {
			if d[0] == required && d[5] != 0 {
				return "", ErrVisibilityBuilding
			}
			found = found || d[0] == required && d[1] > 0
		}
		if !found {
			return "", fmt.Errorf("exact browse witness missing domain %d", required)
		}
	}
	reading, err := s.readingBrowseScope(library)
	if err != nil {
		return "", err
	}
	if reading != nil {
		basis.Reading, basis.Formats = reading.fence(), reading.sourceFormatsSQL()
	}
	raw, err := json.Marshal(basis)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "exact_" + hex.EncodeToString(digest[:]), nil
}
