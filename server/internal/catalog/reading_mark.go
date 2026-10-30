package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// ReadingMark is an explicit profile choice, independent of a saved location.
type ReadingMark struct {
	ItemID   string `json:"itemId"`
	Read     bool   `json:"read"`
	Revision int64  `json:"revision"`
}

func readingPersonalTarget(tx *sql.Tx, entity int64) error {
	var found int
	return tx.QueryRow(`SELECT 1 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind
 JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id
 WHERE e.id=? AND e.retired=0 AND cl.retired=0
 AND ((k.name='ebook' AND l.kind='book') OR (k.name='comic' AND l.kind='comic'))`, entity).Scan(&found)
}

// The caller must re-admit the original principal and exact target inside the
// same snapshot. A nil guard never grants access to personal Reading state.
func (s *Service) readingPersonal(profile, item string, authorize func(*sql.Tx) error) (PersonalState, error) {
	var out PersonalState
	if authorize == nil {
		return out, sql.ErrNoRows
	}
	err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		if err := authorize(tx); err != nil {
			return err
		}
		entity, err := personalID(tx, item)
		if err != nil {
			return err
		}
		if err = readingPersonalTarget(tx, entity); err != nil {
			return err
		}
		out, err = readPersonalByID(tx, profile, entity, personalstate.SQL("?", "?"))
		return err
	})
	return out, err
}
func (s *Service) ReadingMark(profile, item string, authorize func(*sql.Tx) error) (ReadingMark, error) {
	state, err := s.readingPersonal(profile, item, authorize)
	if err != nil {
		return ReadingMark{}, err
	}
	return ReadingMark{ItemID: item, Read: state.Watched, Revision: state.Revision}, nil
}

// Reuse the original CAS, immutable-operation receipts and explicit watched
// intent. This narrow entry cannot author resume seconds, offline evidence,
// conflict resolutions, AV targets or any other personal field.
func (s *Service) SetReadingMark(account, profile, item, operation string, revision int64, read bool, authorize func(*sql.Tx) error) (ReadingMark, error) {
	if authorize == nil {
		return ReadingMark{}, sql.ErrNoRows
	}
	state, err := s.setPersonal(account, profile, item, PersonalMutation{OperationID: operation, ExpectedRevision: revision, Watched: &read}, authorize, true)
	if err != nil {
		return ReadingMark{}, err
	}
	return ReadingMark{ItemID: item, Read: state.Watched, Revision: state.Revision}, nil
}
