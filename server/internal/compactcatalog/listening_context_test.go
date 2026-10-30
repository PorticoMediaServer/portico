package compactcatalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Shared CC1 fixture helpers: facts are created through the write API
// (LibraryTx, UpsertEntityTx, SetFactsTx, UpsertAssetTx, LinkAssetTx) inside a
// dbwork transaction, exactly as production writers do, and derived work is
// drained with the Worker's Step until it returns 0.

func cc1OpenDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func cc1Exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func cc1Scalar(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func cc1Write(t *testing.T, db *sql.DB, fn func(context.Context, *sql.Tx) error) {
	t.Helper()
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		return fn(context.Background(), tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func cc1Drain(t *testing.T, db *sql.DB) {
	t.Helper()
	w := NewWorker(db)
	for i := 0; i < 500; i++ {
		n, err := w.Step(context.Background(), 500)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("catalogue worker did not drain")
}

func cc1Library(t *testing.T, db *sql.DB, id, name, kind, root string) int64 {
	t.Helper()
	cc1Exec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, id, name, kind, root)
	var handle int64
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		handle, err = LibraryTx(ctx, tx, id)
		return err
	})
	return handle
}

func cc1Entity(t *testing.T, db *sql.DB, e Entity, facts map[string]any) int64 {
	t.Helper()
	var id int64
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if id, _, err = UpsertEntityTx(ctx, tx, e); err != nil {
			return err
		}
		if len(facts) > 0 {
			return SetFactsTx(ctx, tx, id, facts)
		}
		return nil
	})
	return id
}

