package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func persistenceWrite(t *testing.T, db *sql.DB, fn func(context.Context, *sql.Tx) error) {
	t.Helper()
	if err := dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassMaintenance, fn); err != nil {
		t.Fatal(err)
	}
}

func persistenceLibrary(t *testing.T, db *sql.DB, id, name, kind, root string) int64 {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, id, name, kind, root); err != nil {
		t.Fatal(err)
	}
	var library int64
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		library, err = compactcatalog.LibraryTx(ctx, tx, id)
		return err
	})
	return library
}

func persistenceMovieTx(ctx context.Context, tx *sql.Tx, library int64, root, path, title string, year int) (int64, int64, string, error) {
	item, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
		Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0),
		Title: title, Year: year, Added: "2026-01-01T00:00:00.000Z",
	})
	if err != nil {
		return 0, 0, "", err
	}
	asset, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{
		Path: path, Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60,
	})
	if err != nil {
		return 0, 0, "", err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); err != nil {
		return 0, 0, "", err
	}
	return item, asset, token, nil
}

func persistenceMovie(t *testing.T, db *sql.DB, library int64, root, relative, title string, year int) (int64, int64, string) {
	t.Helper()
	path := root + "/" + relative
	var item, asset int64
	var token string
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		item, asset, token, err = persistenceMovieTx(ctx, tx, library, root, path, title, year)
		return err
	})
	return item, asset, token
}

func persistenceShowTx(ctx context.Context, tx *sql.Tx, library int64, localKey, title string, year int) (int64, error) {
	show, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
		Library: library, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(localKey), Title: title, Year: year,
	})
	if err != nil {
		return 0, err
	}
	return show, compactcatalog.SetFieldsTx(ctx, tx, show, compactcatalog.Automatic, map[string]any{"local_key": localKey})
}

func persistenceEpisodeTx(ctx context.Context, tx *sql.Tx, library, show int64, showKey string, seasonNumber, episodeNumber int, title string) (int64, error) {
	seasonID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
		Library: library, Kind: compactcatalog.Season, Parent: show, Key: compactcatalog.SeasonKey(showKey, seasonNumber), Title: fmt.Sprintf("Season %d", seasonNumber),
	})
	if err != nil {
		return 0, err
	}
	if err = compactcatalog.SetFieldsTx(ctx, tx, seasonID, compactcatalog.Automatic, map[string]any{"show_id": show, "number": seasonNumber}); err != nil {
		return 0, err
	}
	episode, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
		Library: library, Kind: compactcatalog.Episode, Parent: seasonID,
		Key: compactcatalog.EpisodeKey(showKey, "seasonal", seasonNumber, episodeNumber), Title: title,
	})
	if err != nil {
		return 0, err
	}
	err = compactcatalog.SetFieldsTx(ctx, tx, episode, compactcatalog.Automatic, map[string]any{
		"show_id": show, "season_id": seasonID, "numbering": "seasonal", "number": episodeNumber, "local_identity_status": "parsed",
	})
	return episode, err
}

func persistenceDrain(t *testing.T, db *sql.DB) {
	t.Helper()
	worker := compactcatalog.NewWorker(db)
	for i := 0; i < 10000; i++ {
		n, err := worker.Step(context.Background(), 500)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("catalogue worker did not drain")
}
