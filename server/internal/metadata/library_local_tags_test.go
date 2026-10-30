package metadata

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"testing"
)

func TestLibraryLocalTagsOffStopsNFORead(t *testing.T) {
	_, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	nfo, err := localmetadata.ParseVideoNFO([]byte("<movie><title>Local name</title><outline>Local story</outline></movie>"), false)
	if err != nil {
		t.Fatal(err)
	}
	nfo[0].Locator = "fixture.nfo"
	size, modified := publicationAssetStat(t, db, item)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = localmetadata.PersistVideo(ctx, tx, "lib", item.Token, assets.Facts{VideoMetadata: nfo, ObservedSize: size, ObservedModifiedNS: modified}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	on, err := readScreenBase(ctx, tx, "item", item.Public)
	tx.Rollback()
	if err != nil || !on.LocalAllowed || len(on.NFO) == 0 {
		t.Fatal("local evidence absent", on.LocalAllowed, len(on.NFO), err)
	}
	publicationExec(t, db, `INSERT INTO library_preferences(library_id,local_tags) VALUES('lib','off')`)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	off, err := readScreenBase(ctx, tx, "item", item.Public)
	tx.Rollback()
	if err != nil || off.LocalAllowed || len(off.NFO) != 0 {
		t.Fatal("off ignored", off.LocalAllowed, len(off.NFO), err)
	}
}