func cc1Class(t *testing.T, db *sql.DB, lib int64, key string) int64 {
	t.Helper()
	cc1Exec(t, db, `INSERT INTO compact_visibility_classes(class_key,library_id,policy_json,active_generation,catalog_revision,requested_ms,built_ms,retired) VALUES(?,?,'{}',1,0,0,0,0)`, key, lib)
	var id int64
	if err := db.QueryRow(`SELECT id FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func cc1Visible(t *testing.T, db *sql.DB, class, entity int64, kind int) {
	t.Helper()
	cc1Exec(t, db, `INSERT INTO compact_visibility_rows(class_id,generation,entity_id,library_id,kind,sort_key,head,decade)
	 SELECT ?,1,e.id,e.library_id,?,e.sort_key,e.sort_head,0 FROM catalog_entities e WHERE e.id=?`, class, kind, entity)
}

func bookContextValues(t *testing.T, db *sql.DB, book int64) (string, float64) {
	t.Helper()
	var series string
	var index float64
	if err := db.QueryRow(`SELECT series_name,series_index FROM catalog_book_context WHERE book_id=?`, book).Scan(&series, &index); err != nil {
		t.Fatal(err)
	}
	return series, index
}

func assertBookContextParity(t *testing.T, db *sql.DB, book int64) {
	t.Helper()
	var sourceSeries string
	var sourceIndex float64
	if err := db.QueryRow(`SELECT series_name,series_index FROM listening_book_context WHERE id=?`, book).Scan(&sourceSeries, &sourceIndex); err != nil {
		t.Fatal(err)
	}
	series, index := bookContextValues(t, db, book)
	if series != sourceSeries || index != sourceIndex {
		t.Fatalf("compact book context %q/%g != source %q/%g", series, index, sourceSeries, sourceIndex)
	}
}

func TestBookContextEvidenceDeltaRestartAndGroupIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	cc1Exec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES('books','Books','audiobook','/books')`)
	var lib, book, part1, part2 int64
	var token1, token2 string
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if lib, err = LibraryTx(ctx, tx, "books"); err != nil {
			return err
		}
		if book, _, err = UpsertEntityTx(ctx, tx, Entity{Library: lib, Kind: Book, Key: "book:book", Title: "The Book"}); err != nil {
			return err
		}
		if err = SetFactsTx(ctx, tx, book, map[string]any{"library_id": lib, "local_key": "book", "author": "Author", "narrator": "Narrator", "provider_match_status": "matched"}); err != nil {
			return err
		}
		if part1, _, err = UpsertEntityTx(ctx, tx, Entity{Library: lib, Kind: Part, Parent: book, Key: "file:part1", Title: "Part 1"}); err != nil {
			return err
		}
		if err = SetFactsTx(ctx, tx, part1, map[string]any{"book_id": book, "part_number": 1}); err != nil {
			return err
		}
		if part2, _, err = UpsertEntityTx(ctx, tx, Entity{Library: lib, Kind: Part, Parent: book, Key: "file:part2", Title: "Part 2"}); err != nil {
			return err
		}
		if err = SetFactsTx(ctx, tx, part2, map[string]any{"book_id": book, "part_number": 2}); err != nil {
			return err
		}
		var a1, a2 int64
		if a1, token1, err = UpsertAssetTx(ctx, tx, Asset{Path: "/books/a1.m4b", Size: 1, ModifiedNS: 1, Container: "m4b", AudioCodec: "aac", Duration: 10}); err != nil {
			return err
		}
		if err = LinkAssetTx(ctx, tx, part1, a1, Link{}); err != nil {
			return err
		}
		if a2, token2, err = UpsertAssetTx(ctx, tx, Asset{Path: "/books/a2.m4b", Size: 1, ModifiedNS: 1, Container: "m4b", AudioCodec: "aac", Duration: 10}); err != nil {
			return err
		}
		return LinkAssetTx(ctx, tx, part2, a2, Link{})
	})
	cc1Exec(t, db, `INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series','embedded','Saga'),('books',?,'series_title','embedded','saga'),('books',?,'series_index','embedded','1'),('books',?,'series_position','embedded','2')`, token1, token2, token1, token2)
	cc1Exec(t, db, `INSERT INTO listening_book_groups(id,library_id,kind,name,name_key) VALUES('author-id','books','author','Author','author'),('series-id','books','book_series','Saga','saga')`)
	cc1Drain(t, db)
	assertBookContextParity(t, db, book)
	series, index := bookContextValues(t, db, book)
	if series != "Saga" || index != 0 {
		t.Fatalf("case-folded series or conflicting position lost: %q/%g", series, index)
	}
	if n := cc1Scalar(t, db, `SELECT member_count FROM catalog_book_groups WHERE token='series-id'`); n != 1 {
		t.Fatalf("series group count = %d", n)
	}
	var stableID int64
	if err = db.QueryRow(`SELECT id FROM catalog_book_groups WHERE token='series-id'`).Scan(&stableID); err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `UPDATE audio_tag_evidence SET value='Other' WHERE asset_id=? AND field='series_title'`, token2)
	// The tag trigger queues a durable native job; it must survive a process
	// restart with no other journal state present.
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cc1Drain(t, db)
	assertBookContextParity(t, db, book)
	series, index = bookContextValues(t, db, book)
	if series != "" || index != 0 {
		t.Fatalf("conflicting series evidence admitted: %q/%g", series, index)
	}
	if n := cc1Scalar(t, db, `SELECT member_count FROM catalog_book_groups WHERE token='series-id'`); n != 0 {
		t.Fatalf("conflicted series still counted: %d", n)
	}
	cc1Exec(t, db, `DELETE FROM audio_tag_evidence WHERE asset_id=? AND field IN('series_title','series_position')`, token2)
	cc1Drain(t, db)
	assertBookContextParity(t, db, book)
	series, index = bookContextValues(t, db, book)
	if series != "Saga" || index != 1 {
		t.Fatalf("removed conflicting evidence did not restore context: %q/%g", series, index)
	}
	var afterID int64
	if err = db.QueryRow(`SELECT id FROM catalog_book_groups WHERE token='series-id'`).Scan(&afterID); err != nil || afterID != stableID {
		t.Fatalf("group identity changed on replay: %d -> %d %v", stableID, afterID, err)
	}
}
