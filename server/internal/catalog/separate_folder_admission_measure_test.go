package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

func TestSeparateFolderAdmissionMeasure(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: large-folder admission measurement; measured normal parent 12.97s")
	}
	for _, n := range []int{500, 3998} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			start := time.Now()
			c, object := newSeparateFolderAdmissionScale(t, n, false)
			t.Logf("fixture links=%d children=%d source_objects=%d setup=%s (no derived drain required for raw proof)", n, n, n, time.Since(start))

			c.Exec("INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('admission-owner','admission-owner',X'00','admission-profile')")
			base := WithSeparateFolderActor(context.Background(), identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "admission-owner", ProfileID: "admission-profile", Role: identity.TierOwner}})
			s := New(c.DB)
			readCtx, readCost := dbwork.Measure(base)
			start = time.Now()
			p, err := s.separateFolderProof(readCtx, "music", object, "r1", func(*sql.Tx) error { return nil })
			readElapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cost := dbwork.Measure(base)
			ctx = context.WithValue(ctx, separateFolderProofKey{}, p)
			dbwork.ResetPlans()
			start = time.Now()
			err = dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				_, _, _, err := separateFolderAdmitTx(ctx, tx, "music", object, "r1")
				return err
			})
			record := map[string]any{"links": n, "children": n, "read_elapsed_ns": readElapsed.Nanoseconds(), "read_cost": readCost(), "writer_elapsed_ns": time.Since(start).Nanoseconds(), "writer_cost": cost(), "plans": dbwork.Plans(), "writer_error": fmt.Sprint(err)}
			b, _ := json.Marshal(record)
			t.Log(string(b))
			if err != nil {
				t.Fatal(err)
			}
			if dbwork.Plans().Overruns != 0 {
				t.Fatal("complete writer admission exceeded unchanged plan")
			}
		})
	}
}

func newSeparateFolderAdmissionScale(t *testing.T, n int, two bool) (*catalogtest.Catalog, string) {
	t.Helper()
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Release", 2020)
	var source, inc string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='music'`).Scan(&source, &inc); err != nil {
		t.Fatal(err)
	}
	// Two links per child; each child has one selected-folder file and one elsewhere.
	// One child per fixture batch: real compactcatalog API, baseline and triggers.
	children := n
	if two {
		children = n / 2
	}
	for i := 0; i < children; i++ {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			child, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Track, Parent: album.ID, Key: fmt.Sprintf("probe-%d", i), Title: fmt.Sprintf("Track %d", i)})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, child, map[string]any{"album_id": album.ID, "track_number": i + 1}); err != nil {
				return err
			}
			dir := "selected"
			if i%2 == 1 {
				dir = "elsewhere"
			}
			dirs := []string{dir}
			if two {
				dirs = []string{"selected", "elsewhere"}
			}
			for _, dir := range dirs {
				rel := fmt.Sprintf("%s/%04d.flac", dir, i)
				a, token, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/music/" + rel, Size: 1000, ModifiedNS: 1, Container: "flac", AudioCodec: "flac", Duration: 180})
				if e != nil {
					return e
				}
				if e = compactcatalog.LinkAssetTx(ctx, tx, child, a, compactcatalog.Link{}); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,directory_path) VALUES(?,?,?, ?,?,'r1','{}',1000,1,?)`, "obj-"+token, source, token, inc, rel, dir); e != nil {
					return e
				}
			}
			return nil
		})
	}

	var object string
	if err := c.DB.QueryRow("SELECT id FROM inventory_objects WHERE directory_path='selected' ORDER BY id LIMIT 1").Scan(&object); err != nil {
		t.Fatal(err)
	}
	return c, object
}
