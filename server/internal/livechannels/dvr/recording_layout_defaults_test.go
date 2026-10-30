package dvr

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestRecordingLayoutReadsActualDefaultsAndCallerTransaction(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "recording-layout.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	read := func() RecordingLayout {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		out, err := ReadRecordingLayoutTx(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	initial := read()
	if initial.Revision < 1 || initial.ShowTemplate != "" || initial.MovieTemplate != "" {
		t.Fatalf("factory layout = %#v", initial)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	show, movie := "{series}/Season {season:02}/{series} S{season:02}E{episode:02}", "{title} ({year})/{title} ({year})"
	if _, err = tx.ExecContext(ctx, `UPDATE dvr_defaults SET revision=revision+1,show_folder_template=?,movie_folder_template=? WHERE singleton=1`, show, movie); err != nil {
		t.Fatal(err)
	}
	inside, err := ReadRecordingLayoutTx(ctx, tx)
	if err != nil || inside != (RecordingLayout{initial.Revision + 1, show, movie}) {
		t.Fatalf("caller snapshot = %#v, %v", inside, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := read(); after != initial {
		t.Fatalf("rolled-back layout escaped: %#v -> %#v", initial, after)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM dvr_defaults WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if out, err := ReadRecordingLayoutTx(ctx, tx); !errors.Is(err, sql.ErrNoRows) || out != (RecordingLayout{}) {
		t.Fatalf("missing policy must not select private layout: %#v, %v", out, err)
	}
}

func TestRecordingLayoutRefusesMissingTransaction(t *testing.T) {
	if out, err := ReadRecordingLayoutTx(context.Background(), nil); !errors.Is(err, ErrInvalid) || out != (RecordingLayout{}) {
		t.Fatalf("nil transaction = %#v, %v", out, err)
	}
}
