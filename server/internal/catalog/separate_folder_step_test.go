package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// These fixtures install the real baseline and retain all production triggers.
// Three original children each have a file in the selected folder and another
// elsewhere, matching the previously failing SeparateFolder baseline shape.
type separateFolderStepFixture struct {
	c           *catalogtest.Catalog
	requests    []SeparateFolderFileRequest
	outside     []int64
	new, old    int64
	table       string
	parent      string
	number      string
	library     string
	libraryKind string
}

func newSeparateFolderStepFixture(t *testing.T, book bool) separateFolderStepFixture {
	t.Helper()
	c := catalogtest.Open(t)
	f := separateFolderStepFixture{c: c, table: "catalog_songs", parent: "album_id", number: "track_number", library: "music", libraryKind: "music"}
	root := "/music"
	if book {
		f.table, f.parent, f.number, f.library, f.libraryKind, root = "catalog_book_files", "book_id", "part_number", "books", "audiobook", "/books"
	}
	library := c.Library(f.library, f.library, f.libraryKind, root)
	var source, incarnation, rootIdentity string
	var generation int64
	if err := c.DB.QueryRow(`SELECT id,generation,incarnation,root_identity FROM library_sources WHERE library_id=?`, f.library).Scan(&source, &generation, &incarnation, &rootIdentity); err != nil {
		t.Fatal(err)
	}
	var old catalogtest.Item
	var parent int64
	kind := compactcatalog.Album
	var facts map[string]any
	if book {
		old = c.Book(library, "Book", "Author")
		kind = compactcatalog.Book
		facts = map[string]any{"library_id": library, "local_key": "book#separate", "author": "Author", "narrator": "Narrator"}
	} else {
		artist := c.Artist(library, "Artist")
		parent = artist.ID
		old = c.Album(artist, "Release", 2020)
		facts = map[string]any{"artist_id": parent, "local_key": "release#separate", "edition_key": "edition"}
	}
	f.old = old.ID
	newContainer := c.Entity(compactcatalog.Entity{Library: library, Kind: kind, Parent: parent, Key: compactcatalog.ItemKey(root, source+"/split", 0) + "#separate", Title: "Separated"}, facts)
	f.new = newContainer.ID
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return KeepApartTx(ctx, tx, f.new) })
	for n := 1; n <= 3; n++ {
		path := fmt.Sprintf("%s/elsewhere/%02d.mp3", root, n)
		var original catalogtest.Item
		if book {
			original = c.BookFile(old, n, path)
		} else {
			original = c.Song(old, n, path, fmt.Sprintf("Song %d", n))
		}
		c.Fields(original.ID, map[string]any{"disc_number": 2})
		inventoryFile(t, c, source, original, fmt.Sprintf("elsewhere/%02d.mp3", n), "elsewhere")
		path = fmt.Sprintf("%s/split/%02d.flac", root, n)
		asset, token := c.File(original.ID, path, 5400)
		object := inventoryFile(t, c, source, catalogtest.Item{Token: token}, fmt.Sprintf("split/%02d.flac", n), "split")
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			end := 50.0
			return compactcatalog.LinkAssetTx(ctx, tx, original.ID, asset, compactcatalog.Link{Part: 0, Start: 5, End: &end, Edition: "edition"})
		})
		c.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES(?,?,'title','embedded',?)`, f.library, token, fmt.Sprintf("Source %d", n))
		if book {
			c.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES(?,?,'series','embedded','Series'),(?,?,'series_position','embedded','2')`, f.library, token, f.library, token)
		}
		end := 50.0
		f.requests = append(f.requests, SeparateFolderFileRequest{
			Library: f.library, Object: object, Revision: "r1", Source: source, Directory: "split",
			SourceGeneration: generation, SourceIncarnation: incarnation, RootIdentity: rootIdentity, AssetPath: path,
			OldContainer: old.ID, NewContainer: f.new, Child: original.ID, Asset: asset, HasElsewhere: true,
			Link:   compactcatalog.Link{Part: 0, Start: 5, End: &end, Edition: "edition"},
			Before: func(context.Context, *sql.Tx) error { return nil },
		})
		f.outside = append(f.outside, original.Asset)
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,rating,revision) VALUES('p',?,1,8,3)`, original.ID)
	}
	c.Drain()
	return f
}

func separateFolderStepRun(t *testing.T, f separateFolderStepFixture, req SeparateFolderFileRequest) (SeparateFolderFileResult, int64, error) {
	t.Helper()
	var result SeparateFolderFileResult
	var changes int64
	err := dbwork.WithWriteTx(context.Background(), f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var before, after int64
		if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			return err
		}
		var err error
		result, err = SeparateFolderFileTx(context.Background(), tx, req)
		if err != nil {
			return err
		}
		if err = tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
			return err
		}
		changes = after - before
		return nil
	})
	t.Logf("one SeparateFolder file: rows=%d; default interactive rows=%d (pages/VM require dbwork telemetry)", changes, dbwork.DefaultPlan(dbwork.ClassInteractive).Rows)
	return result, changes, err
}

func separateFolderStepSnapshot(t *testing.T, c *catalogtest.Catalog, tables ...string) []string {
	t.Helper()
	var out []string
	for _, table := range tables {
		r, err := c.DB.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := r.Columns()
		if err != nil {
			r.Close()
			t.Fatal(err)
		}
		for r.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = r.Scan(dest...); err != nil {
				r.Close()
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s:%#v", table, values))
		}
		err = r.Err()
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

func TestSeparateFolderFileThreeFilesAndElsewhere(t *testing.T) {
	for _, book := range []bool{false, true} {
		t.Run(fmt.Sprintf("book=%v", book), func(t *testing.T) {
			f := newSeparateFolderStepFixture(t, book)
			sourceBefore := separateFolderStepSnapshot(t, f.c, "catalog_assets", "inventory_objects", "audio_tag_evidence")
			personalBefore := separateFolderStepSnapshot(t, f.c, "personal_items")
			for i, req := range f.requests {
				called := 0
				req.Before = func(_ context.Context, tx *sql.Tx) error {
					called++
					var current int64
					return tx.QueryRow(`SELECT id FROM catalog_entities WHERE id=? AND parent_id=?`, req.Child, req.OldContainer).Scan(&current)
				}
				got, changes, err := separateFolderStepRun(t, f, req)
				if err != nil || got.Child == 0 || got.Child == req.Child || got.Repeated || called != 1 {
					t.Fatalf("file %d: result=%+v authority calls=%d err=%v", i, got, called, err)
				}
				if changes > dbwork.DefaultPlan(dbwork.ClassInteractive).Rows {
					t.Errorf("one file exceeds existing 160-row plan: %d; unlink/insert trigger closure plus child/facts must fit without a plan raise", changes)
				}
				if n := rows(t, f.c, `SELECT count(*) FROM `+f.table+` f JOIN catalog_entities e ON e.id=f.entity_id JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_kept_apart k ON k.entity_id=e.id WHERE f.entity_id=? AND f.`+f.parent+`=? AND f.disc_number=2 AND f.`+f.number+`=? AND e.parent_id=? AND l.asset_id=? AND l.part_index=0 AND l.start_seconds=5 AND l.end_seconds=50 AND l.edition='edition'`, got.Child, f.new, i+1, f.new, req.Asset); n != 1 {
					t.Fatal("new child lost parent, KeepApart, ordering or link facts")
				}
				if n := rows(t, f.c, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, req.Child, f.outside[i]); n != 1 {
					t.Fatal("elsewhere file moved")
				}
				if n := rows(t, f.c, `SELECT count(*) FROM `+f.table+` WHERE `+f.parent+`=?`, f.new); n != i+1 {
					t.Fatal("one call processed more than the selected file")
				}
				repeat, repeatChanges, err := separateFolderStepRun(t, f, req)
				if err != nil || repeat.Child != got.Child || !repeat.Repeated || repeatChanges != 0 || called != 2 {
					t.Fatalf("restart/repeat: %+v changes=%d calls=%d err=%v", repeat, repeatChanges, called, err)
				}
			}
			if got := separateFolderStepSnapshot(t, f.c, "catalog_assets", "inventory_objects", "audio_tag_evidence"); !reflect.DeepEqual(sourceBefore, got) {
				t.Fatal("source facts changed")
			}
			if got := separateFolderStepSnapshot(t, f.c, "personal_items"); !reflect.DeepEqual(personalBefore, got) {
				t.Fatal("personal state moved or changed")
			}
			if book {
				f.c.Drain()
				if n := rows(t, f.c, `SELECT count(*) FROM catalog_book_series_contrib WHERE book_id=?`, f.old); n != 0 {
					t.Fatal("old book retained evidence from removed folder assets")
				}
				if n := rows(t, f.c, `SELECT count(*) FROM catalog_book_series_contrib WHERE book_id=? AND norm_key='series'`, f.new); n != 3 {
					t.Fatal("separated book lost source series evidence")
				}
			}
			// Exercise the actual scanner's manual-version path with tags that
			// would otherwise choose the old album/book's grouping again.
			s := New(f.c.DB)
			for i, req := range f.requests {
				var token string
				if err := f.c.DB.QueryRow(`SELECT token FROM catalog_assets WHERE id=?`, req.Asset).Scan(&token); err != nil {
					t.Fatal(err)
				}
				f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
					return s.commitInventoryAudioTx(ctx, tx, LibrarySource{ID: req.Source, LibraryID: req.Library, ResolvedPath: "/" + req.Library}, token, f.libraryKind, req.AssetPath, assets.Facts{Tags: map[string]string{"title": fmt.Sprintf("Source %d", i+1), "album": "Release", "artist": "Artist"}})
				})
				if n := rows(t, f.c, `SELECT count(*) FROM inventory_version_links v JOIN catalog_asset_links l ON l.entity_id=v.item_id JOIN catalog_assets a ON a.id=l.asset_id JOIN `+f.table+` f ON f.entity_id=l.entity_id WHERE v.library_id=? AND v.asset_id=? AND a.token=v.asset_id AND f.`+f.parent+`=?`, req.Library, token, f.new); n != 1 {
					t.Fatal("rescan undid manual split")
				}
			}
		})
	}
}

func TestSeparateFolderFileRefusalsAndRollback(t *testing.T) {
	f := newSeparateFolderStepFixture(t, false)
	req := f.requests[0]
	tables := []string{"catalog_entities", "catalog_identities", "catalog_songs", "catalog_asset_links", "catalog_kept_apart", "inventory_version_links", "library_sources", "personal_items"}
	before := separateFolderStepSnapshot(t, f.c, tables...)
	denied := errors.New("parent authority revoked")
	req.Before = func(_ context.Context, tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO catalog_kept_apart(entity_id) VALUES(?)`, f.old); err != nil {
			return err
		}
		return denied
	}
	if _, _, err := separateFolderStepRun(t, f, req); !errors.Is(err, denied) {
		t.Fatal("authority refusal was not returned", err)
	}
	if got := separateFolderStepSnapshot(t, f.c, tables...); !reflect.DeepEqual(before, got) {
		t.Fatal("authority callback's writes escaped rollback")
	}
	req.Before = func(_ context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, req.Source)
		return err
	}
	if _, _, err := separateFolderStepRun(t, f, req); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("source changed after authority check was accepted", err)
	}
	if got := separateFolderStepSnapshot(t, f.c, tables...); !reflect.DeepEqual(before, got) {
		t.Fatal("source-change refusal escaped rollback")
	}
	req = f.requests[0]
	err := dbwork.WithWriteTx(context.Background(), f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if _, err := SeparateFolderFileTx(context.Background(), tx, req); err != nil {
			return err
		}
		return denied // the parent cursor/publication failed after the file unit
	})
	if !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if got := separateFolderStepSnapshot(t, f.c, tables...); !reflect.DeepEqual(before, got) {
		t.Fatal("unlink/link/manual publication was not atomic with parent")
	}
	req.Before = nil
	if _, _, err = separateFolderStepRun(t, f, req); !errors.Is(err, ErrAdminQuery) {
		t.Fatal("missing authority callback accepted", err)
	}
}

