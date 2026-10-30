package catalog

import (
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// bulkSelectionFence extends the existing job fence with exactly the same
// complete predicate/serving witness used by exact browse. Admission's read and
// accepting writer, every capture read and writer, and the final empty capture
// all call this method. A changed or missing witness fails before applying keys.
// This changes no command authority: admission/apply still use their live hooks.
func (s *Service) bulkSelectionFence(tx *sql.Tx, v Viewer, selector JobSelector) (string, error) {
	ctx, err := dbwork.BindSnapshot(s.Context(), tx)
	if err != nil {
		return "", err
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	read := &clone
	legacy, err := bulkSelectionRevisionFence(tx, v, selector)
	if err != nil {
		return "", err
	}
	// Walk only the authorized library metadata, in stable order. The job
	// already stores this bounded scope; no entity or query result is retained.
	rows, err := tx.QueryContext(ctx, `SELECT l.library_id FROM library_revisions l WHERE l.library_id IN(SELECT value FROM json_each(?)) ORDER BY l.library_id`, v.librariesJSON())
	if err != nil {
		return "", err
	}
	libraries := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		libraries = append(libraries, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	values := []any{legacy, "exact-bulk-v1"}
	for _, library := range libraries {
		witness, err := read.browseSelectionWitness(library, v.Profile, v.EffectiveRestrictions())
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSelectionBatchStale
		}
		if err != nil {
			return "", err
		}
		if witness == "" {
			return "", ErrSelectionBatchStale
		}
		values = append(values, []string{library, witness})
	}
	return operationHash(values), nil
}
