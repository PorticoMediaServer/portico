package db_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Making and deleting annotations in a loop cannot fill the database: a book keeps the markers
// of its newest 5,000 deletions for a profile. A very late edit to one deleted before those
// comes back; one to a newer deletion is still refused.
func TestABookKeepsAtMost5000DeletionMarkers(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 11,000 annotation edits, about a second and a half")
	}
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := seed.Import(ctx, d, seed.Config{Seed: 3, Items: 300}, nil); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	v := viewers[0]
	book := ""
	libraries, _ := d.AllLibraries(ctx)
	for _, lib := range libraries {
		if page, err := d.Items(ctx, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindBook}, Limit: 1}); err == nil && len(page.Items) == 1 {
			book = page.Items[0].PublicID
		}
	}
	if book == "" {
		t.Fatal("the seeded library has no book")
	}
	edit := func(id string, del bool, ago time.Duration) db.AnnotationEdit {
		return db.AnnotationEdit{ItemID: book, Delete: del, Ago: ago,
			Annotation: db.Annotation{PublicID: id, Kind: db.AnnotationBookmark, Location: "x", Progress: 0.5}}
	}
	// 5,500 made and deleted, in rounds the 5,000 live annotations a book holds allow.
	const deleted, round = 5500, 2750
	for start := 0; start < deleted; start += round {
		var made, gone []db.AnnotationEdit
		for n := start; n < start+round; n++ {
			made, gone = append(made, edit(fmt.Sprint("a", n), false, 0)), append(gone, edit(fmt.Sprint("a", n), true, 0))
		}
		for _, batch := range [][]db.AnnotationEdit{made, gone} {
			if _, refused, err := d.SaveAnnotations(ctx, v, batch); err != nil || refused[0] != nil {
				t.Fatalf("a round answered %v, %v", err, refused[0])
			}
		}
	}
	// The next write drops the markers past the newest 5,000. An edit made a minute ago reaches
	// the first deletion (whose marker is gone) and the last (whose marker is kept).
	_, refused, err := d.SaveAnnotations(ctx, v, []db.AnnotationEdit{edit("a0", false, time.Minute), edit(fmt.Sprint("a", deleted-1), false, time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if refused[0] != nil || !errors.Is(refused[1], db.ErrAnnotationDeleted) {
		t.Fatalf("after %d deletions: an edit to the first answered %v, to the last %v", deleted, refused[0], refused[1])
	}
}