func TestSeparateFolderFileRejectsChangedSourceAndAssociation(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 6.07s")
	}
	for name, mutate := range map[string]func(separateFolderStepFixture){
		"object revision": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id=?`, f.requests[0].Object)
		},
		"object folder": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE inventory_objects SET directory_path='other' WHERE id=?`, f.requests[0].Object)
		},
		"source generation": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.requests[0].Source)
		},
		"root incarnation": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE library_sources SET incarnation='changed' WHERE id=?`, f.requests[0].Source)
		},
		"source disabled": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, f.requests[0].Source)
		},
		"object retired": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE inventory_objects SET retired=1 WHERE id=?`, f.requests[0].Object)
		},
		"asset replaced": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE catalog_assets SET modified_ns=2 WHERE id=?`, f.requests[0].Asset)
		},
		"old parent": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE catalog_songs SET album_id=? WHERE entity_id=?`, f.new, f.requests[0].Child)
		},
		"child retired": func(f separateFolderStepFixture) {
			f.c.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=?`, f.requests[0].Child)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSeparateFolderStepFixture(t, false)
			mutate(f)
			before := separateFolderStepSnapshot(t, f.c, "catalog_entities", "catalog_identities", "catalog_songs", "catalog_asset_links", "inventory_version_links")
			if _, _, err := separateFolderStepRun(t, f, f.requests[0]); err == nil {
				t.Fatal("changed source/association accepted")
			}
			if got := separateFolderStepSnapshot(t, f.c, "catalog_entities", "catalog_identities", "catalog_songs", "catalog_asset_links", "inventory_version_links"); !reflect.DeepEqual(before, got) {
				t.Fatal("refusal changed catalog")
			}
		})
	}
}

func TestSeparateFolderFileReparentAndBookProjectionBlocker(t *testing.T) {
	for _, book := range []bool{false, true} {
		t.Run(fmt.Sprintf("book=%v", book), func(t *testing.T) {
			f := newSeparateFolderStepFixture(t, book)
			req := f.requests[0]
			req.HasElsewhere = false
			if book {
				if _, _, err := separateFolderStepRun(t, f, req); !errors.Is(err, ErrSeparateFolderFileProjection) {
					t.Fatal("unbounded book reprojector was not refused", err)
				}
			}
			// Parent has proved that the child's only remaining file is here.
			f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
				return compactcatalog.UnlinkAssetTx(ctx, tx, req.Child, f.outside[0])
			})
			got, changes, err := separateFolderStepRun(t, f, req)
			if err != nil || got.Child != req.Child || got.Repeated {
				t.Fatalf("reparent: %+v %v", got, err)
			}
			if changes > dbwork.DefaultPlan(dbwork.ClassInteractive).Rows {
				t.Errorf("one reparent exceeds existing 160-row plan: %d", changes)
			}
			if n := rows(t, f.c, `SELECT count(*) FROM `+f.table+` f JOIN catalog_entities e ON e.id=f.entity_id JOIN catalog_kept_apart k ON k.entity_id=e.id WHERE f.entity_id=? AND f.`+f.parent+`=? AND e.parent_id=? AND f.disc_number=2 AND f.`+f.number+`=1`, req.Child, f.new, f.new); n != 1 {
				t.Fatal("reparent facts or KeepApart missing")
			}
			repeat, changes, err := separateFolderStepRun(t, f, req)
			if err != nil || repeat.Child != req.Child || !repeat.Repeated || changes != 0 {
				t.Fatalf("reparent repeat: %+v %d %v", repeat, changes, err)
			}
		})
	}
}

func TestSeparateFolderFileReparentPublishesEachAlbumVersion(t *testing.T) {
	f := newSeparateFolderStepFixture(t, false)
	req := f.requests[0]
	req.HasElsewhere = false
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, req.Child, f.outside[0])
	})
	other := req
	other.AssetPath = "/music/split/another.flac"
	var token string
	other.Asset, token = f.c.File(req.Child, other.AssetPath, 5400)
	other.Object = inventoryFile(t, f.c, req.Source, catalogtest.Item{Token: token}, "split/another.flac", "split")
	other.Link = compactcatalog.Link{}
	for _, file := range []SeparateFolderFileRequest{req, other, other} {
		got, _, err := separateFolderStepRun(t, f, file)
		if err != nil || got.Child != req.Child {
			t.Fatal("same-folder versions must remain one original track", got, err)
		}
	}
	if n := rows(t, f.c, `SELECT count(*) FROM inventory_version_links WHERE library_id=? AND item_id=?`, req.Library, req.Child); n != 2 {
		t.Fatal("each file's owner decision was not published exactly once")
	}
}

func TestSeparateFolderFileHonorsExistingMutationCustody(t *testing.T) {
	f := newSeparateFolderStepFixture(t, false)
	req := f.requests[0]
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: f.old, OwnerKind: "title", OwnerID: "other-command", OwnerGeneration: 1, Mode: "remove"})
		return err
	})
	before := separateFolderStepSnapshot(t, f.c, "catalog_entities", "catalog_asset_links", "inventory_version_links")
	if _, _, err := separateFolderStepRun(t, f, req); !errors.Is(err, compactcatalog.ErrEntityMutating) {
		t.Fatal("competing actual mutation custody was bypassed", err)
	}
	if after := separateFolderStepSnapshot(t, f.c, "catalog_entities", "catalog_asset_links", "inventory_version_links"); !reflect.DeepEqual(before, after) {
		t.Fatal("mutation refusal changed catalog")
	}
}

func TestSeparateFolderFileVerifiesReplayLinkSnapshot(t *testing.T) {
	f := newSeparateFolderStepFixture(t, false)
	req := f.requests[0]
	got, _, err := separateFolderStepRun(t, f, req)
	if err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`UPDATE catalog_asset_links SET end_seconds=51 WHERE entity_id=? AND asset_id=?`, got.Child, req.Asset)
	if _, _, err = separateFolderStepRun(t, f, req); !errors.Is(err, compactcatalog.ErrMutationStale) {
		t.Fatal("changed manual split was returned as the original publication", err)
	}
}

func TestSeparateFolderFileNeverAdoptsAnotherTitle(t *testing.T) {
	f := newSeparateFolderStepFixture(t, false)
	req := f.requests[0]
	var root string
	if err := f.c.DB.QueryRow(`SELECT root FROM catalog_libraries WHERE library_id=?`, req.Library).Scan(&root); err != nil {
		t.Fatal(err)
	}
	wrong := f.c.Entity(compactcatalog.Entity{Library: f.c.Handle(req.Library), Kind: compactcatalog.Track, Parent: f.old, Key: compactcatalog.ItemKey(root, req.AssetPath, 0) + "#separate", Title: "Other title"}, map[string]any{"album_id": f.old, "track_number": 99})
	if _, _, err := separateFolderStepRun(t, f, req); !errors.Is(err, compactcatalog.ErrMutationStale) {
		t.Fatal("occupied identity was adopted", err)
	}
	f.c.Exec(`INSERT INTO inventory_version_links(library_id,asset_id,item_id) SELECT ?,token,? FROM catalog_assets WHERE id=?`, req.Library, wrong.ID, req.Asset)
	if _, _, err := separateFolderStepRun(t, f, req); !errors.Is(err, compactcatalog.ErrMutationStale) {
		t.Fatal("another manual title was accepted as a repeat", err)
	}
	if n := rows(t, f.c, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, req.Child, req.Asset); n != 1 {
		t.Fatal("original link changed on identity refusal")
	}
}

func TestSeparateFolderFileClaimsRemainByteForByteUnchanged(t *testing.T) {
	f := newSeparateFolderStepFixture(t, false)
	req := f.requests[0]
	var token string
	if err := f.c.DB.QueryRow(`SELECT token FROM catalog_assets WHERE id=?`, req.Asset).Scan(&token); err != nil {
		t.Fatal(err)
	}
	c := f.c
	c.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES('job',?,?,'music','p','target','{}','source-revision','{}','running','prepare',1,2)`, req.Child, token)
	c.Exec(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES('version',?,?,'music','p','target','source-revision','{}',0,'evidence','transform','digest',100,'{}','published',1,'job')`, req.Child, token)
	c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,source_asset_id,quality,origin,state,created_ms,updated_ms) VALUES('prep','p','local','account','p',?,?,'music',?,'original','item','ready',1,2)`, req.Child, c.Public(req.Child), token)
	c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms) VALUES('grant','prep','p',?,1,99)`, req.Child)
	c.Exec(`INSERT INTO download_receipt_keys(id,algorithm,private_key,public_key,created_ms) VALUES('key','ed25519',X'01',X'02',1)`)
	c.Exec(`INSERT INTO download_receipts(id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,preparation_id,quality,publication_required,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,sequence) VALUES('receipt','p','local','account','p',?,?,'music','prep','original',0,'digest',100,'key',1,99,1)`, req.Child, c.Public(req.Child))
	c.Exec(`INSERT INTO download_requests(id,profile_key,operation_id,request_hash,principal,device_id,target_kind,target_id,quality,episodes,keep_next,selection_fence,created_ms,updated_ms) VALUES('request','p','operation','hash','{}','device','container',?,'original','all',0,'fence',1,2)`, f.old)
	c.Exec(`INSERT INTO download_request_items(request_id,ordinal,item_id,preparation_id) VALUES('request',0,?,'prep')`, req.Child)
	tables := []string{"prepared_media_jobs", "prepared_media_versions", "download_preparations", "download_grants", "download_receipts", "download_requests", "download_request_items"}
	before := separateFolderStepSnapshot(t, c, tables...)
	got, _, err := separateFolderStepRun(t, f, req)
	if err != nil || got.Child == req.Child {
		t.Fatal(got, err)
	}
	if after := separateFolderStepSnapshot(t, c, tables...); !reflect.DeepEqual(before, after) {
		t.Fatal("split migrated, repointed or changed an old download/prepared-media claim")
	}
}
