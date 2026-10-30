package catalogtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
)

// PinMBRelease seeds an accepted fixture pin through the real staged accounting
// coordinator, with every installed trigger/FK guard enabled. Construction is
// outside the measured domain writers. It does not grant publication authority
// or add a budget policy: tests retain their explicit cap/admission assertions.
func (c *Catalog) PinMBRelease(entity int64, album bool, release string) {
	c.T.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		change := mbaccounting.Change{Owner: mbaccounting.Owner{Kind: "publication", Operation: "catalog-fixture", Generation: 1}, ID: fmt.Sprintf("fixture-%d", entity), Entity: entity, Link: "song", Digest: fmt.Sprintf("fixture-%d-%s", entity, release), NewRelease: release}
		query := `SELECT COALESCE(recording_revision,''),COALESCE(release_revision,'') FROM mb_song_links WHERE item_id=?`
		var e error
		if album {
			change.Link = "album"
			e = tx.QueryRowContext(ctx, `SELECT release_revision FROM mb_album_links WHERE album_id=?`, entity).Scan(&change.OldRelease)
		} else {
			e = tx.QueryRowContext(ctx, query, entity).Scan(&change.OldRecording, &change.OldRelease)
			change.NewRecording = change.OldRecording
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, e = mbaccounting.BeginTx(ctx, tx, change); e != nil {
			return e
		}
		const maxTurns = 20002/mbaccounting.FactsPerTurn + 8
		ready := false
		for n := 0; n < maxTurns && !ready; n++ {
			ready, e = mbaccounting.PrepareTx(ctx, tx, change)
			if e != nil {
				return e
			}
		}
		if !ready {
			return errors.New("fixture accounting preparation did not finish")
		}
		e = mbaccounting.FlipTx(ctx, tx, change, func() error {
			if album {
				_, e := tx.ExecContext(ctx, `INSERT INTO mb_album_links(album_id,release_revision,release_id,requested_id,observed_at) VALUES(?,?,?,?,'now')`, entity, release, release, release)
				return e
			}
			_, e := tx.ExecContext(ctx, `INSERT INTO mb_song_links(item_id,release_revision,release_id,observed_at) VALUES(?,?,?,'now')`, entity, release, release)
			return e
		}, func() error { return nil })
		if e != nil {
			return e
		}
		for n := 0; n < maxTurns; n++ {
			done, e := mbaccounting.FoldTx(ctx, tx)
			if e != nil {
				return e
			}
			if done {
				return nil
			}
		}
		return errors.New("fixture accounting fold did not finish")
	})
}
