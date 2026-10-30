package dbwork

import (
	"database/sql"
	"errors"
)

// RequireGatedWriteTx rejects snapshots, untracked transactions and finished
// writers before a private accepting store can mutate its publication rows.
// It neither opens a transaction nor establishes actor/source authority.
func RequireGatedWriteTx(tx *sql.Tx) error {
	value, ok := liveWrites.Load(tx)
	if !ok {
		return errors.New("dbwork: accepting publication requires a live gated writer")
	}
	w := value.(*Write)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return errors.New("dbwork: accepting publication writer already finished")
	}
	return nil
}
