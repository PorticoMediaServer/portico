package metadata

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestMetadataProgressIgnoresOtherLanesButFollowsItsOwnBacklog(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`CREATE TABLE progress_probe(value INTEGER)`)
	ctx, progress := metadataProgress(context.Background())
	other, otherProgress := metadataProgress(context.Background())
	write := func(ctx context.Context, value int) {
		t.Helper()
		if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { _, err := tx.Exec(`INSERT INTO progress_probe VALUES(?)`, value); return err }); err != nil {
			t.Fatal(err)
		}
	}
	progress.Reset()
	otherProgress.Reset()
	for i := 0; i < 10; i++ {
		write(other, i)
	}
	if progress.Moved() {
		t.Fatal("idle artwork lane scheduled rapid retry for unrelated scan commits")
	}
	if !otherProgress.Moved() {
		t.Fatal("productive lane lost its backlog retry")
	}
	write(ctx, 11)
	if !progress.Moved() {
		t.Fatal("own changing transaction did not schedule backlog retry")
	}
	if progress.Moved() {
		t.Fatal("settled pass retained a rapid retry")
	}
	if otherProgress.Moved() {
		t.Fatal("own progress leaked into another artwork lane")
	}
	if _, err := dbwork.ExecWrite(ctx, c.DB, dbwork.ClassBackgroundMedia, `UPDATE progress_probe SET value=0 WHERE 0`); err != nil {
		t.Fatal(err)
	}
	if progress.Moved() {
		t.Fatal("empty pass scheduled rapid retry")
	}
}
