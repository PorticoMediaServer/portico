package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
	"time"
)

func TestSeparateFolderAdmissionPublicationFence(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 5.74s")
	}
	for _, change := range []string{
		"UPDATE inventory_objects SET revision='r2' WHERE id=?",
		"UPDATE inventory_objects SET directory_path='other' WHERE id=?",
		"UPDATE inventory_objects SET state='unavailable' WHERE id=?",
		"UPDATE inventory_objects SET root_incarnation='other' WHERE id=?",
		"UPDATE inventory_objects SET asset_id=(SELECT token FROM catalog_assets WHERE token<>inventory_objects.asset_id LIMIT 1) WHERE id=?",
		"UPDATE library_sources SET generation=generation+1 WHERE id=(SELECT source_id FROM inventory_objects WHERE id=?)",
		"UPDATE library_sources SET root_identity='other' WHERE id=(SELECT source_id FROM inventory_objects WHERE id=?)",
		"UPDATE library_sources SET incarnation='other' WHERE id=(SELECT source_id FROM inventory_objects WHERE id=?)",
		"DELETE FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=(SELECT asset_id FROM inventory_objects WHERE id=?))",
		"INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT (SELECT max(entity_id) FROM catalog_songs),a.id FROM catalog_assets a JOIN inventory_objects o ON o.asset_id=a.token WHERE o.id=?",
		"INSERT INTO catalog_entities(public_id,library_id,kind,parent_id,title,sort_key,sort_head) SELECT randomblob(16),library_id,kind,parent_id,'new','new','n' FROM catalog_entities WHERE id=(SELECT entity_id FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=(SELECT asset_id FROM inventory_objects WHERE id=?)) LIMIT 1)",
		"DELETE FROM catalog_entities WHERE id=(SELECT entity_id FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=(SELECT asset_id FROM inventory_objects WHERE id=?)) LIMIT 1)",
		"UPDATE catalog_entities SET title='unrelated' WHERE id=(SELECT min(id) FROM catalog_entities) AND ?<>''",
	} {
		t.Run(change, func(t *testing.T) {
			f := newSeparateFolderCommandFixture(t, 2)
			s := New(f.c.DB)
			ctx := context.Background()
			p, err := s.separateFolderProof(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = dbwork.ExecWrite(ctx, f.c.DB, dbwork.ClassInteractive, change, f.objects[0]); err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, separateFolderProofKey{}, p)
			err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				_, _, _, err := separateFolderAdmitTx(ctx, tx, "music", f.objects[0], "r1")
				return err
			})
			if !errors.Is(err, dbwork.ErrWriterCapacity) {
				t.Fatalf("stale proof: %v", err)
			}
			if rows(t, f.c, "SELECT count(*) FROM catalog_entity_mutations") != 0 || rows(t, f.c, "SELECT count(*) FROM catalog_albums") != 1 {
				t.Fatal("stale proof created custody or target")
			}
		})
	}
}

func TestSeparateFolderAdmissionReadAuthorizationAndCancellation(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 2)
	s := New(f.c.DB)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.separateFolderProof(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	denied := errors.New("denied")
	if _, err := s.separateFolderProof(context.Background(), "missing", "missing", "r1", func(tx *sql.Tx) error {
		if tx == nil {
			t.Fatal("missing read tx")
		}
		return denied
	}); !errors.Is(err, denied) {
		t.Fatalf("authorize first: %v", err)
	}
	err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := s.separateFolderProof(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
		return err
	})
	if !errors.Is(err, dbwork.ErrNested) {
		t.Fatalf("nested: %v", err)
	}
}

func TestSeparateFolderReadmissionMovedSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured completed readmission parent 3.18s")
	}
	for _, moved := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmoved", true: "moved"}[moved], func(t *testing.T) {
			f := newSeparateFolderCommandFixture(t, 3)
			s := New(f.c.DB)
			ctx := context.Background()
			p, err := s.separateFolderProof(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, separateFolderProofKey{}, p)
			var ref compactcatalog.MutationRef
			err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				var e error
				ref, _, _, e = separateFolderAdmitTx(ctx, tx, "music", f.objects[0], "r1")
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			var target int64
			if err = f.c.DB.QueryRow("SELECT target_id FROM catalog_entity_mutations WHERE mutation_id=?", ref.ID).Scan(&target); err != nil {
				t.Fatal(err)
			}
			// Advance real command until the first selected file has moved, then abandon.
			co := SeparateFolderCoordinator{Hooks: SeparateFolderCoordinatorHooks{Authorize: func(*sql.Tx) error { return nil }}}
			if moved {
				for n := 0; n < 80; n++ {
					var parent int64
					f.c.DB.QueryRow("SELECT s.album_id FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE a.token=?", f.tokens[0]).Scan(&parent)
					if parent == target {
						break
					}
					err = dbwork.WithWriteTx(context.Background(), f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error { _, e := co.Step(context.Background(), tx, ref); return e })
					if err != nil {
						t.Fatal(err)
					}
					if n == 79 {
						t.Fatal("selected never moved")
					}
				}
			}
			err = dbwork.WithWriteTx(context.Background(), f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				_, e := tx.Exec("UPDATE library_sources SET generation=generation+1 WHERE id=?", f.source)
				if e != nil {
					return e
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// The real coordinator's abandon API is driven through per-turn source staleness
			// by the background lane; here terminate only after its bounded abandonment.
			for n := 0; n < 80; n++ {
				var status string
				f.c.DB.QueryRow("SELECT status FROM catalog_entity_mutations WHERE mutation_id=?", ref.ID).Scan(&status)
				if status == "terminal" {
					break
				}
				err = dbwork.WithWriteTx(context.Background(), f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
					m, e := compactcatalog.ReadEntityMutationTx(context.Background(), tx, ref)
					if e != nil {
						return e
					}
					_, e = abandonSeparateFolderTurnTx(context.Background(), tx, m)
					return e
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			selected := f.objects[0]
			if !moved {
				selected = f.objects[2]
			}
			var public string
			for attempt := 0; attempt < 8; attempt++ {
				public, err = s.SeparateFolder(context.Background(), "music", selected, "r1", func(*sql.Tx) error { return nil })
				if err == nil {
					break
				}
				if !errors.Is(err, ErrSeparateFolderPending) {
					t.Fatal(err)
				}
			}
			var expected string
			if e := f.c.DB.QueryRow("SELECT pid(public_id) FROM catalog_entities WHERE id=?", target).Scan(&expected); e != nil {
				t.Fatal(e)
			}
			if err != nil || public == "" || public != expected {
				t.Fatalf("new generation did not finish on the same target: got %q want %q error %v", public, expected, err)
			}
			if rows(t, f.c, "SELECT count(*) FROM catalog_albums") != 2 {
				t.Fatal("duplicate target")
			}
			if rows(t, f.c, "SELECT max(owner_generation) FROM catalog_entity_mutations WHERE parent_mutation_id IS NULL") != 2 {
				t.Fatal("fresh owner generation missing")
			}
		})
	}
}

func TestSeparateFolderAdmissionActorFence(t *testing.T) {
	for _, query := range []string{
		"UPDATE direct_memberships SET role='member' WHERE account_id=?",
		"UPDATE direct_memberships SET incarnation=lower(hex(randomblob(16))) WHERE account_id=?",
		"UPDATE direct_profiles SET lifetime=lower(hex(randomblob(16))) WHERE account_id=?",
	} {
		t.Run(query, func(t *testing.T) {
			f := newSeparateAsyncFixture(t, 2)
			s := New(f.c.DB)
			ctx := WithSeparateFolderActor(context.Background(), f.principal)
			p, err := s.separateFolderProof(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = dbwork.ExecWrite(ctx, f.c.DB, dbwork.ClassInteractive, query, f.account); err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, separateFolderProofKey{}, p)
			err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				_, _, _, e := separateFolderAdmitTx(ctx, tx, "music", f.objects[0], "r1")
				return e
			})
			if !errors.Is(err, dbwork.ErrWriterCapacity) {
				t.Fatalf("actor publication: %v", err)
			}
			if rows(t, f.c, "SELECT count(*) FROM catalog_entity_mutations") != 0 || rows(t, f.c, "SELECT count(*) FROM catalog_albums") != 1 {
				t.Fatal("actor race created effects")
			}
		})
	}
}

func TestSeparateFolderAdmissionSnapshotExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: real two-second snapshot deadline; measured normal parent 2.56s")
	}
	f := newSeparateFolderCommandFixture(t, 2)
	_, err := New(f.c.DB).separateFolderProof(context.Background(), "music", f.objects[0], "r1", func(tx *sql.Tx) error {
		<-time.After(2100 * time.Millisecond)
		var one int
		return tx.QueryRow("SELECT 1").Scan(&one)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("snapshot expiry: %v", err)
	}
	if rows(t, f.c, "SELECT count(*) FROM catalog_entity_mutations") != 0 {
		t.Fatal("expired proof admitted")
	}
}
func TestSeparateFolderAdmissionNoneAllRefused(t *testing.T) {
	for _, dir := range []string{"none", "mp3"} {
		t.Run(dir, func(t *testing.T) {
			f := newSeparateFolderCommandFixture(t, 2)
			if dir == "mp3" {
				if _, err := dbwork.ExecWrite(context.Background(), f.c.DB, dbwork.ClassInteractive, "UPDATE inventory_objects SET directory_path='mp3'"); err != nil {
					t.Fatal(err)
				}
			}
			var inc string
			f.c.DB.QueryRow("SELECT incarnation FROM library_sources WHERE id=?", f.source).Scan(&inc)
			err := dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
				return separateFolderAdmissionWindowTx(ctx, dbwork.Snapshot(ctx), f.album, compactcatalog.Album, f.source, inc, dir)
			})
			if !errors.Is(err, ErrAdminQuery) {
				t.Fatalf("none/all: %v", err)
			}
		})
	}
}

func TestSeparateFolderAdmissionRawWindowBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: 8000-row scale boundary; measured normal parent 2.63s")
	}
	for _, empty := range []int{7994, 7995} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			f := newSeparateFolderCommandFixture(t, 2)
			var library int64
			if err := f.c.DB.QueryRow("SELECT library_id FROM catalog_entities WHERE id=?", f.album).Scan(&library); err != nil {
				t.Fatal(err)
			}
			// Two real children + four real links consume six raw candidates. Empty
			// children consume the remainder even though none can contribute a file.
			f.c.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
   INSERT INTO catalog_entities(public_id,library_id,kind,parent_id,title,sort_key,sort_head)
   SELECT randomblob(16),?,?,?,'empty','empty','e' FROM n`, empty, library, int(compactcatalog.Track), f.album)
			var inc string
			f.c.DB.QueryRow("SELECT incarnation FROM library_sources WHERE id=?", f.source).Scan(&inc)
			err := dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
				return separateFolderAdmissionWindowTx(ctx, dbwork.Snapshot(ctx), f.album, compactcatalog.Album, f.source, inc, "mp3")
			})
			if empty == 7994 && err != nil {
				t.Fatal(err)
			}
			if empty == 7995 && !errors.Is(err, ErrAdminQuery) {
				t.Fatalf("raw 8001 admitted: %v", err)
			}
		})
	}
}

func TestSeparateFolderAdmissionFileLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: 4000-file scale boundary; measured normal parent 13.65s")
	}
	for _, links := range []int{4000, 4002} {
		t.Run(fmt.Sprint(links), func(t *testing.T) {
			c, object := newSeparateFolderAdmissionScale(t, links, true)
			_, err := New(c.DB).separateFolderProof(context.Background(), "music", object, "r1", func(*sql.Tx) error { return nil })
			if links == 4000 && err != nil {
				t.Fatal(err)
			}
			if links == 4002 && !errors.Is(err, ErrAdminQuery) {
				t.Fatalf("4002 links admitted: %v", err)
			}
		})
	}
}

func TestSeparateFolderAdmissionCheapResumeCannotBecomeNewAdmission(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 2)
	s := New(f.c.DB)
	ctx := context.Background()
	p, err := s.separateFolderProof(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	admitCtx := context.WithValue(ctx, separateFolderProofKey{}, p)
	var ref compactcatalog.MutationRef
	err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var e error
		ref, _, _, e = separateFolderAdmitTx(admitCtx, tx, "music", f.objects[0], "r1")
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, f.c.DB, dbwork.ClassInteractive, "UPDATE inventory_objects SET revision='r2' WHERE id=?", f.objects[0]); err != nil {
		t.Fatal(err)
	}
	readCtx, cost := dbwork.Measure(ctx)
	resume, err := s.separateFolderProof(readCtx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if resume.ready || cost().Statements > 16 {
		t.Fatalf("resume scanned admission: ready=%v cost=%+v", resume.ready, cost())
	}
	err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if e != nil {
			return e
		}
		_, e = abandonSeparateFolderTurnTx(ctx, tx, m)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	resumeCtx := context.WithValue(ctx, separateFolderProofKey{}, resume)
	err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, _, _, e := separateFolderAdmitTx(resumeCtx, tx, "music", f.objects[0], "r1")
		return e
	})
	if !errors.Is(err, dbwork.ErrWriterCapacity) {
		t.Fatalf("resume became admission: %v", err)
	}
	if rows(t, f.c, "SELECT max(owner_generation) FROM catalog_entity_mutations") != 1 {
		t.Fatal("no-proof generation admitted")
	}
}
